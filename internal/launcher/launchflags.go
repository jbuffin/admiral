package launcher

import (
	"net/http"
	"regexp"
	"slices"

	"github.com/jbuffin/admiral/internal/config"
)

// Efforts each harness offers; copilot has none, so any effort for it is refused.
var efforts = map[string][]string{
	"claude": {"low", "medium", "high", "max"},
	"codex":  {"low", "medium", "high"},
}

// Codex and copilot models change often, so any plain name passes; it is one argv element and never dash-led.
var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// modelFlags is the harness's own flags for the chosen model and effort; empty means its default.
func modelFlags(harness, model, effort string) ([]string, *Error) {
	var out []string
	if model != "" {
		switch {
		case harness == "claude" && !slices.Contains(config.ChatModelsFor("claude"), model):
			return nil, refuse(http.StatusBadRequest, "model %q is not one of %v for claude", model, config.ChatModelsFor("claude"))
		case !modelName.MatchString(model):
			return nil, refuse(http.StatusBadRequest, "model %q is not a plain model name", model)
		}
		flag := "--model"
		if harness == "codex" {
			flag = "-m"
		}
		out = append(out, flag, model)
	}
	if effort != "" {
		if !slices.Contains(efforts[harness], effort) {
			if len(efforts[harness]) == 0 {
				return nil, refuse(http.StatusBadRequest, "%s picks its effort itself, so effort %q is refused", harness, effort)
			}
			return nil, refuse(http.StatusBadRequest, "effort %q is not one of %v for %s", effort, efforts[harness], harness)
		}
		if harness == "codex" {
			out = append(out, "-c", "model_reasoning_effort="+effort)
		} else {
			out = append(out, "--effort", effort)
		}
	}
	return out, nil
}
