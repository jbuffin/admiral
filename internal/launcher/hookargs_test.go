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
		{"claude", []string{"--setting-sources", "project"}},
		{"claude", []string{"--disallowedTools", "Edit(/x/**)"}},
		{"copilot", []string{"--model", "x"}},
		{"copilot", []string{"--add-dir", "/x"}},
		// The same flag name on another harness is that harness's own business.
		{"copilot", []string{"--settings", "x"}},
		{"codex", []string{"--settings", "x"}},
		{"claude", []string{"--plugin-dir", "x"}},
		{"claude", []string{"-p", "x"}},
		{"claude", nil},
	}
	for _, tc := range passes {
		if err := RefuseHookArgs(tc.harness, tc.args); err != nil {
			t.Errorf("%s %q: refused: %v", tc.harness, tc.args, err)
		}
	}
}
