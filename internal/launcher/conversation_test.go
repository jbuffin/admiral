package launcher

import (
	"regexp"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestConversationAnchor(t *testing.T) {
	cfgDir := t.TempDir()
	env := map[string]string{"CLAUDE_CONFIG_DIR": cfgDir}
	now := time.Unix(1700000000, 0)
	for _, c := range []struct {
		name     string
		harness  string
		dock     []string
		extra    []string
		anchored bool
	}{
		{"claude with nothing chosen", "claude", nil, nil, true},
		{"--resume in the dock's args", "claude", []string{"--resume", "x"}, nil, false},
		{"-c in an order's extra args", "claude", nil, []string{"-c"}, false},
		{"--session-id=x", "claude", nil, []string{"--session-id=x"}, false},
		{"--resume after -- is a prompt", "claude", nil, []string{"--", "--resume"}, true},
		{"codex", "codex", nil, nil, false},
	} {
		flags, pane := conversationAnchor(Request{Harness: c.harness, Dir: "/work/a", ExtraArgs: c.extra}, config.Harness{Args: c.dock, Env: env}, now)
		if !c.anchored {
			if flags != nil || pane != nil {
				t.Errorf("%s: flags %q and pane options %q, want neither", c.name, flags, pane)
			}
			continue
		}
		if len(flags) != 2 || flags[0] != "--session-id" || !uuidShape.MatchString(flags[1]) {
			t.Fatalf("%s: flags = %q, want --session-id and a version 4 uuid", c.name, flags)
		}
		want := [][2]string{{"transcript", "1700000000000000000 launch " + cfgDir + "/projects/-work-a/" + flags[1] + ".jsonl"}}
		if len(pane) != 1 || pane[0] != want[0] {
			t.Errorf("%s: pane options = %q, want %q", c.name, pane, want)
		}
	}
}
