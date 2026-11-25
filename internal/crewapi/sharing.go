package crewapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"

	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/textsafe"
)

// Sidecar is the provenance file beside an imported member, to Commodore's crew-members.md schema.
type Sidecar struct {
	Version       int     `json:"version"`
	Host          string  `json:"host"`
	Repo          string  `json:"repo"`
	Path          string  `json:"path"`
	UpstreamName  string  `json:"upstream_name"`
	Commit        string  `json:"commit"`
	Tag           *string `json:"tag"`
	UpstreamBlob  string  `json:"upstream_blob"`
	ContentSHA256 string  `json:"content_sha256"`
	ImportedAt    string  `json:"imported_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func sidecarFile(name string) string { return name + crew.SidecarSuffix }

// WriteSidecar writes <name>.shared.json in dir, keeping the unknown fields of one already there.
func WriteSidecar(dir, name string, s Sidecar) error {
	own, err := json.Marshal(s)
	if err != nil {
		return err
	}
	var set map[string]json.RawMessage
	if err := json.Unmarshal(own, &set); err != nil {
		return err
	}
	base, _, err := readRegular(filepath.Join(dir, sidecarFile(name)))
	if err != nil {
		return err
	}
	out, err := mergeSidecar(base, set)
	if err != nil {
		return err
	}
	return writeAtomic(dir, sidecarFile(name), out)
}

// mergeSidecar sets the named raw fields on base's fields and leaves every other key as it was, with its key's exact
// case. It never decodes into a struct, which would match keys without regard to case.
func mergeSidecar(base []byte, set map[string]json.RawMessage) ([]byte, error) {
	fields := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(base)) > 0 {
		if err := json.Unmarshal(base, &fields); err != nil {
			return nil, fmt.Errorf("the sidecar isn't a JSON object: %w", err)
		}
		if fields == nil {
			return nil, errors.New("the sidecar isn't a JSON object")
		}
	}
	for k, v := range set {
		fields[k] = v
	}
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// SidecarPresent says whether anything, of any kind, a dangling link included, is at <name>.shared.json in dir.
func SidecarPresent(dir, name string) (bool, error) {
	_, err := os.Lstat(filepath.Join(dir, sidecarFile(name)))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return false, nil
	}
	return false, err
}

// CanonicalMemberBytes is the file Admiral would publish for a resolved member.
func CanonicalMemberBytes(m crew.Member) []byte {
	return memberBody{Role: m.Role, BasedOn: m.BasedOn, Skills: m.Skills, Model: m.Model, Harness: m.Harness,
		PickMeWhen: m.PickMeWhen, Instructions: m.Instructions}.file(m.Name)
}

func (s *Service) serveMakeMine(w http.ResponseWriter, r *http.Request) {
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return
	}
	name, ok := memberName(w, r)
	if !ok {
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	dir, ok := s.fleetFile(w, r, c, name)
	if !ok {
		return
	}
	present, err := SidecarPresent(dir, name)
	switch {
	case err != nil:
		refuse(w, err)
		return
	case !present:
		writeError(w, http.StatusNotFound, name+" has no "+sidecarFile(name)+": it isn't tracking a shared member")
		return
	}
	// A non-recursive remove takes a file, a link (never its target) or an empty directory.
	if err := os.Remove(filepath.Join(dir, sidecarFile(name))); err != nil {
		if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			writeError(w, http.StatusConflict, sidecarFile(name)+" is a directory with files in it; Admiral doesn't delete those")
			return
		}
		refuse(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// sidecarMove is what a rename carries to the new name. rewritten holds the bytes of an attached sidecar re-hashed for
// the renamed file; it is nil for any other sidecar, which moves as its directory entry.
type sidecarMove struct {
	present   bool
	rewritten []byte
}

// prepareSidecarMove decides, before anything moves, how the member's sidecar follows a rename. Only an attached one,
// read through the resolver's guarded reader, is rewritten; everything else is moved as the entry it is.
func prepareSidecarMove(dir, name string, oldMember, newMember []byte) (sidecarMove, error) {
	present, err := SidecarPresent(dir, name)
	if err != nil || !present {
		return sidecarMove{}, err
	}
	layerReal, rerr := filepath.EvalSymlinks(dir)
	if rerr != nil {
		layerReal = filepath.Clean(dir)
	}
	shared, raw, perr := crew.LoadSidecar(dir, layerReal, name, oldMember)
	if perr != nil || shared == nil || shared.Detached {
		return sidecarMove{present: true}, nil
	}
	sum := sha256.Sum256(newMember)
	hash, err := json.Marshal(hex.EncodeToString(sum[:]))
	if err != nil {
		return sidecarMove{}, err
	}
	out, err := mergeSidecar(raw, map[string]json.RawMessage{"content_sha256": hash})
	if err != nil {
		return sidecarMove{}, fmt.Errorf("%s can't be re-hashed: %w", sidecarFile(name), err)
	}
	return sidecarMove{present: true, rewritten: out}, nil
}

// place puts the sidecar at the new name and returns what undoes it.
func (m sidecarMove) place(dir, from, to string) (undo func(), err error) {
	if !m.present {
		return func() {}, nil
	}
	newPath := filepath.Join(dir, sidecarFile(to))
	if m.rewritten != nil {
		if err := writeAtomic(dir, sidecarFile(to), m.rewritten); err != nil {
			return nil, err
		}
		return func() { _ = os.Remove(newPath) }, nil
	}
	oldPath := filepath.Join(dir, sidecarFile(from))
	if err := os.Rename(oldPath, newPath); err != nil {
		return nil, err
	}
	return func() { _ = os.Rename(newPath, oldPath) }, nil
}

// removeMemberAndSidecar deletes name.md without ever leaving its sidecar behind: the sidecar steps aside first,
// and goes back if the member can't be removed.
func removeMemberAndSidecar(dir, name string) error {
	sidecar := filepath.Join(dir, sidecarFile(name))
	aside := filepath.Join(dir, "."+sidecarFile(name)+".removing")
	present, err := SidecarPresent(dir, name)
	if err != nil {
		return err
	}
	if present {
		if err := os.Rename(sidecar, aside); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(dir, name+".md")); err != nil {
		if present {
			_ = os.Rename(aside, sidecar)
		}
		return err
	}
	if present {
		return os.Remove(aside)
	}
	return nil
}

// The publish checks.
const (
	CheckPaths         = "paths"
	CheckHidden        = "hidden"
	CheckGitleaksAllow = "gitleaks-allow"
)

// PublishPaths are the places on the Mac a published file must not name.
type PublishPaths struct {
	AdmiralHome string
	Home        string
	CloneRoot   string
	SearchRoots []string
}

// Refusal is the first publish check a file fails; Message names the rule and the line.
type Refusal struct {
	Check   string
	Line    int
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// CheckPublish runs the canonical bytes through the path, hidden-character and gitleaks:allow checks, in that order.
func CheckPublish(data []byte, p PublishPaths) *Refusal {
	lines := strings.Split(string(data), "\n")
	if r := checkPaths(lines, p); r != nil {
		return r
	}
	if r := checkHidden(lines); r != nil {
		return r
	}
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), "gitleaks:allow") {
			return &Refusal{CheckGitleaksAllow, i + 1, fmt.Sprintf("gitleaks:allow on line %d: a published file can't switch the secret scan off", i+1)}
		}
	}
	return nil
}

func checkPaths(lines []string, p PublishPaths) *Refusal {
	roots := append([]string{p.AdmiralHome, p.Home, p.CloneRoot}, p.SearchRoots...)
	home := ""
	if p.Home != "" {
		home = filepath.Clean(p.Home)
	}
	var spellings []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		spellings = append(spellings, root)
		if home != "" && strings.HasPrefix(root, strings.TrimSuffix(home, "/")+"/") {
			rest := strings.TrimPrefix(root, home)
			for _, shorthand := range homeShorthands {
				spellings = append(spellings, strings.TrimSuffix(shorthand, "/")+rest)
			}
		}
	}
	base := ""
	if home != "" {
		base = filepath.Base(home)
	}
	for i, line := range lines {
		named := ""
		for _, sp := range spellings {
			if namesPath(line, sp) {
				named = sp
				break
			}
		}
		if named == "" && home != "" && homeShorthand(line) {
			named = "~/"
		}
		if named == "" && base != "" && base != "/" && base != "." && hasSegment(line, base) {
			named = "/" + base
		}
		if named != "" {
			return &Refusal{CheckPaths, i + 1, fmt.Sprintf("a path on this Mac (%s) on line %d: use a path relative to the repo", named, i+1)}
		}
	}
	return nil
}

func pathChar(r byte) bool {
	return r == '_' || r == '.' || r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// namesPath is whether line holds path as a whole path or the front of a longer one.
func namesPath(line, path string) bool {
	path = strings.TrimSuffix(path, "/")
	for from := 0; ; {
		i := strings.Index(line[from:], path)
		if i < 0 {
			return false
		}
		end := from + i + len(path)
		if end == len(line) || !pathChar(line[end]) {
			return true
		}
		from += i + 1
	}
}

// homeShorthands are the ways a path can spell the home directory without naming it.
var homeShorthands = []string{"~/", "$HOME/", "${HOME}/"}

// homeShorthand is one of homeShorthands that starts a word.
func homeShorthand(line string) bool {
	for _, sh := range homeShorthands {
		for from := 0; ; {
			i := strings.Index(line[from:], sh)
			if i < 0 {
				break
			}
			at := from + i
			if at == 0 || !pathChar(line[at-1]) {
				return true
			}
			from = at + 1
		}
	}
	return false
}

// hasSegment is whether an absolute path in line has seg as one of its segments.
func hasSegment(line, seg string) bool {
	words := strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune("\"'`()[]{}<>,;=:", r) || unicode.IsSpace(r) })
	for _, w := range words {
		if !strings.HasPrefix(w, "/") || strings.HasPrefix(w, "//") {
			continue
		}
		for _, part := range strings.Split(w, "/") {
			// macOS paths ignore case.
			if strings.EqualFold(part, seg) {
				return true
			}
		}
	}
	return false
}

