package crew

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const noteCap = 30

var (
	// \S is spelled out: RE2's leaves out \v, which Python's ASCII \s counts as space.
	notePattern    = regexp.MustCompile(`^- ([0-9]{4})-([0-9]{2})-([0-9]{2}) \(([A-Za-z0-9._-]+)\): [^ \t\n\r\f\v].*$`)
	segmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

type memoryProblem struct{ code, message string }

func memoryWarning(code, rel, message string) Diagnostic {
	return newDiag(layerMemory, rel, code, "", message)
}

func (p *memoryProblem) warning(rel string) Diagnostic { return memoryWarning(p.code, rel, p.message) }

func describe(rel string) string {
	if rel == "" {
		return "the memory root"
	}
	return rel
}

func isMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

func look(path, rel string) (fs.FileInfo, *memoryProblem) {
	info, err := os.Lstat(path)
	if err != nil {
		if isMissing(err) {
			return nil, nil
		}
		return nil, &memoryProblem{"memory-unreadable", "can't be read: " + strerror(err)}
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, &memoryProblem{"memory-refused", describe(rel) + " is a symlink, skipped"}
	}
	return info, nil
}

func lookDir(path, rel string) (bool, *memoryProblem) {
	info, prob := look(path, rel)
	if prob != nil || info == nil {
		return false, prob
	}
	if !info.IsDir() {
		return false, &memoryProblem{"memory-unreadable", describe(rel) + " is not a directory, skipped"}
	}
	return true, nil
}

func readNoteFile(path, rel string) ([]string, *memoryProblem) {
	info, prob := look(path, rel)
	if prob != nil || info == nil {
		return nil, prob
	}
	if !info.Mode().IsRegular() {
		return nil, &memoryProblem{"memory-unreadable", rel + " is not a regular file, skipped"}
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	var raw []byte
	if err == nil {
		defer f.Close()
		raw, err = io.ReadAll(io.LimitReader(f, maxBytes+1))
	}
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, &memoryProblem{"memory-refused", rel + " is a symlink, skipped"}
		}
		return nil, &memoryProblem{"memory-unreadable", rel + " can't be read: " + strerror(err)}
	}
	switch {
	case len(raw) > maxBytes:
		return nil, &memoryProblem{"memory-unreadable", rel + " is over 65536 bytes, skipped"}
	case !utf8.Valid(raw):
		return nil, &memoryProblem{"memory-unreadable", rel + " is not valid UTF-8, skipped"}
	case strings.ContainsRune(string(raw), 0):
		return nil, &memoryProblem{"memory-unreadable", rel + " is not text, skipped"}
	}
	return strings.Split(string(raw), "\n"), nil
}

// Characters the engineer can't see when approving a note; other spaces show as spaces and are allowed.
func hiddenChar(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Cs, unicode.Co, unicode.Zl, unicode.Zp) ||
			!unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z, unicode.C) {
			return true
		}
	}
	return false
}

func realDate(year, month, day int) bool {
	if year < 1 {
		return false
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return t.Year() == year && int(t.Month()) == month && t.Day() == day
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func parseNotes(lines []string, rel string, warns *[]Diagnostic) []string {
	notes := []string{}
	over := false
	for i, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		m := notePattern.FindStringSubmatch(line)
		if m == nil || !realDate(atoi(m[1]), atoi(m[2]), atoi(m[3])) || hiddenChar(line) {
			*warns = append(*warns, memoryWarning("bad-note", rel,
				"line "+strconv.Itoa(i+1)+" is not a note (- YYYY-MM-DD (<run>): <fact>), skipped"))
		} else if len(notes) < noteCap {
			notes = append(notes, line[2:])
		} else {
			over = true
		}
	}
	if over {
		*warns = append(*warns, memoryWarning("notes-over-cap", rel, "more than 30 notes, only the first 30 load"))
	}
	return notes
}

func loadScope(path, rel string, warns *[]Diagnostic) []string {
	lines, prob := readNoteFile(path, rel)
	if prob != nil {
		*warns = append(*warns, prob.warning(rel))
		return []string{}
	}
	if lines == nil {
		return []string{}
	}
	return parseNotes(lines, rel, warns)
}

func loadMemberNotes(root, name, slug string, warns *[]Diagnostic) Notes {
	notes := Notes{Fleet: []string{}, Repo: []string{}}
	dir := filepath.Join(root, name)
	ok, prob := lookDir(dir, name)
	if prob != nil {
		*warns = append(*warns, prob.warning(name))
		return notes
	}
	if !ok {
		return notes
	}
	notes.Fleet = loadScope(filepath.Join(dir, "fleet.md"), name+"/fleet.md", warns)
	if slug == "" {
		return notes
	}
	owner, repo, _ := strings.Cut(slug, "/")
	path, rel := dir, name
	for _, part := range []string{"repos", owner} {
		path, rel = filepath.Join(path, part), rel+"/"+part
		ok, prob := lookDir(path, rel)
		if prob != nil {
			*warns = append(*warns, prob.warning(rel))
			return notes
		}
		if !ok {
			return notes
		}
	}
	notes.Repo = loadScope(filepath.Join(path, repo+".md"), rel+"/"+repo+".md", warns)
	return notes
}

func segmentsOK(parts ...string) bool {
	for _, p := range parts {
		if !segmentPattern.MatchString(p) || p == "." || p == ".." {
			return false
		}
	}
	return true
}

// attachNotes gives every member its notes and returns the memory warnings. A fix checker's files are never opened.
func attachNotes(members []Member, root, repo string) []Diagnostic {
	for i := range members {
		members[i].Notes = Notes{Fleet: []string{}, Repo: []string{}}
	}
	var targets []int
	for i, m := range members {
		if m.Role != fixCheckerKey {
			targets = append(targets, i)
		}
	}
	if root == "" || len(targets) == 0 {
		return nil
	}
	var warns []Diagnostic
	ok, prob := lookDir(root, "")
	if prob != nil {
		return []Diagnostic{prob.warning("")}
	}
	if !ok {
		return nil
	}
	slug := ""
	if repo != "" {
		owner, name, found := strings.Cut(repo, "/")
		if found && segmentsOK(owner, name) {
			slug = repo
		} else {
			warns = append(warns, memoryWarning("bad-repo", "", "the repo is not github.com/<owner>/<repo>, no repo notes load"))
		}
	}
	for _, i := range targets {
		members[i].Notes = loadMemberNotes(root, members[i].Name, slug, &warns)
	}
	return warns
}
