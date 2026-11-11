package e2e

import (
	"encoding/json"
	"net/http"
)

// the real binary serves GET /api/resources; the
// launches that name a resource are in launch_autopilot_test.go.

type resourceJSON struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
	InUse    int    `json:"in_use"`
	Holders  []struct {
		Kind    string `json:"kind"`
		Session string `json:"session"`
	} `json:"holders"`
	Check string `json:"check"`
}

func (w *world) resourceList() []resourceJSON {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/resources", nil)
	var out struct {
		Resources []resourceJSON `json:"resources"`
	}
	if err := json.Unmarshal([]byte(body), &out); code != http.StatusOK || err != nil {
		w.t.Fatalf("GET /api/resources = %d %s", code, body)
	}
	return out.Resources
}
