package harnesscaps_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/harnesscaps"
	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/probes"
)

// The client's view of the responses.
type capJSON struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Status    string  `json:"status"`
	CheckedAt *string `json:"checked_at"`
	Detail    string  `json:"detail"`
}
type featureJSON struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	Capabilities []capJSON `json:"capabilities"`
}
type harnessJSON struct {
	Harness    string        `json:"harness"`
	Version    string        `json:"version"`
	DetectedAt *string       `json:"detected_at"`
	Features   []featureJSON `json:"features"`
}
type fleetJSON struct {
	Key       string        `json:"key"`
	Name      string        `json:"name"`
	Harnesses []harnessJSON `json:"harnesses"`
}

func (h harnessJSON) status(t *testing.T, id string) capJSON {
	t.Helper()
	for _, f := range h.Features {
		for _, c := range f.Capabilities {
			if c.ID == id {
				return c
			}
		}
	}
	t.Fatalf("%s has no capability %s: %+v", h.Harness, id, h.Features)
	return capJSON{}
}

// script is a harness executable whose --version prints version, or fails when it is "FAIL".
func script(t *testing.T, version string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "harness")
	body := "#!/bin/sh\n"
	if version == "FAIL" {
		body += "echo boom >&2; exit 1\n"
	} else {
		body += "echo '" + version + "'\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func service(t *testing.T, home string, fleets ...config.Fleet) *harnesscaps.Service {
	t.Helper()
	return &harnesscaps.Service{Home: home, Runner: probes.New(home),
		Config: func() *config.Config { return &config.Config{Fleets: fleets} }}
}

func fleet(key, name string, harnesses ...config.Harness) config.Fleet {
	return config.Fleet{Key: key, Name: name, Harnesses: harnesses}
}

func dock(name, exe string) config.Harness {
	return config.Harness{Name: name, Executable: exe, Found: true}
}

