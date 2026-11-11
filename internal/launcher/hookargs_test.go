package launcher

import (
	"strings"
	"testing"
)

func TestRefuseHookArgs(t *testing.T) {
	refused := []struct {
		harness string
		args    []string
		name    string // the text the error must carry
	}{
		{"claude", []string{"--settings", "x"}, "--settings"},
		{"claude", []string{"--settings=x"}, "--settings=x"},
		{"claude", []string{"--bare"}, "--bare"},
		{"claude", []string{"--"}, "--"},
		{"codex", []string{"-c", "hooks.Stop=[]"}, "hooks.Stop=[]"},
		{"codex", []string{"-chooks.Stop=[]"}, "-chooks.Stop=[]"},
		{"codex", []string{"--config=features.hooks=false"}, "--config=features.hooks=false"},
		{"codex", []string{"-c", ` "hooks".Stop=[]`}, "hooks"},
		{"codex", []string{"--disable", "hooks"}, "hooks"},
		{"codex", []string{"-p", "x"}, "-p"},
		{"copilot", []string{"--plugin-dir", "/tmp/x"}, "--plugin-dir"},
		{"codex", []string{"-c", "features={hooks=false}"}, "features={hooks=false}"},
		{"codex", []string{"-c", "features.codex_hooks=false"}, "features.codex_hooks"},
		{"codex", []string{"-c", "profile=x"}, "profile=x"},
		{"codex", []string{"--config=profiles.x.model=y"}, "profiles.x"},
		{"codex", []string{"-c", " hooks.Stop=[]"}, "hooks.Stop"},
		{"codex", []string{"-c", "\u2003hooks.Stop=[]"}, "hooks.Stop"},
		{"claude", []string{"--setting-sources", "project"}, "project"},
		{"claude", []string{"--model", "--setting-sources", "project"}, "project"},
		{"codex", []string{"cloud-tasks"}, "cloud-tasks"},
		{"claude", []string{"remote-control"}, "remote-control"},
		{"claude", []string{"--model", "a\nb"}, `a\nb`},
		{"claude", []string{"--model", "opus", "doctor"}, "doctor"},
		{"codex", []string{"e"}, "e"},
		{"copilot", []string{"login"}, "login"},
		{"codex", []string{"--remote", "ws://x"}, "--remote"},
		{"copilot", []string{"--connect"}, "--connect"},
		{"copilot", []string{"--acp"}, "--acp"},
	}
	for _, tc := range refused {
		err := RefuseHookArgs(tc.harness, tc.args)
		if err == nil {
			t.Errorf("%s %q: not refused", tc.harness, tc.args)
		} else if !strings.Contains(err.Error(), tc.name) {
			t.Errorf("%s %q: error %q does not name %q", tc.harness, tc.args, err, tc.name)
		}
	}

	passes := []struct {
		harness string
		args    []string
	}{
		{"codex", []string{"-c", "model_reasoning_effort=high"}},
		{"claude", []string{"--model", "opus"}},
		{"claude", []string{"--setting-sources=project"}},
		{"claude", []string{"--disallowedTools", "Edit(/x/**)"}},
		{"copilot", []string{"--add-dir", "/x"}},
		// The same flag name on another harness is that harness's own business.
		{"copilot", []string{"--settings", "x"}},
		{"codex", []string{"--settings", "x"}},
		{"claude", []string{"--plugin-dir", "x"}},
		{"claude", []string{"-p", "x"}},
		{"claude", []string{"--restricted"}},
		{"claude", []string{"--model", "mcpx"}},
		{"codex", []string{"-c", "features.other=true"}},
		{"claude", nil},
	}
	for _, tc := range passes {
		if err := RefuseHookArgs(tc.harness, tc.args); err != nil {
			t.Errorf("%s %q: refused: %v", tc.harness, tc.args, err)
		}
	}
}
