package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type capsHarness struct {
	Harness  string `json:"harness"`
	Version  string `json:"version"`
	Features []struct {
		ID           string `json:"id"`
		Capabilities []struct {
			ID        string  `json:"id"`
			Status    string  `json:"status"`
			CheckedAt *string `json:"checked_at"`
		} `json:"capabilities"`
	} `json:"features"`
}

func (h capsHarness) status(id string) string {
	for _, f := range h.Features {
		for _, c := range f.Capabilities {
			if c.ID == id {
				return c.Status
			}
		}
	}
	return "missing"
}

func (w *world) capsOf(fleet, harness string) capsHarness {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/harness-capabilities", nil)
	if code != http.StatusOK {
		w.t.Fatalf("GET /api/harness-capabilities = %d, want 200: %s", code, body)
	}
	var got struct {
		Fleets []struct {
			Key       string        `json:"key"`
			Harnesses []capsHarness `json:"harnesses"`
		} `json:"fleets"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		w.t.Fatalf("not JSON: %v\n%s", err, body)
	}
	for _, f := range got.Fleets {
		for _, h := range f.Harnesses {
			if f.Key == fleet && h.Harness == harness {
				return h
			}
		}
	}
	w.t.Fatalf("GET /api/harness-capabilities has no %s/%s: %s", fleet, harness, body)
	return capsHarness{}
}

func (w *world) check(fleet, harness string) (int, string) {
	w.t.Helper()
	return send(w.t, http.MethodPost, w.in.LocalURL+"/api/harness-capabilities/check",
		map[string]any{"fleet": fleet, "harness": harness})
}

func TestHarnessCapabilities_ListShowsPassedFailedAndNotChecked(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.seedGuard("work", "claude", true)
	w.seedGuard("home", "claude", false)
	w.start()
	const guard = "autopilot.guard_hook"
	if s := w.capsOf("work", "claude").status(guard); s != "passed" {
		t.Errorf("work claude guard = %q, want passed", s)
	}
	if s := w.capsOf("home", "claude").status(guard); s != "failed" {
		t.Errorf("home claude guard = %q, want failed", s)
	}
	codex := w.capsOf("work", "codex")
	if s := codex.status(guard); s != "not_checked" {
		t.Errorf("work codex guard = %q, want not_checked", s)
	}
	if h := w.capsOf("work", "claude"); h.Version != fakeVersion {
		t.Errorf("work claude version = %q, want %q", h.Version, fakeVersion)
	}
}

func TestHarnessCapabilities_CheckReRunsTheProbeAndTheListReflectsIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.start()
	if s := w.capsOf("work", "claude").status("autopilot.guard_hook"); s != "not_checked" {
		t.Fatalf("before the check the guard = %q, want not_checked", s)
	}
	code, body := w.check("work", "claude")
	if code != http.StatusOK {
		t.Fatalf("POST check = %d, want 200: %s", code, body)
	}
	var got capsHarness
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.status("autopilot.guard_hook") != "passed" || got.Version != fakeVersion {
		t.Errorf("check answered %s, want the guard passed on %s", body, fakeVersion)
	}
	if s := w.capsOf("work", "claude").status("autopilot.guard_hook"); s != "passed" {
		t.Errorf("after the check the list says %q, want passed", s)
	}
	// The probed guard now lets autopilot launch.
	w.writeCue("wait go", "exit 0")
	w.launch(w.autopilotLaunch("claude", "after-check", nil))
}

func TestHarnessCapabilities_CheckRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.start()
	for _, tc := range [][2]string{{"nowhere", "claude"}, {"claudeonly", "codex"}} {
		if code, body := w.check(tc[0], tc[1]); code != http.StatusNotFound {
			t.Errorf("check %v = %d, want 404: %s", tc, code, body)
		}
	}
	if code, _ := send(t, http.MethodPost, w.in.LocalURL+"/api/harness-capabilities/check", map[string]any{}); code != http.StatusBadRequest {
		t.Errorf("check with no fleet = %d, want 400", code)
	}
}

func TestHarnessCapabilities_CheckWhenTheVersionCannotBeRead(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.seedGuard("work", "claude", true)
	vf := filepath.Join(w.root, "version")
	if err := os.WriteFile(vf, []byte("FAIL"), 0o644); err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.config()), &cfg); err != nil {
		t.Fatal(err)
	}
	claude := cfg["fleets"].(map[string]any)["work"].(map[string]any)["claude"].(map[string]any)
	claude["env"].(map[string]any)["FAKE_VERSION_FILE"] = vf
	b, _ := json.Marshal(cfg)
	writeConfig(t, w.home, string(b))
	w.start()

	code, body := w.check("work", "claude")
	if code != http.StatusBadGateway || !json.Valid([]byte(body)) {
		t.Fatalf("check with a failing --version = %d %s, want 502 with a JSON error", code, body)
	}
	h := w.capsOf("work", "claude")
	if h.Version != "" || h.status("autopilot.guard_hook") != "not_checked" {
		t.Errorf("after the failed check version = %q guard = %q, want no version and not_checked", h.Version, h.status("autopilot.guard_hook"))
	}
}