func checkHidden(lines []string) *Refusal {
	if r, line := firstHidden(lines); line > 0 {
		return hiddenRefusal(r, line)
	}
	return nil
}

// firstHidden is the first code point a reader can't see and its 1-based line, or line 0 when there is none.
func firstHidden(lines []string) (rune, int) {
	for i, line := range lines {
		for _, r := range line {
			if publishHidden(r) {
				return r, i + 1
			}
		}
		if n, first := hidden(line, true); n > 0 {
			return first, i + 1
		}
	}
	return 0, 0
}

func hiddenRefusal(r rune, line int) *Refusal {
	return &Refusal{CheckHidden, line, fmt.Sprintf("U+%04X on line %d: a published file can't hold a character a reader can't see", r, line)}
}

// publishHidden is the union of every hidden-character rule in Admiral, plus every variation selector and blank filler.
func publishHidden(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	switch {
	case unicode.In(r, unicode.Cc, unicode.Cf, unicode.Cs, unicode.Co, unicode.Cn, unicode.Zl, unicode.Zp):
		return true
	case textsafe.Hidden(r):
		return true
	case r >= 0xE0000 && r <= 0xE007F, r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF:
		return true
	case unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r), unicode.Is(unicode.Variation_Selector, r):
		return true
	}
	switch r {
	case 0x115F, 0x1160, 0x3164, 0xFFA0, 0x2800:
		return true
	}
	return false
}
