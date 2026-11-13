package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The fake judge answers a standing-order draft only once its cue exists, so this stays red until then.
func TestStandingOrders_DraftThroughTheRealServerWithTheFakeJudgeSavesNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard("work")
	w.start()

	prose := "Keep every run under 30 minutes.\nAlways run make check before a PR.\nPrefer small PRs."
	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/fleets/work/standing-orders/draft", map[string]any{"prose": prose})
	if code != http.StatusOK {
		t.Fatalf("draft = %d %s, want 200 from the fake judge", code, body)
	}
	var got struct {
		Draft    map[string]any `json:"draft"`
		Problems []any          `json:"problems"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Draft == nil || got.Problems == nil {
		t.Fatalf("draft body = %s (%v), want a draft object and a problems list", body, err)
	}
	if _, ok := got.Draft["checks"].([]any); !ok {
		t.Errorf("draft.checks = %v, want a list", got.Draft["checks"])
	}

	code, body = send(t, http.MethodPost, w.in.LocalURL+"/api/fleets/work/routines/draft", map[string]any{"prose": "Every night, check for outdated dependencies."})
	var routine struct {
		Draft struct {
			Do struct {
				Prompt string `json:"prompt"`
			} `json:"do"`
		} `json:"draft"`
		Problems []any `json:"problems"`
	}
	if err := json.Unmarshal([]byte(body), &routine); err != nil || code != http.StatusOK || routine.Draft.Do.Prompt == "" || len(routine.Problems) != 0 {
		t.Fatalf("routine draft = %d %s (%v), want 200 with a do.prompt and no problems", code, body, err)
	}

	code, body = send(t, http.MethodGet, w.in.LocalURL+"/api/fleets/work/standing-orders", nil)
	var stored struct {
		Checks     []string `json:"checks"`
		Guidance   string   `json:"guidance"`
		Overridden []string `json:"overridden"`
	}
	if err := json.Unmarshal([]byte(body), &stored); err != nil || code != http.StatusOK {
		t.Fatalf("GET = %d %s (%v)", code, body, err)
	}
	if len(stored.Checks) != 0 || stored.Guidance != "" || len(stored.Overridden) != 0 {
		t.Errorf("after a draft the fleet holds %+v: a draft must save nothing", stored)
	}
}
