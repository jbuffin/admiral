package crewapi

import (
	"errors"
	"os"
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

// remove deletes the scope's file, which a delete or a move has left with no notes.
func (s scopeFile) remove() error {
	dirs, file := s.location()
	dir, ok, err := walk(s.crewDir, false, dirs...)
	if err != nil || !ok {
		return err
	}
	return os.Remove(filepath.Join(dir, file))
}

// noNotes is true for lines holding nothing but blank lines.
func noNotes(lines []string) bool {
	for _, l := range lines {
		if strings.Trim(l, " \t\r") != "" {
			return false
		}
	}
	return true
}

// appended is lines with line added as the last note, after the file's own trailing newline.
func appended(lines []string, line string) []string {
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return append(lines[:len(lines):len(lines)], line, "")
}

func (s scopeFile) full(lines []string) bool { return len(crew.ParseNotes(lines)) >= crew.NoteCap }

// AppendNote adds one note ("- YYYY-MM-DD (<run>): <fact>") to a member's scope under fleet key's crew
// directory in the Admiral home; repo is "<owner>/<repo>", or empty for the fleet scope. It makes the
// directories and the file as needed, refuses a path that holds a link anywhere from the crew directory
// down, answers ErrScopeFull at the cap and writes atomically.
func AppendNote(home, key, member, repo, line string) error {
	if !crew.ValidName(member) || !crew.ValidNote(line) {
		return errors.New("not a note for a member")
	}
	if _, _, ok := crew.SplitRepo(repo); repo != "" && !ok {
		return errors.New("repo is not <owner>/<repo>")
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	crewDir, found, err := walk(home, false, "fleets", key, "crew")
	if err != nil {
		return err
	}
	s := scopeFile{crewDir, member, repo}
	var lines []string
	if found {
		if lines, _, err = s.read(); err != nil {
			return err
		}
	}
	if s.full(lines) {
		return ErrScopeFull
	}
	if !found {
		if crewDir, _, err = walk(home, true, "fleets", key, "crew"); err != nil {
			return err
		}
		s.crewDir = crewDir
	}
	return s.write(appended(lines, line))
}
