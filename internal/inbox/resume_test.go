package inbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/state"
)

type addFailure struct{ kind AddKind }

func (e addFailure) Error() string    { return "add failed" }
func (e addFailure) AddKind() AddKind { return e.kind }

// resumeFixture is a home with one ship whose report lists q-1, answered.
type resumeFixture struct {
	t     *testing.T
	s     *Service
	calls int
	err   error
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
		ResumeTarget: func() *config.ResumeSource { return &config.ResumeSource{Source: "orders", Panel: "orders"} },
		AddOrder: func(context.Context, string, string, string) (string, error) {
			f.calls++
			return "o-1", f.err
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

// What a failed add does: a final refusal is dropped, a failure that changed
// nothing is tried again, and one that may have landed is never sent twice.
func TestAddResumesByHowTheAddFailed(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantPending int
		wantLeft    bool
	}{
		{"it worked", nil, 0, false},
		{"the source refused the text", errors.New("An order cannot be empty"), 0, false},
		{"the source is not running yet", addFailure{AddRetryable}, 1, true},
		{"the plugin may have added it", addFailure{AddUncertain}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newResumeFixture(t)
			f.ship("Finish https://github.com/acme/widgets/pull/1", "x")
			f.s.CaptureResume("s1")
			f.err = c.err
			if left := f.s.addResumes(""); left != c.wantLeft {
				t.Errorf("left = %v, want %v", left, c.wantLeft)
			}
			if got := f.pending(); got != c.wantPending {
				t.Errorf("pending = %d, want %d", got, c.wantPending)
			}
			before := f.calls
			f.s.addResumes("")
			if again := f.calls - before; (c.wantPending == 0) != (again == 0) {
				t.Errorf("a second pass sent %d more, with %d pending", again, c.wantPending)
			}
		})
	}
}

// One order per resume text and set of items: a rewritten report adds none, and a
// session name drawn again (its report gone in between) adds its own.
func TestResumeOnceAndReuse(t *testing.T) {
	f := newResumeFixture(t)
	f.ship("Finish it", "first")
	f.s.captureAll()
	f.s.addResumes("")
	f.write("reports/s1.json", `{"resume":"Finish it","why":"a rewrite","inbox":["q-1"]}`)
	f.s.captureAll()
	f.s.addResumes("")
	if f.calls != 1 {
		t.Fatalf("%d orders added for a rewritten report, want 1", f.calls)
	}

	for _, p := range []string{"reports/s1.json", "inbox/s1"} {
		if err := os.RemoveAll(filepath.Join(f.s.Home, p)); err != nil {
			t.Fatal(err)
		}
	}
	f.s.captureAll() // the report is gone: the done marker goes
	f.ship("Finish it", "reused name")
	f.s.captureAll()
	f.s.addResumes("")
	if f.calls != 2 {
		t.Errorf("%d orders after the name was drawn again, want 2", f.calls)
	}
}

// Only the answered ship's resume order is tried on its answer.
func TestAddResumesOnlyForTheSession(t *testing.T) {
	f := newResumeFixture(t)
	f.ship("Finish it", "x")
	f.s.CaptureResume("s1")
	f.s.addResumes("other")
	if f.calls != 0 {
		t.Errorf("%d orders added for another ship's answer, want none", f.calls)
	}
	f.s.addResumes("s1")
	if f.calls != 1 {
		t.Errorf("%d orders added for s1, want 1", f.calls)
	}
}
