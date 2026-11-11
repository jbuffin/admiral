package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestFleetSave_WakeReviewersShowInTheConfigViewAndBadLoginsAreRefused(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"wake_reviewers": []string{"Alice", "ci-bot[bot]"}})
	if code != http.StatusOK {
		t.Fatalf("PUT wake_reviewers = %d %s, want 200", code, body)
	}
	_, view := f.do(http.MethodGet, "/api/config", nil)
	var v struct {
		Fleets []struct {
			Key           string   `json:"key"`
			WakeReviewers []string `json:"wake_reviewers"`
		} `json:"fleets"`
	}
	if err := json.Unmarshal([]byte(view), &v); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, fl := range v.Fleets {
		got[fl.Key] = strings.Join(fl.WakeReviewers, ",")
	}
	if got["work"] != "Alice,ci-bot[bot]" || got["home"] != "" {
		t.Errorf("wake_reviewers by fleet = %v, want work Alice,ci-bot[bot] and home none", got)
	}

	before := f.file()
	code, body = f.do(http.MethodPut, "/api/fleets/work", map[string]any{"wake_reviewers": []string{"alice", "not a login"}})
	if code != http.StatusBadRequest || !strings.Contains(body, "wake_reviewers[1]") {
		t.Errorf("PUT with a bad login = %d %s, want 400 naming wake_reviewers[1]", code, body)
	}
	if f.file() != before {
		t.Error("a refused save changed config.json")
	}
}
