package launcher

import (
	"strings"
	"testing"
)

// RefuseHookArgs vets the values of `crew launch --arg` (#175): anything that
// overrides or disables Admiral's hooks is refused, and the error names the value.
func TestRefuseHookArgs(t *testing.T) {
	refused := []struct {
		harness string
		args    []string
		name    string // the text the error must carry
	}{
		{"claude", []string{"--settings", "x"}, "--settings"},
		{"claude", []string{"--settings=x"}, "--settings=x"},
		{"claude", []string{"--bare"}, "--bare"},
		{"claude", []string{"--safe-mode"}, "--safe-mode"},
		{"claude", []string{"--"}, "--"},
		{"claude", []string{"--model", "opus", "--settings", "x"}, "--settings"},
		{"codex", []string{"-c", "hooks.Stop=[]"}, "hooks.Stop=[]"},
		{"codex", []string{"-chooks.Stop=[]"}, "-chooks.Stop=[]"},
		{"codex", []string{"-c=hooks.x"}, "-c=hooks.x"},
		{"codex", []string{"--config", "hooks={}"}, "hooks={}"},
		{"codex", []string{"--config=features.hooks=false"}, "--config=features.hooks=false"},
		{"codex", []string{"-c", ` "hooks".Stop=[]`}, "hooks"},
		{"codex", []string{"--disable", "hooks"}, "hooks"},
		{"codex", []string{"--disable=hooks"}, "--disable=hooks"},
		{"codex", []string{"-p", "x"}, "-p"},
		{"codex", []string{"--profile=x"}, "--profile=x"},
		{"codex", []string{"--profile", "x"}, "--profile"},
		{"codex", []string{"--"}, "--"},
		{"copilot", []string{"--plugin-dir", "/tmp/x"}, "--plugin-dir"},
		{"copilot", []string{"--plugin-dir=x"}, "--plugin-dir=x"},
		{"copilot", []string{"--"}, "--"},
		{"codex", []string{"-c", "features={hooks=false}"}, "features={hooks=false}"},
		{"codex", []string{"-c", "features.codex_hooks=false"}, "features.codex_hooks"},
		{"codex", []string{"-c", "profile=x"}, "profile=x"},
		{"codex", []string{"--config=profiles.x.model=y"}, "profiles.x"},
		{"codex", []string{"-c", "profiles={}"}, "profiles={}"},
		{"codex", []string{"--disable", "codex_hooks"}, "codex_hooks"},
		{"codex", []string{"--disable=codex_hooks"}, "--disable=codex_hooks"},
		{"codex", []string{"-c", " hooks.Stop=[]"}, "hooks.Stop"},
		{"codex", []string{"-c", "\u2003hooks.Stop=[]"}, "hooks.Stop"},
		{"codex", []string{"-c", "\vhooks.Stop=[]"}, "hooks.Stop"}, // an ASCII control character
		{"codex", []string{"-c", "\u00a0hooks.Stop=[]"}, "hooks.Stop"},
		{"codex", []string{"-phooks"}, "-phooks"},
		{"claude", []string{"--setting-sources", "project"}, "project"},
		{"claude", []string{"--model", "--setting-sources", "project"}, "project"},
		{"codex", []string{"cloud-tasks"}, "cloud-tasks"},
		{"codex", []string{"tcp-tunnel"}, "tcp-tunnel"},
		{"codex", []string{"stdio-to-uds"}, "stdio-to-uds"},
		{"codex", []string{"responses-api-proxy"}, "responses-api-proxy"},
		{"codex", []string{"execpolicy"}, "execpolicy"},
		{"claude", []string{"remote-control"}, "remote-control"},
		{"claude", []string{"rc"}, "rc"},
		{"claude", []string{"remote"}, "remote"},
		{"claude", []string{"project"}, "project"},
		{"claude", []string{"sandbox"}, "sandbox"},
		{"claude", []string{"design-login"}, "design-login"},
		{"claude", []string{"--model", "a\nb"}, `a\nb`},
		{"claude", []string{"mcp"}, "mcp"},
		{"claude", []string{"--model", "opus", "doctor"}, "doctor"},
		{"codex", []string{"e"}, "e"},
		{"codex", []string{"a"}, "a"},
		{"codex", []string{"exec"}, "exec"},
		{"codex", []string{"help"}, "help"},
		{"copilot", []string{"login"}, "login"},
		{"copilot", []string{"help"}, "help"},
		{"codex", []string{"--remote", "ws://x"}, "--remote"},
		{"codex", []string{"--remote=ws://x"}, "--remote=ws://x"},
		{"copilot", []string{"--connect"}, "--connect"},
		{"copilot", []string{"--connect=abc"}, "--connect=abc"},
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
		{"codex", []string{"-m", "x"}},
		{"codex", []string{"--sandbox", "read-only"}},
		{"claude", []string{"--model", "opus"}},
		{"claude", []string{"--setting-sources=project"}},
		{"claude", []string{"--disallowedTools", "Edit(/x/**)"}},
		{"copilot", []string{"--model", "x"}},
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
