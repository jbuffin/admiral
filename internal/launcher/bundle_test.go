package launcher

import (
	"encoding/json"
	"strings"
	"testing"
)

// The bundle registers the subagent events for each harness, each running
// `admiral hook <event>` (ADR 0014).
func TestBundleRegistersSubagentEvents(t *testing.T) {
	asJSON := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	claude, copilot, codex := asJSON(claudeSettings()), asJSON(copilotHooks()), strings.Join(codexFlags(), " ")
	for _, c := range []struct{ harness, bundle, event string }{
		{"claude", claude, "SubagentStart"}, {"claude", claude, "SubagentStop"},
		{"copilot", copilot, "subagentStart"}, {"copilot", copilot, "subagentStop"},
		{"codex", codex, "SubagentStart"}, {"codex", codex, "SubagentStop"},
	} {
		if want := `"` + c.event + `"`; c.harness != "codex" && !strings.Contains(c.bundle, want+":") {
			t.Errorf("%s bundle has no %s hook", c.harness, c.event)
		}
		if want := "admiral hook " + c.event; !strings.Contains(c.bundle, want) {
			t.Errorf("%s bundle never runs %q", c.harness, want)
		}
		if c.harness == "codex" && !strings.Contains(c.bundle, "hooks."+c.event+"=") {
			t.Errorf("codex flags have no hooks.%s override", c.event)
		}
	}
}
