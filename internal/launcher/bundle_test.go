package launcher

import (
	"encoding/json"
	"regexp"
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

// Claude's PreToolUse runs the guard for Bash, GitHub's MCP review tools and
// its merge tools, with a timeout that blocks, beside the working hook.
func TestBundleRunsTheGuard(t *testing.T) {
	raw, _ := json.Marshal(claudeSettings())
	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command   string `json:"command"`
				Timeout   int    `json:"timeout"`
				OnFailure string `json:"onFailure"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	var guarded []*regexp.Regexp
	working := 0
	for _, g := range settings.Hooks["PreToolUse"] {
		for _, h := range g.Hooks {
			switch h.Command {
			case "admiral guard pretooluse":
				guarded = append(guarded, regexp.MustCompile(g.Matcher))
				if h.Timeout != 10 || h.OnFailure != "block" {
					t.Errorf("the guard on %q has timeout %d onFailure %q, want 10 and block: a timed-out hook would let the call through", g.Matcher, h.Timeout, h.OnFailure)
				}
			case "admiral hook PreToolUse":
				working++
			}
		}
	}
	matched := func(tool string) bool {
		for _, re := range guarded {
			if re.MatchString(tool) {
				return true
			}
		}
		return false
	}
	for _, tool := range []string{"Bash", "mcp__github__create_pull_request_review", "mcp__github__merge_pull_request", "mcp__github__enable_pull_request_auto_merge", "mcp__github__enqueue_pull_request"} {
		if !matched(tool) {
			t.Errorf("the guard does not run for %s", tool)
		}
	}
	for _, tool := range []string{"Read", "mcp__github__get_pull_request"} {
		if matched(tool) {
			t.Errorf("the guard runs for %s, which can neither review nor merge", tool)
		}
	}
	if working == 0 {
		t.Error("PreToolUse lost the working hook")
	}
}
