package inbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/state"
)

// resumeFixture is a home with one ship whose report lists q-1, answered.
type resumeFixture struct {
	t      *testing.T
	s      *Service
	afloat bool
	typed  []string
}

func newResumeFixture(t *testing.T) *resumeFixture {
	t.Helper()
	home := t.TempDir()
	st, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	f := &resumeFixture{t: t}
	f.s = &Service{Home: home, Kept: st,
		Afloat: func(string) bool { return f.afloat },
		TypeResume: func(_, text string) (bool, bool, error) {
			if !f.afloat {
				return false, false, nil
			}
			f.typed = append(f.typed, text)
			return true, true, nil
		}}
	return f
}

func (f *resumeFixture) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.s.Home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// ship writes an answered q-1 and a report with the given resume and a note.
func (f *resumeFixture) ship(resume, note string) {
	f.write("inbox/s1/q-1.json", "{}")
	f.write("inbox/s1/q-1.answer.json", "{}")
	f.write("reports/s1.json", `{"resume":"`+resume+`","why":"`+note+`","inbox":["q-1"]}`)
}

func (f *resumeFixture) pending() int { return len(f.s.Kept.PendingResumes()) }

// A ship afloat has its text typed once; one that is gone, or text that can't
// be typed, finishes the entry with nothing typed and nothing added.
func TestResumeTextIsTypedOnceOrFinished(t *testing.T) {
	cases := []struct {
		name      string
		afloat    bool
		resume    string
		wantTyped int
	}{
		{"afloat", true, "Finish it", 1},
		{"the ship is gone", false, "Finish it", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newResumeFixture(t)
			f.afloat = c.afloat
			f.ship(c.resume, "x")
			f.s.CaptureResume("s1")
			f.s.addResumes("")
			f.s.addResumes("")
			if len(f.typed) != c.wantTyped {
				t.Errorf("typed %d times, want %d", len(f.typed), c.wantTyped)
			}
			if got := f.pending(); got != 0 {
				t.Errorf("pending = %d, want 0", got)
			}
		})
	}
}

// A pending text for a ship that is gone, or text with a control character,
// is finished on the first pass and never typed.
func TestPendingResumeOfAGoneShipOrUntypableTextFinishes(t *testing.T) {
	for _, c := range []struct {
		name, text string
		afloat     bool
	}{{"gone", "Finish it", false}, {"untypable", "Finish\x1b it", true}} {
		t.Run(c.name, func(t *testing.T) {
			f := newResumeFixture(t)
			f.afloat = c.afloat
			if _, err := f.s.Kept.QueueResume("s1:k", "s1", c.text); err != nil {
				t.Fatal(err)
			}
			f.s.addResumes("")
			if f.pending() != 0 || len(f.typed) != 0 {
				t.Errorf("pending %d typed %v, want finished and nothing typed", f.pending(), f.typed)
			}
			if fresh, _ := f.s.Kept.QueueResume("s1:k", "s1", c.text); fresh {
				t.Error("the finished entry was queued again")
			}
		})
	}
}

// Before the host's first snapshot no ship is found: the typer says so by
// reporting the ship afloat but not typed, and the entry stays pending.
func TestPendingResumeWaitsWhileShipsAreNotKnown(t *testing.T) {
	f := newResumeFixture(t)
	f.s.TypeResume = func(string, string) (bool, bool, error) { return true, false, nil }
	if _, err := f.s.Kept.QueueResume("s1:k", "s1", "Finish it"); err != nil {
		t.Fatal(err)
	}
	if left := f.s.addResumes(""); !left {
		t.Error("addResumes reported nothing left to try again")
	}
	if f.pending() != 1 {
		t.Errorf("pending = %d, want the entry kept", f.pending())
	}
}
