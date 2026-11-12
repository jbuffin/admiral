package crewapi_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/crewapi"
)

func TestApplyNotesRefusesAnAppendIntoAFullScopeAndWritesNothingForAnyNote(t *testing.T) {
	f := newFixture(t)
	full := f.repoNotes("bosun", "acme/api", lines(30)...)
	before := readFile(t, full)
	err := crewapi.ApplyNotes(f.home, "work", []crewapi.NoteWrite{
		{Member: "bosun", Line: "- " + note(100)}, // fits: the fleet scope is empty
		{Member: "bosun", Repo: "acme/api", Line: "- " + note(101)},
	})
	var refused *crewapi.NoteRefusedError
	if !errors.As(err, &refused) || refused.Index != 1 || !errors.Is(err, crewapi.ErrScopeFull) {
		t.Fatalf("err = %v, want a refusal of write 1 that is ErrScopeFull", err)
	}
	if readFile(t, full) != before {
		t.Error("the full scope changed")
	}
	if _, statErr := os.Stat(filepath.Join(f.memDir(), "bosun", "fleet.md")); !os.IsNotExist(statErr) {
		t.Errorf("the note that fit was written although another did not: %v", statErr)
	}
}

func TestApplyNotesWithReplacesFillsAFullScopeInPlace(t *testing.T) {
	f := newFixture(t)
	path := f.fleetNotes("bosun", lines(30)...)
	err := crewapi.ApplyNotes(f.home, "work", []crewapi.NoteWrite{
		{Member: "bosun", Line: "- 2026-11-11 (run-b): new fact", Replaces: note(4)},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := lines(30)
	want[4] = "- 2026-11-11 (run-b): new fact"
	if got := readFile(t, path); got != strings.Join(want, "\n")+"\n" {
		t.Errorf("file = %q, want the fifth line replaced where it stood and the other 29 untouched", got)
	}
}

func TestApplyNotesRefusesAReplacesThatMatchesNoLineEvenInAScopeWithRoom(t *testing.T) {
	f := newFixture(t)
	path := f.fleetNotes("bosun", lines(2)...)
	before := readFile(t, path)
	err := crewapi.ApplyNotes(f.home, "work", []crewapi.NoteWrite{
		{Member: "bosun", Line: "- 2026-11-11 (run-b): new fact", Replaces: "2026-11-10 (run-a): a fact that is not there"},
	})
	var refused *crewapi.NoteRefusedError
	if !errors.As(err, &refused) || refused.Index != 0 || !errors.Is(err, crewapi.ErrReplacesMissing) {
		t.Fatalf("err = %v, want a refusal of write 0 that is ErrReplacesMissing", err)
	}
	if readFile(t, path) != before {
		t.Error("the file changed")
	}
}

func TestApplyNotesDoesNotWriteAFactThatIsAlreadyInTheScope(t *testing.T) {
	f := newFixture(t)
	path := f.fleetNotes("bosun", "- 2026-09-01 (run-old): the build needs make")
	err := crewapi.ApplyNotes(f.home, "work", []crewapi.NoteWrite{
		{Member: "bosun", Line: "- 2026-11-11 (run-b): the build needs make"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "- 2026-09-01 (run-old): the build needs make\n" {
		t.Errorf("file = %q, want the one line it had", got)
	}
}

func TestApplyNotesPutsBackTheScopesAlreadyWrittenWhenALaterWriteFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	f := newFixture(t)
	existing := f.repoNotes("bosun", "acme/api", lines(2)...)
	before := readFile(t, existing)
	memory := f.memDir()
	if err := os.Chmod(memory, 0o500); err != nil { // a new member's directory can't be made
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(memory, 0o700) })
	err := crewapi.ApplyNotes(f.home, "work", []crewapi.NoteWrite{
		{Member: "bosun", Repo: "acme/api", Line: "- " + note(100)}, // rewrites an existing file
		{Member: "bosun", Line: "- " + note(101)},                   // creates bosun's fleet file
		{Member: "newcomer", Line: "- " + note(102)},                // fails
	})
	if err == nil {
		t.Fatal("ApplyNotes succeeded although a write could not be made")
	}
	if readFile(t, existing) != before {
		t.Error("the rewritten scope was not put back")
	}
	if _, statErr := os.Stat(filepath.Join(memory, "bosun", "fleet.md")); !os.IsNotExist(statErr) {
		t.Errorf("the created scope file was not removed: %v", statErr)
	}
}
