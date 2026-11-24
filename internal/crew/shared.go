package crew

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

// SidecarSuffix names the provenance file beside a shared member's file.
const SidecarSuffix = ".shared.json"

// maxIntDigits is the longest integer literal Python parses; a longer one makes a sidecar unreadable in both resolvers.
const maxIntDigits = 4300

var (
	repoSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	commitPattern      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256Pattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Shared is where a member came from, read from its sidecar.
type Shared struct {
	Owner    string  `json:"owner"`
	Repo     string  `json:"repo"`
	Commit   string  `json:"commit"`
	Tag      *string `json:"tag"`
	Detached bool    `json:"detached"`
}

type sidecarProblem string

func (p sidecarProblem) Error() string { return string(p) }

// readSidecar returns nil, nil when the member has no sidecar, and a problem when one is there but unusable.
func readSidecar(dir, layerReal, stem string, memberRaw []byte) (*Shared, error) {
	path := filepath.Join(dir, stem+SidecarSuffix)
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nil, nil
		}
		return nil, sidecarProblem("sidecar can't be read: " + strerror(err))
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return nil, sidecarProblem("sidecar can't be read: " + strerror(err))
	case !info.Mode().IsRegular():
		return nil, sidecarProblem("sidecar is not a regular file")
	case info.Size() > maxBytes:
		return nil, sidecarProblem("sidecar is over 65536 bytes")
	case !within(realPath(path), layerReal):
		return nil, sidecarProblem("sidecar resolves outside its layer directory")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, sidecarProblem("sidecar can't be read: " + strerror(err))
	}
	return parseSidecar(raw, memberRaw)
}

func parseSidecar(raw, memberRaw []byte) (*Shared, error) {
	if !utf8.Valid(raw) {
		return nil, sidecarProblem("sidecar is not valid UTF-8")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, sidecarProblem("sidecar is not a JSON object")
	}
	if err := checkIntegerLiterals(raw); err != nil {
		return nil, err
	}
	var s Shared
	if v, ok := fields["version"]; !ok || string(bytes.TrimSpace(v)) != "1" {
		return nil, sidecarProblem("version must be the integer 1")
	}
	if host, ok := stringField(fields, "host"); !ok || host != "github.com" {
		return nil, sidecarProblem("host must be github.com")
	}
	repo, ok := stringField(fields, "repo")
	if !ok {
		return nil, sidecarProblem("repo must be owner/name")
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || !validSegment(owner) || !validSegment(name) {
		return nil, sidecarProblem("repo must be owner/name")
	}
	if path, ok := stringField(fields, "path"); !ok || !validSharePath(path) {
		return nil, sidecarProblem("path must be crew/<file>.md")
	}
	commit, ok := stringField(fields, "commit")
	if !ok || !commitPattern.MatchString(commit) {
		return nil, sidecarProblem("commit must be 40 lowercase hex characters")
	}
	sum, ok := stringField(fields, "content_sha256")
	if !ok || !sha256Pattern.MatchString(sum) {
		return nil, sidecarProblem("content_sha256 must be 64 lowercase hex characters")
	}
	tag, err := readTag(fields)
	if err != nil {
		return nil, err
	}
	have := sha256.Sum256(memberRaw)
	s.Owner, s.Repo, s.Commit, s.Tag = owner, name, commit, tag
	s.Detached = hex.EncodeToString(have[:]) != sum
	return &s, nil
}

func stringField(fields map[string]json.RawMessage, key string) (string, bool) {
	v, ok := fields[key]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", false
	}
	return s, true
}

func readTag(fields map[string]json.RawMessage) (*string, error) {
	v, ok := fields["tag"]
	if !ok || string(bytes.TrimSpace(v)) == "null" {
		return nil, nil
	}
	// encoding/json turns a lone surrogate escape into U+FFFD, which would hide it.
	if hasLoneSurrogate(v) {
		return nil, sidecarProblem("tag holds a lone surrogate")
	}
	var tag string
	if err := json.Unmarshal(v, &tag); err != nil || tag == "" || hiddenChar(tag) {
		return nil, sidecarProblem("tag must be a non-empty string with no hidden characters")
	}
	return &tag, nil
}

// hasLoneSurrogate scans a JSON string's escapes for a surrogate half without its partner.
func hasLoneSurrogate(raw []byte) bool {
	s := string(raw)
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			continue
		}
		if i+1 >= len(s) {
			return false
		}
		if s[i+1] != 'u' {
			i++
			continue
		}
		unit, ok := hexUnit(s, i+2)
		if !ok {
			return false
		}
		i += 5
		switch {
		case unit >= 0xD800 && unit <= 0xDBFF:
			low, paired := uint64(0), false
			if i+2 < len(s) && s[i+1] == '\\' && s[i+2] == 'u' {
				low, paired = hexUnit(s, i+3)
			}
			if !paired || low < 0xDC00 || low > 0xDFFF {
				return true
			}
			i += 6
		case unit >= 0xDC00 && unit <= 0xDFFF:
			return true
		}
	}
	return false
}

func hexUnit(s string, at int) (uint64, bool) {
	if at+4 > len(s) {
		return 0, false
	}
	n, err := strconv.ParseUint(s[at:at+4], 16, 32)
	return n, err == nil
}

// checkIntegerLiterals refuses an integer literal Python can't parse, wherever it sits.
func checkIntegerLiterals(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return sidecarProblem("sidecar is not valid JSON")
		}
		if n, ok := tok.(json.Number); ok {
			lit := strings.TrimPrefix(n.String(), "-")
			if !strings.ContainsAny(lit, ".eE") && len(lit) > maxIntDigits {
				return sidecarProblem("sidecar holds an integer over 4300 digits")
			}
		}
	}
}

func validSegment(s string) bool { return s != "." && s != ".." && repoSegmentPattern.MatchString(s) }

func validSharePath(p string) bool {
	file, ok := strings.CutPrefix(p, "crew/")
	return ok && strings.HasSuffix(file, ".md") && !strings.HasPrefix(file, ".") && !strings.Contains(file, "/")
}
