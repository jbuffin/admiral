package crewapi

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/skillindex"
)

// ErrReplacesMissing is what a note answers when the line it replaces is not in its scope.
var ErrReplacesMissing = errors.New("the line this note replaces is not in its scope")

// NoteWrite is one note for a member's scope: Repo is "<owner>/<repo>" or empty for the fleet scope, Line is
// "- YYYY-MM-DD (<run>): <fact>" and Replaces, when set, is the whole line it takes the place of, without the dash.
type NoteWrite struct{ Member, Repo, Line, Replaces string }

// NoteRefusedError names the first write, by its index in the list, that doesn't fit; Err is ErrScopeFull or ErrReplacesMissing.
type NoteRefusedError struct {
	Index int
	Err   error
}

func (e *NoteRefusedError) Error() string { return e.Err.Error() }
func (e *NoteRefusedError) Unwrap() error { return e.Err }

// MemberRefusedError is a keep that can't go ahead, with nothing changed.
type MemberRefusedError struct{ Msg string }

func (e *MemberRefusedError) Error() string { return e.Msg }

func safeKey(key string) bool {
	return key != "" && filepath.IsLocal(key) && !strings.ContainsRune(key, filepath.Separator)
}

// factOf is the text after a note line's "(<run>): ".
func factOf(line string) string {
	_, fact, _ := strings.Cut(line, "): ")
	return fact
}

type scopePlan struct {
	scope scopeFile
	lines []string
	orig  []string // what the file held when it was read, to put back
	found bool     // the file existed
}

// planNotes applies writes to the scopes in memory, in order, and refuses the first that doesn't fit.
func planNotes(crewDir string, writes []NoteWrite) ([]*scopePlan, error) {
	var plans []*scopePlan
	for i, w := range writes {
		if !crew.ValidName(w.Member) || !crew.ValidNote(w.Line) {
			return nil, errors.New("not a note for a member")
		}
		if _, _, ok := crew.SplitRepo(w.Repo); w.Repo != "" && !ok {
			return nil, errors.New("repo is not <owner>/<repo>")
		}
		sf := scopeFile{crewDir, w.Member, w.Repo}
		at := slices.IndexFunc(plans, func(p *scopePlan) bool { return p.scope == sf })
		if at < 0 {
			lines, found, err := sf.read()
			if err != nil {
				return nil, err
			}
			plans = append(plans, &scopePlan{sf, lines, slices.Clone(lines), found})
			at = len(plans) - 1
		}
		p := plans[at]
		fact := factOf(w.Line)
		if slices.ContainsFunc(crew.ParseNotes(p.lines), func(n string) bool { return factOf(n) == fact }) {
			continue
		}
		switch {
		case w.Replaces != "":
			j := indexOfNote(p.lines, w.Replaces)
			if j < 0 {
				return nil, &NoteRefusedError{i, ErrReplacesMissing}
			}
			p.lines[j] = w.Line
		case sf.full(p.lines):
			return nil, &NoteRefusedError{i, ErrScopeFull}
		default:
			p.lines = appended(p.lines, w.Line)
		}
	}
	return plans, nil
}

// commitPlans writes each changed scope. When a write fails it puts back, as far as it can, the scopes it already wrote.
func commitPlans(plans []*scopePlan) error {
	for i, p := range plans {
		if p.found && slices.Equal(p.lines, p.orig) {
			continue
		}
		if err := p.scope.write(p.lines); err != nil {
			for _, done := range plans[:i] {
				if !done.found {
					_ = done.scope.remove()
				} else if !slices.Equal(done.lines, done.orig) {
					_ = done.scope.write(done.orig)
				}
			}
			return err
		}
	}
	return nil
}

