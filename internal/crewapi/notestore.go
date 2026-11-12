package crewapi

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/jbuffin/admiral/internal/crew"
)

// ErrScopeFull is what AppendNote and a move answer for a scope that already holds the cap of notes.
var ErrScopeFull = errors.New("that scope already holds 30 notes")

// scopeFile is one notes file of a member under a fleet's crew directory: the fleet's, or one repo's.
type scopeFile struct {
	crewDir string
	member  string
	repo    string // "<owner>/<repo>", empty for the fleet scope
}

func (s scopeFile) location() (dirs []string, file string) {
	dirs = []string{"memory", s.member}
	if s.repo == "" {
		return dirs, "fleet.md"
	}
	owner, repo, _ := crew.SplitRepo(s.repo)
	return append(dirs, "repos", owner), repo + ".md"
}

func (s scopeFile) read() (lines []string, found bool, err error) {
	dirs, file := s.location()
	dir, ok, err := walk(s.crewDir, false, dirs...)
	if err != nil || !ok {
		return nil, false, err
	}
	raw, found, err := readRegular(filepath.Join(dir, file))
	if err != nil || !found {
		return nil, false, err
	}
	return strings.Split(string(raw), "\n"), true, nil
}

func (s scopeFile) write(lines []string) error {
	dirs, file := s.location()
	dir, _, err := walk(s.crewDir, true, dirs...)
	if err != nil {
		return err
	}
	return writeAtomic(dir, file, []byte(strings.Join(lines, "\n")))
}

// appended is lines with line added as the last note, after the file's own trailing newline.
func appended(lines []string, line string) []string {
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return append(lines[:len(lines):len(lines)], line, "")
}

func (s scopeFile) full(lines []string) bool { return len(crew.ParseNotes(lines)) >= crew.NoteCap }

// AppendNote adds one note ("- YYYY-MM-DD (<run>): <fact>") to a member's scope under a fleet's crew
// directory; repo is "<owner>/<repo>", or empty for the fleet scope. It makes the directories and the file
// as needed, refuses a path that holds a link, answers ErrScopeFull at the cap and writes atomically.
func AppendNote(crewDir, member, repo, line string) error {
	if !crew.ValidName(member) || !crew.ValidNote(line) {
		return errors.New("not a note for a member")
	}
	if _, _, ok := crew.SplitRepo(repo); repo != "" && !ok {
		return errors.New("repo is not <owner>/<repo>")
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	s := scopeFile{crewDir, member, repo}
	lines, _, err := s.read()
	if err != nil {
		return err
	}
	if s.full(lines) {
		return ErrScopeFull
	}
	return s.write(appended(lines, line))
}
