// Package judge runs one judge call: a single prompt on the fleet's judge harness with
// tools, MCP and network off, answered as JSON that fits a schema.
package judge

import (
	"strings"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
)

// order is the judge harness preference; the fleet's own harness order is the dock's.
var order = []string{"claude", "codex", "copilot"}

// models are each harness's small model for the Lookout and the stronger one for every
// other call. Codex's pair comes from `codex debug models`, Copilot's from the names
// `copilot --model` accepted on a real call.
var models = map[string][2]string{
	"claude":  {"haiku", "sonnet"},
	"codex":   {"gpt-6-luna", "gpt-6.1-sol"},
	"copilot": {"gpt-5.6-luna", "gpt-5.6-sol"},
}

// Choice is the fleet's judge harness.
type Choice struct {
	Name       string
	Exe        string
	Env        []string
	JSONSchema bool // the harness enforces a schema itself
	CallCap    bool // the harness enforces a per-call USD cap
}

// NoJudgeHarness is the outcome for a fleet that has no harness proven safe for judge calls.
type NoJudgeHarness struct{ Reason string }

func (e *NoJudgeHarness) Error() string { return "no judge harness: " + e.Reason }

// Choose picks the first of claude, codex and copilot that the fleet has and that has
// proven no tools and no network on its current version.
func Choose(fleet config.Fleet, store *capabilities.Store) (Choice, error) {
	var why []string
	for _, name := range order {
		h, ok := fleet.Harness(name)
		if !ok {
			continue
		}
		exe, found := h.Resolve()
		if !found {
			why = append(why, name+": not found")
			continue
		}
		tools := store.Proven(fleet.Key, name, capabilities.JudgeNoTools)
		network := store.Proven(fleet.Key, name, capabilities.JudgeNoNetwork)
		if !tools || !network {
			missing := "no tools"
			switch {
			case !tools && !network:
				missing = "no tools and no network"
			case !network:
				missing = "no network"
			}
			why = append(why, name+": "+missing+" not proven")
			continue
		}
		return Choice{
			Name: name, Exe: exe, Env: h.Environ(),
			JSONSchema: store.Proven(fleet.Key, name, capabilities.JudgeJSONSchema),
			CallCap:    store.Proven(fleet.Key, name, capabilities.JudgeCallCap),
		}, nil
	}
	if len(why) == 0 {
		return Choice{}, &NoJudgeHarness{Reason: "the fleet has no claude, codex or copilot harness"}
	}
	return Choice{}, &NoJudgeHarness{Reason: strings.Join(why, "; ")}
}