func do(t *testing.T, s *harnesscaps.Service, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestListShowsPassedFailedAndNotCheckedPerFleetAndHarness(t *testing.T) {
	home := t.TempDir()
	store := capabilities.Open(home)
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	for _, fh := range [][2]string{{"work", "claude"}, {"home", "claude"}} {
		if err := store.RecordVersion(fh[0], fh[1], "2.1.300", at); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Record("work", "claude", "2.1.300", capabilities.AutopilotGuard, capabilities.Result{Passed: true, At: at, Detail: "denied the sentinel"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record("home", "claude", "2.1.300", capabilities.AutopilotGuard, capabilities.Result{Passed: false, At: at, Detail: "the hook never ran"}); err != nil {
		t.Fatal(err)
	}
	s := service(t, home,
		fleet("work", "Work", dock("claude", "x"), dock("codex", "x")),
		fleet("home", "Home", dock("claude", "x")))

	rec := do(t, s, "GET", "/api/harness-capabilities", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d %s, want 200", rec.Code, rec.Body)
	}
	var got struct {
		Fleets []fleetJSON `json:"fleets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, rec.Body)
	}
	if len(got.Fleets) != 2 || got.Fleets[0].Key != "work" || got.Fleets[0].Name != "Work" || got.Fleets[1].Key != "home" {
		t.Fatalf("fleets = %+v, want work then home, each with its name", got.Fleets)
	}
	work := got.Fleets[0]
	if len(work.Harnesses) != 2 || work.Harnesses[0].Harness != "claude" || work.Harnesses[1].Harness != "codex" {
		t.Fatalf("work harnesses = %+v, want claude and codex in the config's order", work.Harnesses)
	}

	claude := work.Harnesses[0]
	if claude.Version != "2.1.300" || claude.DetectedAt == nil {
		t.Errorf("work claude version = %q detected_at = %v, want 2.1.300 and a time", claude.Version, claude.DetectedAt)
	}
	g := claude.status(t, "autopilot.guard_hook")
	if g.Status != "passed" || g.Detail != "denied the sentinel" || g.Label != "Pre-tool guard hook" ||
		g.CheckedAt == nil || !strings.HasPrefix(*g.CheckedAt, "2026-11-11T09:00:00") {
		t.Errorf("work claude guard = %+v, want passed, its detail, label and the time checked", g)
	}
	_, noJudge := judge.Choose(config.Fleet{Key: "work", Harnesses: []config.Harness{dock("claude", "x"), dock("codex", "x")}}, store)
	var none *judge.NoJudgeHarness
	if !errors.As(noJudge, &none) {
		t.Fatalf("Choose = %v, want NoJudgeHarness", noJudge)
	}
	if a := claude.status(t, "autopilot.advisor"); a.Status != "failed" || a.Detail != none.Reason {
		t.Errorf("work claude advisor = %+v, want failed with the reason %q: it follows the fleet's judge harness live", a, none.Reason)
	}
	if f := got.Fleets[1].Harnesses[0].status(t, "autopilot.guard_hook"); f.Status != "failed" || f.Detail != "the hook never ran" {
		t.Errorf("home claude guard = %+v, want failed with its detail", f)
	}
	if c := work.Harnesses[1]; c.status(t, "autopilot.guard_hook").Status != "not_checked" || c.Version != "" {
		t.Errorf("work codex = version %q guard %+v, want never detected and not_checked", c.Version, c.status(t, "autopilot.guard_hook"))
	}

	var ids []string
	for _, f := range claude.Features {
		ids = append(ids, f.ID)
	}
	if strings.Join(ids, ",") != "judge,bot,restricted,autopilot" {
		t.Errorf("features = %v, want the table's groups judge, bot, restricted, autopilot", ids)
	}
	ap := claude.Features[3]
	if ap.Label != "Autopilot" || len(ap.Capabilities) != 3 || ap.Capabilities[0].ID != "autopilot.guard_hook" {
		t.Errorf("autopilot group = %+v, want its three capabilities, guard first", ap)
	}
}

func TestCheckRunsTheProbesAndAnswersInTheHarnessShape(t *testing.T) {
	home := t.TempDir()
	s := service(t, home, fleet("work", "Work", dock("claude", script(t, "2.1.300 (test)"))))
	rec := do(t, s, "POST", "/api/harness-capabilities/check", `{"fleet":"work","harness":"claude"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST check = %d %s, want 200", rec.Code, rec.Body)
	}
	var got struct {
		harnessJSON
		Fleet string `json:"fleet"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, rec.Body)
	}
	if got.Fleet != "work" || got.Harness != "claude" || got.Version != "2.1.300 (test)" || len(got.Features) != 4 {
		t.Errorf("response = %+v, want fleet work, harness claude, the detected version and the four feature groups", got)
	}
	// The harness doesn't run Claude's headless guard probe, so the guard reads as the probe saw it, and the
	// store now holds the detected version.
	if v, _, _ := capabilities.Open(home).Version("work", "claude"); v != "2.1.300 (test)" {
		t.Errorf("stored version = %q, want the check to have recorded 2.1.300 (test)", v)
	}
}

func TestCheckRefusesWhatItCannotRun(t *testing.T) {
	s := service(t, t.TempDir(),
		fleet("work", "Work", dock("claude", script(t, "2.1.300"))),
		fleet("broken", "Broken", dock("claude", script(t, "FAIL"))))
	for _, tc := range []struct {
		name, body string
		lo, hi     int
	}{
		{"an unknown fleet", `{"fleet":"nowhere","harness":"claude"}`, 404, 404},
		{"a harness the fleet has no dock for", `{"fleet":"work","harness":"codex"}`, 404, 404},
		{"a harness Admiral doesn't know", `{"fleet":"work","harness":"emacs"}`, 400, 404},
		{"no fleet named", `{"harness":"claude"}`, 400, 400},
		{"no JSON", `{`, 400, 400},
		{"a harness whose --version fails", `{"fleet":"broken","harness":"claude"}`, 502, 502},
	} {
		rec := do(t, s, "POST", "/api/harness-capabilities/check", tc.body)
		if rec.Code < tc.lo || rec.Code > tc.hi {
			t.Errorf("%s: POST check = %d %s, want %d–%d", tc.name, rec.Code, rec.Body, tc.lo, tc.hi)
		}
		if !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("%s: body %s has no error message", tc.name, rec.Body)
		}
	}
}

func TestCheckFailureBodyHasNoAbsolutePath(t *testing.T) {
	exe := script(t, "FAIL")
	s := service(t, t.TempDir(), fleet("broken", "Broken", dock("claude", exe)))
	rec := do(t, s, "POST", "/api/harness-capabilities/check", `{"fleet":"broken","harness":"claude"}`)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), filepath.Dir(exe)) {
		t.Errorf("POST check = %d %s, want 502 without the executable's directory", rec.Code, rec.Body)
	}
}

type judgeLine struct {
	Harness      json.RawMessage `json:"harness"`
	LookoutModel string          `json:"lookout_model"`
	StrongModel  string          `json:"strong_model"`
	JSONSchema   bool            `json:"json_schema"`
	CallCap      bool            `json:"call_cap"`
	Reason       string          `json:"reason"`
}

func rawJudge(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var got struct {
		Judge map[string]json.RawMessage `json:"judge"`
	}
	if err := json.Unmarshal(body, &got); err != nil || got.Judge == nil {
		t.Fatalf("no judge object (%v):\n%s", err, body)
	}
	return got.Judge
}

func prove(t *testing.T, store *capabilities.Store, fleet, harness, version string, ids ...capabilities.ID) {
	t.Helper()
	if err := store.RecordVersion(fleet, harness, version, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := store.Record(fleet, harness, version, id, capabilities.Result{Passed: true, At: time.Now(), Detail: "fake"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListCarriesEachFleetsJudgeHarnessOrTheReasonThereIsNone(t *testing.T) {
	home := t.TempDir()
	store := capabilities.Open(home)
	basic := []capabilities.ID{capabilities.JudgeNoTools, capabilities.JudgeNoNetwork}
	prove(t, store, "full", "claude", "2.1.300", append(basic, capabilities.JudgeJSONSchema, capabilities.JudgeCallCap)...)
	prove(t, store, "basic", "codex", "0.167.1", basic...)
	prove(t, store, "none", "claude", "2.1.300", capabilities.JudgeNoTools)
	fleets := []config.Fleet{
		fleet("full", "Full", dock("claude", script(t, "2.1.300"))),
		fleet("basic", "Basic", dock("codex", script(t, "0.167.1"))),
		fleet("none", "None", dock("claude", script(t, "2.1.300"))),
	}
	s := service(t, home, fleets...)
	rec := do(t, s, "GET", "/api/harness-capabilities", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Fleets []struct {
			Key   string                     `json:"key"`
			Judge map[string]json.RawMessage `json:"judge"`
		} `json:"fleets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Fleets) != 3 {
		t.Fatalf("fleets = %+v (%v)", got.Fleets, err)
	}
	read := func(m map[string]json.RawMessage) judgeLine {
		b, _ := json.Marshal(m)
		var j judgeLine
		if err := json.Unmarshal(b, &j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	keys := func(m map[string]json.RawMessage) string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}

	full := read(got.Fleets[0].Judge)
	if string(full.Harness) != `"claude"` || full.LookoutModel != "haiku" || full.StrongModel != "sonnet" || !full.JSONSchema || !full.CallCap {
		t.Errorf("full judge = %+v, want claude, haiku / sonnet, json_schema and call_cap true", full)
	}
	if keys(got.Fleets[0].Judge) != "call_cap,harness,json_schema,lookout_model,strong_model" {
		t.Errorf("full judge keys = %s", keys(got.Fleets[0].Judge))
	}
	basicJ := read(got.Fleets[1].Judge)
	if string(basicJ.Harness) != `"codex"` || basicJ.LookoutModel != "gpt-6-luna" || basicJ.StrongModel != "gpt-6.1-sol" || basicJ.JSONSchema || basicJ.CallCap {
		t.Errorf("basic judge = %+v, want codex with its models and both flags false: they are the probed ones", basicJ)
	}

	off := read(got.Fleets[2].Judge)
	_, why := judge.Choose(fleets[2], store)
	var none *judge.NoJudgeHarness
	if !errors.As(why, &none) {
		t.Fatalf("Choose = %v, want NoJudgeHarness", why)
	}
	if string(off.Harness) != "null" || off.Reason != none.Reason || off.Reason == "" {
		t.Errorf("none judge = %+v, want harness null and the reason %q", off, none.Reason)
	}
	if keys(got.Fleets[2].Judge) != "harness,reason" {
		t.Errorf("none judge keys = %s, want harness and reason only", keys(got.Fleets[2].Judge))
	}
}

func TestCheckAnswersWithTheJudgeHarnessItJustProved(t *testing.T) {
	home := t.TempDir()
	proves := func(id capabilities.ID) probes.Probe {
		return probes.Probe{Harness: "claude", Capability: id,
			Run: func(context.Context, probes.Target) probes.Outcome {
				return probes.Outcome{Passed: true, Detail: "fake"}
			}}
	}
	s := service(t, home, fleet("work", "Work", dock("claude", script(t, "2.1.300 (test)"))))
	s.Runner = probes.New(home, proves(capabilities.JudgeNoTools), proves(capabilities.JudgeNoNetwork))

	rec := do(t, s, "POST", "/api/harness-capabilities/check", `{"fleet":"work","harness":"claude"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST check = %d %s", rec.Code, rec.Body)
	}
	j := rawJudge(t, rec.Body.Bytes())
	if string(j["harness"]) != `"claude"` || string(j["lookout_model"]) != `"haiku"` || string(j["json_schema"]) != "false" {
		t.Errorf("check answer judge = %v, want claude chosen now the check proved no tools and no network", j)
	}
}

func TestAdvisorFollowsTheFleetsJudgeHarnessOnEveryHarness(t *testing.T) {
	home := t.TempDir()
	store := capabilities.Open(home)
	prove(t, store, "full", "claude", "2.1.300", capabilities.JudgeNoTools, capabilities.JudgeNoNetwork)
	prove(t, store, "full", "codex", "0.167.1")
	prove(t, store, "none", "claude", "2.1.300", capabilities.JudgeNoTools)
	fleets := []config.Fleet{
		fleet("full", "Full", dock("claude", script(t, "2.1.300")), dock("codex", script(t, "0.167.1"))),
		fleet("none", "None", dock("claude", script(t, "2.1.300"))),
	}
	s := service(t, home, fleets...)
	rec := do(t, s, "GET", "/api/harness-capabilities", "")
	var got struct {
		Fleets []fleetJSON `json:"fleets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Fleets) != 2 {
		t.Fatalf("GET = %d %s (%v)", rec.Code, rec.Body, err)
	}
	for _, h := range got.Fleets[0].Harnesses {
		if a := h.status(t, "autopilot.advisor"); a.Status != "passed" {
			t.Errorf("full %s advisor = %+v, want passed: the fleet's claude is its judge harness", h.Harness, a)
		}
	}
	_, why := judge.Choose(fleets[1], store)
	var none *judge.NoJudgeHarness
	if !errors.As(why, &none) {
		t.Fatalf("Choose = %v, want NoJudgeHarness", why)
	}
	if a := got.Fleets[1].Harnesses[0].status(t, "autopilot.advisor"); a.Status != "failed" || a.Detail != none.Reason {
		t.Errorf("none claude advisor = %+v, want failed with the reason %q", a, none.Reason)
	}
}

func TestCheckAnswersWithTheAdvisorTheJudgeHarnessItJustProvedGives(t *testing.T) {
	home := t.TempDir()
	proves := func(id capabilities.ID) probes.Probe {
		return probes.Probe{Harness: "claude", Capability: id,
			Run: func(context.Context, probes.Target) probes.Outcome {
				return probes.Outcome{Passed: true, Detail: "fake"}
			}}
	}
	s := service(t, home, fleet("work", "Work", dock("claude", script(t, "2.1.300 (test)"))))
	s.Runner = probes.New(home, proves(capabilities.JudgeNoTools), proves(capabilities.JudgeNoNetwork))

	rec := do(t, s, "POST", "/api/harness-capabilities/check", `{"fleet":"work","harness":"claude"}`)
	var got harnessJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("POST check = %d %s (%v)", rec.Code, rec.Body, err)
	}
	if a := got.status(t, "autopilot.advisor"); a.Status != "passed" {
		t.Errorf("check answer advisor = %+v, want passed now the check proved the judge harness", a)
	}

	empty := service(t, t.TempDir(), fleet("work", "Work", dock("claude", script(t, "2.1.300 (test)"))))
	rec = do(t, empty, "POST", "/api/harness-capabilities/check", `{"fleet":"work","harness":"claude"}`)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("POST check = %d %s (%v)", rec.Code, rec.Body, err)
	}
	if a := got.status(t, "autopilot.advisor"); a.Status != "failed" || a.Detail == "" {
		t.Errorf("check answer advisor with no judge proof = %+v, want failed with the reason", a)
	}
}
