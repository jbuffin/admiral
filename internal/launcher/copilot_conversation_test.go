package launcher

// Naming a Copilot ship's session at launch so its events.jsonl is known.

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

func TestCopilotConversationAnchor(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"COPILOT_HOME": home}
	now := time.Unix(1700000000, 0)
	for _, c := range []struct {
		name     string
		dock     []string
		extra    []string
		anchored bool
	}{
		{"nothing chosen", nil, nil, true},
		{"-c is Claude's flag, not Copilot's", nil, []string{"-c"}, true},
		{"--resume after -- is a prompt", nil, []string{"--", "--resume"}, true},
		{"--session-id in the dock's args", []string{"--session-id", "x"}, nil, false},
		{"--resume with a value", nil, []string{"--resume", "abc1234"}, false},
		{"--resume=x", nil, []string{"--resume=x"}, false},
		{"bare --resume", nil, []string{"--resume"}, false},
		{"-r", nil, []string{"-r", "x"}, false},
		{"--continue", nil, []string{"--continue"}, false},
		{"--connect", []string{"--connect"}, nil, false},
	} {
		flags, pane := conversationAnchor(Request{Harness: "copilot", Dir: "/work/a", ExtraArgs: c.extra}, config.Harness{Args: c.dock, Env: env}, now)
		if !c.anchored {
			if flags != nil || pane != nil {
				t.Errorf("%s: flags %q and pane options %q, want neither", c.name, flags, pane)
			}
			continue
		}
		if len(flags) != 2 || flags[0] != "--session-id" || !uuidShape.MatchString(flags[1]) {
			t.Errorf("%s: flags = %q, want --session-id and a version 4 uuid", c.name, flags)
			continue
		}
		want := [2]string{"transcript", "1700000000000000000 launch " + home + "/session-state/" + flags[1] + "/events.jsonl"}
		if len(pane) != 1 || pane[0] != want {
			t.Errorf("%s: pane options = %q, want %q", c.name, pane, want)
		}
	}
}
