package judge_test

import (
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/judge"
)

func TestReplyStructuredSaysTheReplyCameFromClaudesStructuredOutput(t *testing.T) {
	r := newRig(t, "claude", full...)
	r.reply(claudeStructured(validFields, 0.01))
	if rep := r.mustRun(judge.Lookout, "p", []byte(schema), 0.1); !rep.Structured {
		t.Error("Structured = false for a reply that came from structured_output")
	}
	r.reply(claudeResult(validFields, 0.01))
	if rep := r.mustRun(judge.Lookout, "p", []byte(schema), 0.1); rep.Structured || rep.Fields["severity"] != "low" {
		t.Errorf("reply = %+v, want fields parsed from result text with Structured false", rep)
	}
}

func TestReplyIsNeverStructuredOnCodexOrCopilot(t *testing.T) {
	for _, name := range []string{"codex", "copilot"} {
		r := newRig(t, name, full...)
		r.reply(validFields)
		if rep := r.mustRun(judge.Lookout, "p", []byte(schema), 0.1); rep.Structured {
			t.Errorf("%s: Structured = true, but only claude reports structured_output", name)
		}
	}
}

func TestModelsExposeEachHarnessesFixedPair(t *testing.T) {
	for name, want := range map[string][2]string{
		"claude":  {"haiku", "sonnet"},
		"codex":   {"gpt-6-luna", "gpt-6.1-sol"},
		"copilot": {"gpt-5.6-luna", "gpt-5.6-sol"},
	} {
		small, strong, ok := judge.Models(name)
		if !ok || small != want[0] || strong != want[1] {
			t.Errorf("Models(%s) = %q, %q, %v; want %q, %q", name, small, strong, ok, want[0], want[1])
		}
	}
	if _, _, ok := judge.Models("emacs"); ok {
		t.Error("Models(emacs) reported a pair for a harness with no judge calls")
	}
}

func TestForBuildsAChoiceForOneNamedHarnessWithItsMechanismsOn(t *testing.T) {
	for name, want := range map[string]struct{ schema, cap bool }{
		"claude":  {true, true},
		"codex":   {true, false},
		"copilot": {false, false},
	} {
		r := newRig(t, name) // nothing proven: For is what a probe runs before anything is
		c, err := judge.For(r.fleet, name)
		if err != nil {
			t.Fatalf("For(%s): %v", name, err)
		}
		exe, _ := r.fleet.Harnesses[0].Resolve()
		if c.Name != name || c.Exe != exe || c.JSONSchema != want.schema || c.CallCap != want.cap {
			t.Errorf("For(%s) = %+v, want its executable, env and JSONSchema %v, CallCap %v", name, c, want.schema, want.cap)
		}
	}
}

func TestForFailsForAHarnessTheFleetLacksOrCannotFind(t *testing.T) {
	r := newRig(t, "claude")
	if _, err := judge.For(r.fleet, "codex"); err == nil {
		t.Error("For returned a Choice for a harness the fleet has no dock for")
	}
	gone := config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: "claude", Executable: "/nonexistent/claude"}}}
	if _, err := judge.For(gone, "claude"); err == nil {
		t.Error("For returned a Choice for an executable that was not found")
	}
}
