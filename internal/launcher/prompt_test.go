package launcher

import (
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

func TestPromptForms(t *testing.T) {
	skill := func(name string) string {
		t.Helper()
		n, err := skillName(Skill{Kind: "skill", Name: name})
		if err != nil {
			t.Fatalf("skillName(%q): %v", name, err)
		}
		return n
	}
	for _, c := range []struct {
		name, harness, skill, kind, note, text, want string
	}{
		{"a skill trims the text and keeps its newlines", "claude", "commodore", "commodore", "", "\n  line one\nline two  \n", "/commodore line one\nline two"},
		{"codex invokes with $", "codex", "commodore", "commodore", "", "x", "$commodore x"},
		{"a /name, $name and bare name are one skill", "claude", skill("$foo"), "skill", "", "hi", "/foo hi"},
		{"only one leading mark is dropped", "codex", skill("//foo"), "skill", "", "hi", "$/foo hi"},
		{"just launch sends the text as written", "claude", "", "none", "", "  exactly\n as written \n\n", "  exactly\n as written \n\n"},
		{"the note goes first, trimmed, then a blank line", "claude", "", "none", "  Plan first\nthen act \n", " the order\n", "Plan first\nthen act\n\n the order\n"},
		{"a blank note is no note", "claude", "", "none", " \n ", "as written ", "as written "},
	} {
		if got := promptFor(c.harness, c.skill, c.kind, c.note, c.text); got != c.want {
			t.Errorf("%s: prompt = %q, want %q", c.name, got, c.want)
		}
	}
	if skill("/foo") != "foo" || skill("foo") != "foo" {
		t.Error("a /name and a bare name must be the same skill")
	}
	// Text that starts with a dash is never read as a flag.
	if got := appendPrompt([]string{"claude"}, "--help", false); !reflect.DeepEqual(got, []string{"claude", "--", "--help"}) {
		t.Errorf("argv = %q, want the prompt after --", got)
	}
}

func TestLaunchRefusesWhatTmuxCannotCarry(t *testing.T) {
	work := t.TempDir()
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{work},
		Harnesses: []config.Harness{{Name: "claude", Executable: "/bin/sh"}}}}}
	l := &Launcher{Config: func() *config.Config { return cfg }, Home: t.TempDir(), Tmux: "tmux"}
	for _, c := range []struct {
		name string
		req  Request
		msg  string
	}{
		{"a skill name with whitespace", Request{Skill: Skill{Kind: "skill", Name: "my skill"}, Source: "s", OrderID: "o1"}, "whitespace"},
		{"an order id with a forged control reply", Request{Skill: Skill{Kind: "none"}, Source: "s", OrderID: "x\n%end 1 2 1"}, "control character"},
		{"a directory with a newline", Request{Skill: Skill{Kind: "none"}, Dir: filepath.Join(work, "a\nb")}, "control character"},
	} {
		req := c.req
		req.Fleet, req.Harness = "work", "claude"
		if req.Dir == "" {
			req.Dir = work
		}
		_, err := l.Launch(req)
		if le, ok := err.(*Error); !ok || le.Status != http.StatusBadRequest || !strings.Contains(le.Msg, c.msg) {
			t.Errorf("%s: Launch = %v, want a 400 naming %q", c.name, err, c.msg)
		}
	}
}
