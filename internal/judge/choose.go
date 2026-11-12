// Package judge runs one judge call: a single prompt on the fleet's judge harness with
// tools, MCP and network off, answered as JSON that fits a schema.
package judge

import (
	"fmt"
	"strings"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
)

// order is the judge harness preference; the fleet's own harness order is the dock's.
var order = []string{"claude", "codex", "copilot"}

// Choice is the fleet's judge harness.
type Choice struct {
	Name       string
	Exe        string
	Env        []string
	JSONSchema bool // the harness enforces a schema itself
	CallCap    bool // the harness enforces a per-call USD cap
}

// Models returns a harness's Lookout and strong model; ok is false for a harness with no judge calls.
func Models(name string) (lookout, strong string, ok bool) {
	return config.ModelPair(name)
}

// For builds the Choice for one named harness with every mechanism it has switched on,
// whether or not it is proven yet: a probe runs the call a real judge call would.
func For(fleet config.Fleet, name string) (Choice, error) {
	h, ok := fleet.Harness(name)
	if !ok {
		return Choice{}, fmt.Errorf("the fleet has no %s harness", name)
	}
	exe, found := h.Resolve()
	if !found {
		return Choice{}, fmt.Errorf("%s was not found", name)
	}
	return Choice{Name: name, Exe: exe, Env: h.Environ(),
		JSONSchema: name == "claude" || name == "codex", CallCap: name == "claude"}, nil
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