// ApplyNotes writes every note under one hold of the write lock, after checking each against its scope: a note
// with Replaces takes the place of that line, any other is appended unless the scope holds the cap, and a note
// whose fact is already in the scope is skipped. A refusal is a *NoteRefusedError and nothing is written.
func ApplyNotes(home, key string, writes []NoteWrite) error {
	if !safeKey(key) {
		return errors.New("not a fleet key")
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	crewDir, found, err := walk(home, false, "fleets", key, "crew")
	if err != nil {
		return err
	}
	plans, err := planNotes(crewDir, writes)
	if err != nil {
		return err
	}
	if !found && len(plans) > 0 {
		if crewDir, _, err = walk(home, true, "fleets", key, "crew"); err != nil {
			return err
		}
		for _, p := range plans {
			p.scope.crewDir = crewDir
		}
	}
	return commitPlans(plans)
}

// adHocFile reads a member's ad hoc file under crewDir; found is false when there is none.
func adHocFile(crewDir, member string) (raw []byte, found bool, err error) {
	dir, ok, err := walk(crewDir, false, "ad-hoc")
	if err != nil || !ok {
		return nil, false, err
	}
	return readRegular(filepath.Join(dir, member+".md"))
}

// withoutAdHocLines is raw with the frontmatter's ad-hoc and ad-hoc-run lines removed.
func withoutAdHocLines(raw string) string {
	lines := strings.Split(raw, "\n")
	out := lines[:0:0]
	inside := false
	for i, l := range lines {
		if i == 0 && l == "---" {
			inside = true
		} else if inside && l == "---" {
			inside = false
		} else if inside {
			if k, _, _ := strings.Cut(strings.TrimSuffix(l, "\r"), ":"); k == "ad-hoc" || k == "ad-hoc-run" {
				continue
			}
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// KeepAdHoc makes the ad hoc member a member of the fleet: its file without the ad hoc lines, its notes, and the
// ad hoc file gone. It acts only on the file whose text is definition, and refuses with a *MemberRefusedError or a
// *NoteRefusedError, changing nothing, when the file is gone or changed, the fleet or builtIns has a member of that name, the
// result is not a fleet member or a note doesn't fit. removed is false when only the ad hoc file's removal failed.
func KeepAdHoc(home, key, member, definition string, builtIns []string, writes []NoteWrite) (removed bool, err error) {
	if !safeKey(key) || !crew.ValidName(member) {
		return false, errors.New("not a fleet member")
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	crewDir, found, err := walk(home, false, "fleets", key, "crew")
	if err != nil {
		return false, err
	}
	raw, ok, err := adHocFile(crewDir, member)
	if err != nil {
		return false, err
	}
	if !found || !ok || string(raw) != definition {
		return false, &MemberRefusedError{"the ad hoc file is gone or is not the one you were shown"}
	}
	if slices.Contains(builtIns, member) {
		return false, &MemberRefusedError{member + " is a built-in member; an ad hoc member never replaces one"}
	}
	exists, err := lstatFile(filepath.Join(crewDir, member+".md"))
	if err != nil {
		return false, err
	}
	if exists {
		return false, &MemberRefusedError{"this fleet already has a crew member " + member}
	}
	data := []byte(withoutAdHocLines(definition))
	if _, ferr := crew.CheckFile(data, member); ferr != nil {
		return false, &MemberRefusedError{"the file can't be kept as a fleet member: " + fieldMessage(ferr.Field, ferr.Message)}
	}
	plans, err := planNotes(crewDir, writes)
	if err != nil {
		return false, err
	}
	if err := writeAtomic(crewDir, member+".md", data); err != nil {
		return false, err
	}
	if err := commitPlans(plans); err != nil {
		_ = os.Remove(filepath.Join(crewDir, member+".md"))
		return false, err
	}
	return removeAdHoc(crewDir, member), nil
}

// removeAdHoc removes the ad hoc file through a root on the crew directory, so a link swapped in since the read can't lead out of it.
func removeAdHoc(crewDir, member string) bool {
	root, err := os.OpenRoot(crewDir)
	if err != nil {
		return false
	}
	defer root.Close()
	return root.Remove("ad-hoc/"+member+".md") == nil
}

// DropAdHoc removes the member's ad hoc file when its text is still definition, and leaves it otherwise.
func DropAdHoc(home, key, member, definition string) (removed bool, err error) {
	if !safeKey(key) || !crew.ValidName(member) {
		return false, errors.New("not a fleet member")
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	crewDir, found, err := walk(home, false, "fleets", key, "crew")
	if err != nil || !found {
		return false, err
	}
	raw, ok, err := adHocFile(crewDir, member)
	if err != nil {
		return false, err
	}
	if !ok || string(raw) != definition {
		return false, nil
	}
	return removeAdHoc(crewDir, member), nil
}

// BuiltInNames lists the built-in members of every dock of the fleet.
func BuiltInNames(fleet config.Fleet) []string {
	var names []string
	for _, dock := range fleet.Harnesses {
		d, ok := skillindex.CommodoreDir(dock.Name, dock.Environ(), "")
		if !ok {
			continue
		}
		c := fleetCrew{builtInDir: filepath.Join(d, "crew")}
		for name := range c.builtIns() {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}
