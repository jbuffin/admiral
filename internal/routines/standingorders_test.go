package routines_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

type soRig struct {
	t     *testing.T
	home  string
	cfg   *config.Manager
	store *routines.Store
	srv   *httptest.Server

	mu    sync.Mutex
	calls []judge.Call
	reply func(judge.Call) (judge.Reply, error)
}

// newSO serves the routes over a real config with fleets work (judge proven) and play (not proven), both on the fake claude.
func newSO(t *testing.T) *soRig {
	t.Helper()
	fake, err := filepath.Abs(filepath.Join("..", "..", "e2e", "fakeharness", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := json.Marshal(fake)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	cfgJSON := `{"version":1,"fleets":{"work":{"name":"Work","claude":{"executable":` + string(exe) + `}},"play":{"name":"Play","claude":{"executable":` + string(exe) + `}}}}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	caps := capabilities.Open(home)
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	if err := caps.RecordVersion("work", "claude", "2.1.300 (fake)", now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []capabilities.ID{capabilities.JudgeNoTools, capabilities.JudgeNoNetwork, capabilities.JudgeJSONSchema, capabilities.JudgeCallCap} {
		if err := caps.Record("work", "claude", "2.1.300 (fake)", id, capabilities.Result{Passed: true, At: now}); err != nil {
			t.Fatal(err)
		}
	}
	r := &soRig{t: t, home: home, cfg: m}
	r.store = &routines.Store{Home: home, Now: func() time.Time { return now }, ResourceKnown: func(string) bool { return false },
		Fleets: func(k string) (config.Fleet, bool) { return m.Current().Fleet(k) }}
	mux := http.NewServeMux()
	(&routines.Service{Store: r.store, Config: m, Judge: r.judge}).Routes(mux)
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *soRig) judge(_ context.Context, _ judge.Choice, call judge.Call) (judge.Reply, error) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	reply := r.reply
	r.mu.Unlock()
	if reply == nil {
		return judge.Reply{}, errors.New("no reply set")
	}
	return reply(call)
}

func (r *soRig) answers(fields map[string]any) {
	r.reply = func(judge.Call) (judge.Reply, error) { return judge.Reply{Fields: fields}, nil }
}

func (r *soRig) called() []judge.Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]judge.Call(nil), r.calls...)
}

func (r *soRig) do(method, path string, body any) (int, map[string]any) {
	r.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, r.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			r.t.Fatalf("%s %s answered %d with a body that is not a JSON object: %q", method, path, resp.StatusCode, raw)
		}
	}
	return resp.StatusCode, out
}

func (r *soRig) file(fleet string) string {
	return filepath.Join(r.home, "routines", fleet, "standing-orders.json")
}

func (r *soRig) put(fleet string, body any) {
	r.t.Helper()
	if code, out := r.do("PUT", "/api/fleets/"+fleet+"/standing-orders", body); code != 200 {
		r.t.Fatalf("seeding %s's standing orders: %d %v", fleet, code, out)
	}
}

func (r *soRig) setLimits(fleet string, f config.FleetFields) {
	r.t.Helper()
	if err := r.cfg.SetFleetFields(fleet, f); err != nil {
		r.t.Fatal(err)
	}
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func runes(n int, r string) string { return strings.Repeat(r, n) }

func TestStoreReadsMissingStandingOrdersAsEmpty(t *testing.T) {
	r := newSO(t)
	got, err := r.store.StandingOrders("work")
	if err != nil || len(got.Checks) != 0 || got.Guidance != "" {
		t.Errorf("StandingOrders = %+v, %v; want empty and no error", got, err)
	}
}

func TestStoreSetsAndReadsStandingOrdersAndWritesTheFileBesideRoutines(t *testing.T) {
	r := newSO(t)
	checks, guidance := []string{"run make check", "no force pushes"}, "Prefer small PRs."
	if _, err := r.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &checks, Guidance: &guidance}); err != nil {
		t.Fatalf("SetStandingOrders: %v", err)
	}
	got, err := r.store.StandingOrders("work")
	if err != nil || !reflect.DeepEqual(got.Checks, checks) || got.Guidance != guidance {
		t.Errorf("read back %+v, %v; want %v and %q", got, err, checks, guidance)
	}
	raw, err := os.ReadFile(r.file("work"))
	if err != nil {
		t.Fatalf("no standing-orders.json under routines/work: %v", err)
	}
	var onDisk map[string]any
	if json.Unmarshal(raw, &onDisk) != nil || onDisk["guidance"] != guidance || len(onDisk["checks"].([]any)) != 2 {
		t.Errorf("file = %s, want {checks, guidance}", raw)
	}
	if other, _ := r.store.StandingOrders("play"); len(other.Checks) != 0 {
		t.Errorf("play's checks = %v: a save leaked across fleets", other.Checks)
	}
}

func TestStorePatchLeavesAnAbsentFieldAsStored(t *testing.T) {
	r := newSO(t)
	checks, guidance := []string{"one"}, "keep me"
	r.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &checks, Guidance: &guidance})
	next := "changed"
	got, err := r.store.SetStandingOrders("work", routines.StandingOrdersPatch{Guidance: &next})
	if err != nil || !reflect.DeepEqual(got.Checks, checks) || got.Guidance != next {
		t.Errorf("after a guidance-only patch: %+v, %v; want checks kept and guidance %q", got, err, next)
	}
	empty := []string{}
	got, err = r.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &empty})
	if err != nil || len(got.Checks) != 0 || got.Guidance != next {
		t.Errorf("after an empty checks patch: %+v, %v; want no checks and guidance kept", got, err)
	}
}

func TestStoreMovesAnUnreadableFileAsideAndReadsEmpty(t *testing.T) {
	r := newSO(t)
	if err := os.MkdirAll(filepath.Dir(r.file("work")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.file("work"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := r.store.StandingOrders("work")
	if err != nil || len(got.Checks) != 0 || got.Guidance != "" {
		t.Errorf("StandingOrders = %+v, %v; want empty and no error", got, err)
	}
	if _, err := os.Stat(r.file("work") + ".bad"); err != nil {
		t.Errorf("no .bad file beside the unreadable one: %v", err)
	}
}

func TestStoreRefusesBadStandingOrdersAndKeepsWhatWasStored(t *testing.T) {
	twentyOne := make([]string, 21)
	for i := range twentyOne {
		twentyOne[i] = "check " + string(rune('a'+i))
	}
	cases := []struct {
		name   string
		checks []string
		guide  *string
		want   string
	}{
		{"21 checks", twentyOne, nil, "checks"},
		{"a blank check", []string{"fine", "   "}, nil, "checks[1]"},
		{"an empty first check", []string{""}, nil, "checks[0]"},
		{"a check over 300 characters", []string{"a", "b", runes(301, "x")}, nil, "checks[2]: longer than 300 characters"},
		{"a duplicate check", []string{"same", "same"}, nil, "checks[1]"},
		{"guidance over 2000 characters", nil, ptr(runes(2001, "g")), "guidance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newSO(t)
			kept, keptGuide := []string{"kept"}, "kept guidance"
			r.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &kept, Guidance: &keptGuide})
			p := routines.StandingOrdersPatch{Guidance: tc.guide}
			if tc.checks != nil {
				p.Checks = &tc.checks
			}
			_, err := r.store.SetStandingOrders("work", p)
			if !errors.Is(err, routines.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want ErrInvalid naming %q", err, tc.want)
			}
			got, _ := r.store.StandingOrders("work")
			if !reflect.DeepEqual(got.Checks, kept) || got.Guidance != keptGuide {
				t.Errorf("a refused save changed the stored orders to %+v", got)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestStoreAcceptsTheBoundsAndCountsCharactersNotBytes(t *testing.T) {
	r := newSO(t)
	twenty := make([]string, 20)
	for i := range twenty {
		twenty[i] = "check " + string(rune('a'+i))
	}
	twenty[0] = runes(300, "é")
	guide := runes(2000, "é")
	if _, err := r.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &twenty, Guidance: &guide}); err != nil {
		t.Errorf("20 checks, one of 300 characters, and 2000 characters of guidance: %v", err)
	}
	none, noGuide := []string{}, ""
	if _, err := r.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &none, Guidance: &noGuide}); err != nil {
		t.Errorf("empty checks and guidance: %v", err)
	}
}

func TestStoreNamesAnUnknownFleet(t *testing.T) {
	r := newSO(t)
	if _, err := r.store.StandingOrders("nowhere"); !errors.Is(err, routines.ErrUnknownFleet) {
		t.Errorf("read: %v, want ErrUnknownFleet", err)
	}
	if _, err := r.store.SetStandingOrders("nowhere", routines.StandingOrdersPatch{}); !errors.Is(err, routines.ErrUnknownFleet) {
		t.Errorf("set: %v, want ErrUnknownFleet", err)
	}
}

func TestGetStandingOrdersAnswersTheChecksGuidanceAndTheFleetsLimits(t *testing.T) {
	r := newSO(t)
	code, body := r.do("GET", "/api/fleets/work/standing-orders", nil)
	if code != 200 {
		t.Fatalf("GET = %d %v, want 200", code, body)
	}
	if c, ok := body["checks"].([]any); !ok || len(c) != 0 {
		t.Errorf("checks = %v, want an empty list", body["checks"])
	}
	if g, ok := body["guidance"].(string); !ok || g != "" {
		t.Errorf("guidance = %v, want an empty string", body["guidance"])
	}
	if body["preset"] != "work" {
		t.Errorf("preset = %v, want work", body["preset"])
	}
	if l, ok := body["limits"].(map[string]any); !ok || len(l) == 0 {
		t.Errorf("limits = %v, want the fleet's effective Limits", body["limits"])
	}
	if s, ok := body["sentences"].([]any); !ok || len(s) == 0 {
		t.Errorf("sentences = %v, want the generated sentences", body["sentences"])
	}
	if o, ok := body["overridden"].([]any); !ok || len(o) != 0 {
		t.Errorf("overridden = %v, want an empty list for a fleet on its preset", body["overridden"])
	}
}

func TestGetStandingOrdersListsTheLimitsAFleetOverrides(t *testing.T) {
	r := newSO(t)
	r.setLimits("work", config.FleetFields{Limits: map[string]json.RawMessage{"wall_clock_minutes": json.RawMessage(`45`)}})
	_, body := r.do("GET", "/api/fleets/work/standing-orders", nil)
	if o, _ := body["overridden"].([]any); len(o) != 1 || o[0] != "wall_clock_minutes" {
		t.Errorf("overridden = %v, want [wall_clock_minutes]", body["overridden"])
	}
	if l, _ := body["limits"].(map[string]any); l["wall_clock_minutes"] != float64(45) {
		t.Errorf("limits.wall_clock_minutes = %v, want 45", l["wall_clock_minutes"])
	}
}

func TestPutStandingOrdersSavesAndAnswersTheGetShapeLeavingAnAbsentKeyAsStored(t *testing.T) {
	r := newSO(t)
	code, body := r.do("PUT", "/api/fleets/work/standing-orders", `{"checks":["run make check"],"guidance":"Prefer small PRs."}`)
	if code != 200 || !reflect.DeepEqual(strs(body["checks"]), []string{"run make check"}) || body["guidance"] != "Prefer small PRs." || body["preset"] != "work" {
		t.Fatalf("PUT = %d %v, want 200 and the GET shape holding what was saved", code, body)
	}
	code, body = r.do("PUT", "/api/fleets/work/standing-orders", `{"guidance":"Only this changes."}`)
	if code != 200 || !reflect.DeepEqual(strs(body["checks"]), []string{"run make check"}) || body["guidance"] != "Only this changes." {
		t.Errorf("a guidance-only PUT = %d %v, want checks kept", code, body)
	}
	code, body = r.do("PUT", "/api/fleets/work/standing-orders", `{"checks":[]}`)
	if code != 200 || len(body["checks"].([]any)) != 0 || body["guidance"] != "Only this changes." {
		t.Errorf("a checks-only PUT = %d %v, want guidance kept", code, body)
	}
	_, got := r.do("GET", "/api/fleets/work/standing-orders", nil)
	if got["guidance"] != "Only this changes." {
		t.Errorf("GET after the PUTs = %v", got)
	}
}

func TestPutStandingOrdersRefusesBadInputWith400AndTheReason(t *testing.T) {
	r := newSO(t)
	long, _ := json.Marshal(map[string]any{"checks": []string{"a", "b", runes(301, "x")}})
	code, body := r.do("PUT", "/api/fleets/work/standing-orders", string(long))
	if code != 400 || !strings.Contains(body["error"].(string), "checks[2]: longer than 300 characters") {
		t.Errorf("a long check: %d %v, want 400 naming checks[2]", code, body)
	}
	for name, raw := range map[string]string{"not JSON": `{nope`, "a check that is not text": `{"checks":[1]}`, "21 checks": `{"checks":["1","2","3","4","5","6","7","8","9","10","11","12","13","14","15","16","17","18","19","20","21"]}`} {
		if code, body := r.do("PUT", "/api/fleets/work/standing-orders", raw); code != 400 || body["error"] == nil {
			t.Errorf("%s: %d %v, want 400 with an error", name, code, body)
		}
	}
	if _, err := os.Stat(r.file("work")); err == nil {
		t.Error("a refused PUT wrote the file")
	}
}

func TestStandingOrdersRoutesAnswer404ForAnUnknownFleet(t *testing.T) {
	r := newSO(t)
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/fleets/nowhere/standing-orders", ""},
		{"PUT", "/api/fleets/nowhere/standing-orders", `{"guidance":"x"}`},
		{"POST", "/api/fleets/nowhere/standing-orders/copy", `{"to":"play"}`},
		{"POST", "/api/fleets/nowhere/standing-orders/draft", `{"prose":"x"}`},
		{"POST", "/api/fleets/nowhere/routines/draft", `{"prose":"x"}`},
	} {
		if code, body := r.do(c.method, c.path, c.body); code != 404 || body["error"] == nil {
			t.Errorf("%s %s = %d %v, want 404 with an error", c.method, c.path, code, body)
		}
	}
}

func (r *soRig) seedCopy() {
	r.t.Helper()
	r.put("work", map[string]any{"checks": []string{"run make check", "no force pushes"}, "guidance": "Prefer small PRs."})
	r.setLimits("work", config.FleetFields{Preset: ptr("home"), Limits: map[string]json.RawMessage{"wall_clock_minutes": json.RawMessage(`1440`)}})
	r.put("play", map[string]any{"checks": []string{"old check"}, "guidance": "old guidance"})
	r.setLimits("play", config.FleetFields{Limits: map[string]json.RawMessage{
		"quiet_hours": json.RawMessage(`[{"start":"22:00","end":"06:00","weekdays":["fri"]}]`), "rates": json.RawMessage(`{"concurrent":9}`)}})
}

func TestCopyReplacesTheTargetsChecksGuidancePresetAndLimitsAndNamesWhatLoosens(t *testing.T) {
	r := newSO(t)
	r.seedCopy()
	_, source := r.do("GET", "/api/fleets/work/standing-orders", nil)
	code, body := r.do("POST", "/api/fleets/work/standing-orders/copy", `{"to":"play"}`)
	if code != 200 {
		t.Fatalf("copy = %d %v, want 200", code, body)
	}
	_, target := r.do("GET", "/api/fleets/play/standing-orders", nil)
	for _, k := range []string{"checks", "guidance", "preset", "limits", "overridden"} {
		if !reflect.DeepEqual(target[k], source[k]) {
			t.Errorf("target %s = %v, want the source's %v (a stored quiet window or rate on the target must not survive)", k, target[k], source[k])
		}
		if !reflect.DeepEqual(body[k], target[k]) {
			t.Errorf("copy's answer %s = %v, want the target's GET value %v", k, body[k], target[k])
		}
	}
	loosens := strs(body["loosens"])
	for _, want := range []string{"wall_clock_minutes", "quiet_hours"} {
		found := false
		for _, l := range loosens {
			found = found || l == want
		}
		if !found {
			t.Errorf("loosens = %v, want it to name %s", loosens, want)
		}
	}
	_, again := r.do("GET", "/api/fleets/work/standing-orders", nil)
	if !reflect.DeepEqual(again, source) {
		t.Errorf("the source changed by being copied: %v, was %v", again, source)
	}
}

func TestCopyFromAFleetWithNoStandingOrdersEmptiesTheTarget(t *testing.T) {
	r := newSO(t)
	r.put("play", map[string]any{"checks": []string{"old check"}, "guidance": "old guidance"})
	if code, body := r.do("POST", "/api/fleets/work/standing-orders/copy", `{"to":"play"}`); code != 200 {
		t.Fatalf("copy = %d %v", code, body)
	}
	_, target := r.do("GET", "/api/fleets/play/standing-orders", nil)
	if len(target["checks"].([]any)) != 0 || target["guidance"] != "" {
		t.Errorf("target = %v, want the empty orders of the source", target)
	}
}

func TestCopyRefusesSelfMissingAndUnknownTargetsAndChangesNothing(t *testing.T) {
	r := newSO(t)
	r.seedCopy()
	_, before := r.do("GET", "/api/fleets/play/standing-orders", nil)
	for name, c := range map[string]struct {
		body string
		want int
	}{
		"to itself":        {`{"to":"work"}`, 400},
		"no to":            {`{}`, 400},
		"an empty to":      {`{"to":""}`, 400},
		"not JSON":         {`{nope`, 400},
		"an unknown fleet": {`{"to":"nowhere"}`, 404},
	} {
		if code, body := r.do("POST", "/api/fleets/work/standing-orders/copy", c.body); code != c.want || body["error"] == nil {
			t.Errorf("copy %s = %d %v, want %d with an error", name, code, body, c.want)
		}
	}
	if _, after := r.do("GET", "/api/fleets/play/standing-orders", nil); !reflect.DeepEqual(after, before) {
		t.Errorf("a refused copy changed play: %v, was %v", after, before)
	}
}

func TestCopyRefusedOnTheTargetsRepoMembershipLeavesBothFilesUnchanged(t *testing.T) {
	r := newSO(t)
	r.cfg.SetRepoLookup(func(fleet string) []string {
		if fleet == "work" {
			return []string{"acme/widget"}
		}
		return nil
	})
	r.put("work", map[string]any{"checks": []string{"new check"}, "guidance": "new guidance"})
	r.setLimits("work", config.FleetFields{Limits: map[string]json.RawMessage{"repo_allowlist": json.RawMessage(`["acme/widget"]`)}})
	r.put("play", map[string]any{"checks": []string{"old check"}, "guidance": "old guidance"})
	cfgPath := filepath.Join(r.home, config.FileName)
	cfgBefore, _ := os.ReadFile(cfgPath)
	soBefore, _ := os.ReadFile(r.file("play"))
	code, body := r.do("POST", "/api/fleets/work/standing-orders/copy", `{"to":"play"}`)
	if code != 400 || !strings.Contains(body["error"].(string), "repo_allowlist") {
		t.Fatalf("copy = %d %v, want 400 naming repo_allowlist", code, body)
	}
	cfgAfter, _ := os.ReadFile(cfgPath)
	soAfter, _ := os.ReadFile(r.file("play"))
	if !bytes.Equal(cfgBefore, cfgAfter) || !bytes.Equal(soBefore, soAfter) {
		t.Error("a refused copy changed the target's config.json or standing-orders.json")
	}
}

// The reply a drafter takes when the prose holds one Check, a wall-clock limit and Guidance.
const draftProse = "Keep every run under 30 minutes.\nAlways run make check before a PR.\nPrefer small PRs."

func goodDraft() map[string]any {
	return map[string]any{"limits": map[string]any{"wall_clock_minutes": 30}, "checks": []any{"run make check before a PR"}, "guidance": "Prefer small PRs."}
}

type draftOut struct {
	Draft struct {
		Limits   map[string]any `json:"limits"`
		Checks   []string       `json:"checks"`
		Guidance *string        `json:"guidance"`
	} `json:"draft"`
	Sentences []any `json:"sentences"`
	Changes   []struct {
		Field    string `json:"field"`
		Sentence string `json:"sentence"`
	} `json:"changes"`
	Loosens  []string `json:"loosens"`
	Problems []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"problems"`
}

func (r *soRig) draft(prose string) (int, draftOut, map[string]any) {
	r.t.Helper()
	code, raw := r.do("POST", "/api/fleets/work/standing-orders/draft", map[string]any{"prose": prose})
	var out draftOut
	b, _ := json.Marshal(raw)
	json.Unmarshal(b, &out)
	return code, out, raw
}

func (r *soRig) nothingSaved() {
	r.t.Helper()
	if _, err := os.Stat(r.file("work")); err == nil {
		r.t.Error("a draft wrote standing-orders.json")
	}
	_, body := r.do("GET", "/api/fleets/work/standing-orders", nil)
	if o, _ := body["overridden"].([]any); len(o) != 0 {
		r.t.Errorf("a draft saved limits: overridden = %v", o)
	}
}

func hasProblem(o draftOut, field string) bool {
	for _, p := range o.Problems {
		if p.Field == field && p.Message != "" {
			return true
		}
	}
	return false
}

func TestStandingOrderDraftAnswersTheJudgesDraftWithTheSentencesAndChanges(t *testing.T) {
	r := newSO(t)
	r.answers(goodDraft())
	code, got, raw := r.draft(draftProse)
	if code != 200 {
		t.Fatalf("draft = %d %v, want 200", code, raw)
	}
	if got.Draft.Limits["wall_clock_minutes"] != float64(30) || !reflect.DeepEqual(got.Draft.Checks, []string{"run make check before a PR"}) || got.Draft.Guidance == nil || *got.Draft.Guidance != "Prefer small PRs." {
		t.Errorf("draft = %+v, want the judge's limits, check and guidance", got.Draft)
	}
	if got.Sentences == nil || len(got.Sentences) == 0 {
		t.Errorf("sentences = %v, want the sentences of the limits the draft would leave", got.Sentences)
	}
	found := false
	for _, c := range got.Changes {
		found = found || c.Field == "wall_clock_minutes" && c.Sentence != ""
	}
	if !found {
		t.Errorf("changes = %+v, want an entry for wall_clock_minutes with its sentence", got.Changes)
	}
	if got.Loosens == nil || len(got.Loosens) != 0 || got.Problems == nil || len(got.Problems) != 0 {
		t.Errorf("loosens %v, problems %v; want both as empty lists", got.Loosens, got.Problems)
	}
	r.nothingSaved()
}

func TestStandingOrderDraftKeepsAnEmptyLimitsObjectAndMapsEmptyGuidanceToNull(t *testing.T) {
	r := newSO(t)
	r.answers(map[string]any{"limits": map[string]any{}, "checks": []any{}, "guidance": ""})
	_, raw := r.do("POST", "/api/fleets/work/standing-orders/draft", map[string]any{"prose": draftProse})
	d, _ := raw["draft"].(map[string]any)
	if l, ok := d["limits"].(map[string]any); !ok || len(l) != 0 {
		t.Errorf("draft.limits = %v, want {}", d["limits"])
	}
	if g, present := d["guidance"]; !present || g != nil {
		t.Errorf("draft.guidance = %v (present %v), want null for an empty reply", g, present)
	}
	if c, ok := d["checks"].([]any); !ok || len(c) != 0 {
		t.Errorf("draft.checks = %v, want []", d["checks"])
	}
}

func TestStandingOrderDraftNamesALooseningPatchInLoosens(t *testing.T) {
	r := newSO(t)
	fields := goodDraft()
	fields["limits"] = map[string]any{"wall_clock_minutes": 1440}
	r.answers(fields)
	code, got, raw := r.draft(draftProse)
	if code != 200 || !reflect.DeepEqual(got.Loosens, []string{"wall_clock_minutes"}) {
		t.Errorf("draft = %d, loosens %v (%v), want 200 and [wall_clock_minutes]", code, got.Loosens, raw)
	}
	r.nothingSaved()
}

func TestStandingOrderDraftDropsAnInvalidPatchToNullLimitsWithAProblemAndKeepsTheRest(t *testing.T) {
	r := newSO(t)
	fields := goodDraft()
	fields["limits"] = map[string]any{"wall_clock_minutes": 99999}
	r.answers(fields)
	code, got, raw := r.draft(draftProse)
	if code != 200 {
		t.Fatalf("draft = %d %v, want 200 with the problem listed", code, raw)
	}
	if d := raw["draft"].(map[string]any); d["limits"] != nil {
		t.Errorf("draft.limits = %v, want null", d["limits"])
	}
	if !hasProblem(got, "limits") || !strings.Contains(got.Problems[0].Message, "wall_clock_minutes") {
		t.Errorf("problems = %+v, want one on limits naming wall_clock_minutes", got.Problems)
	}
	if !reflect.DeepEqual(got.Draft.Checks, []string{"run make check before a PR"}) {
		t.Errorf("checks = %v, want them kept", got.Draft.Checks)
	}
}

func TestStandingOrderDraftReportsMoreThan20Checks(t *testing.T) {
	r := newSO(t)
	var checks []any
	var prose []string
	for i := 0; i < 21; i++ {
		c := "check number " + string(rune('a'+i))
		checks = append(checks, c)
		prose = append(prose, "Always do "+c+".")
	}
	r.answers(map[string]any{"limits": map[string]any{}, "checks": checks, "guidance": ""})
	_, got, _ := r.draft(strings.Join(prose, "\n"))
	if !hasProblem(got, "checks") || len(got.Draft.Checks) > 20 {
		t.Errorf("problems %+v, %d checks; want a problem on checks and at most 20 kept", got.Problems, len(got.Draft.Checks))
	}
}

func TestStandingOrderDraftDropsACheckTheProseDoesNotHoldAndAcceptsOneAcrossWhitespace(t *testing.T) {
	r := newSO(t)
	r.answers(map[string]any{"limits": map[string]any{}, "checks": []any{"run make check", "never touch prod"}, "guidance": ""})
	_, got, _ := r.draft("Always  run\n   make check before a PR.")
	if !reflect.DeepEqual(got.Draft.Checks, []string{"run make check"}) {
		t.Errorf("checks = %v, want only the one the prose holds (whitespace collapsed)", got.Draft.Checks)
	}
	if !hasProblem(got, "checks") || !strings.Contains(got.Problems[0].Message, "never touch prod") {
		t.Errorf("problems = %+v, want one on checks naming the dropped Check", got.Problems)
	}
}

func TestStandingOrderDraftWithNoProvenJudgeHarnessIs409WithTheReasonAndMakesNoCall(t *testing.T) {
	r := newSO(t)
	r.answers(goodDraft())
	code, body := r.do("POST", "/api/fleets/play/standing-orders/draft", map[string]any{"prose": draftProse})
	if code != 409 || body["error"] == nil || !strings.Contains(body["reason"].(string), "claude") {
		t.Errorf("draft = %d %v, want 409 with an error and a reason naming claude", code, body)
	}
	if n := len(r.called()); n != 0 {
		t.Errorf("%d judge calls, want none", n)
	}
}

func TestStandingOrderDraftFailureIs502NamingTheKind(t *testing.T) {
	for _, tc := range []struct {
		err  error
		kind string
	}{
		{&judge.Failure{Kind: judge.KindTimeout, Detail: "no answer"}, "timeout"},
		{&judge.Failure{Kind: judge.KindBadReply, Detail: "not JSON"}, "bad reply"},
		{errors.New("the seam broke"), "error"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			r := newSO(t)
			r.reply = func(judge.Call) (judge.Reply, error) { return judge.Reply{}, tc.err }
			code, body := r.do("POST", "/api/fleets/work/standing-orders/draft", map[string]any{"prose": draftProse})
			if code != 502 || body["kind"] != tc.kind || body["error"] == nil {
				t.Errorf("draft = %d %v, want 502 with kind %q and an error", code, body, tc.kind)
			}
			r.nothingSaved()
		})
	}
}

func TestStandingOrderDraftRefusesEmptyBlankOrOverlongProseBeforeAnyCall(t *testing.T) {
	r := newSO(t)
	r.answers(goodDraft())
	for name, prose := range map[string]string{"empty": "", "blank": " \n\t ", "4001 characters": runes(4001, "é")} {
		if code, _, raw := r.draft(prose); code != 400 || raw["error"] == nil {
			t.Errorf("%s: %d %v, want 400 with an error", name, code, raw)
		}
	}
	if code, _ := r.do("POST", "/api/fleets/work/standing-orders/draft", map[string]any{}); code != 400 {
		t.Errorf("no prose key: %d, want 400", code)
	}
	if n := len(r.called()); n != 0 {
		t.Errorf("%d judge calls, want none", n)
	}
	r.answers(goodDraft())
	if code, _, _ := r.draft(runes(4000, "é")); code != 200 && code != 502 {
		t.Errorf("4000 characters: %d, want the prose accepted", code)
	}
}

func TestStandingOrderDraftMakesOneStrongCallWithTheBudgetTimeoutSchemaAndAFencedPrompt(t *testing.T) {
	r := newSO(t)
	r.answers(goodDraft())
	if code, _, raw := r.draft(draftProse); code != 200 {
		t.Fatalf("draft = %d %v", code, raw)
	}
	calls := r.called()
	if len(calls) != 1 {
		t.Fatalf("%d judge calls, want 1", len(calls))
	}
	c := calls[0]
	if c.Kind != judge.Strong || c.BudgetUSD != 0.30 || c.Timeout != 2*time.Minute {
		t.Errorf("call = kind %v, budget %v, timeout %v; want Strong, 0.30 and 2m", c.Kind, c.BudgetUSD, c.Timeout)
	}
	var schema struct {
		Type                 string                    `json:"type"`
		Properties           map[string]map[string]any `json:"properties"`
		Required             []string                  `json:"required"`
		AdditionalProperties *bool                     `json:"additionalProperties"`
	}
	if err := json.Unmarshal(c.Schema, &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	if schema.Type != "object" || schema.AdditionalProperties == nil || *schema.AdditionalProperties || len(schema.Properties) != 3 || len(schema.Required) != 3 {
		t.Errorf("schema = %s, want a closed object of limits, checks and guidance, all required", c.Schema)
	}
	if p := schema.Properties["checks"]; p["type"] != "array" || p["items"] == nil {
		t.Errorf("checks schema = %v, want an array of strings", p)
	}
	if p := schema.Properties["guidance"]; p["type"] != "string" || p["maxLength"] != float64(2000) {
		t.Errorf("guidance schema = %v, want a string of at most 2000", p)
	}
	first := strings.SplitN(c.Prompt, "\n", 2)[0]
	if first == "" {
		t.Error("the prompt has no first line for the fake judge to recognise")
	}
	if !strings.Contains(c.Prompt, "\n```\n"+draftProse+"\n```") && !strings.Contains(c.Prompt, "\n````\n"+draftProse+"\n````") {
		t.Errorf("the prose is not in a fenced block:\n%s", c.Prompt)
	}
	for _, phrase := range []string{"verbatim", "data"} {
		if !strings.Contains(c.Prompt, phrase) {
			t.Errorf("the prompt lacks %q: it must call the prose the engineer's data and tell the judge to copy Checks verbatim", phrase)
		}
	}
	for _, s := range standing.Sentences(standing.Limits{}) {
		if !strings.Contains(c.Prompt, s.Field) {
			t.Errorf("the prompt does not name the Limit %q", s.Field)
		}
	}
	for _, key := range []string{"ceilings", "sender_allowlist", "rates", "quiet_hours", "repo_allowlist", "wall_clock_minutes", "spend", "quota_hold_percent", "write_deny", "skip", "1440", "10000"} {
		if !strings.Contains(c.Prompt, key) {
			t.Errorf("the prompt does not name %q (a Limit field or a bound)", key)
		}
	}
}

const routineProse = "Every weekday at 8:30, ask AdmiralBot to sweep the board."

func routineDraftFields() map[string]any {
	return map[string]any{"name": "Nightly sweep", "enabled": true, "when": "30 8 * * 1-5", "before_acting": "ask_first", "notify": true,
		"do": map[string]any{"prompt": "sweep the board"}}
}

func (r *soRig) routineDraft(prose string) (int, map[string]any) {
	r.t.Helper()
	return r.do("POST", "/api/fleets/work/routines/draft", map[string]any{"prose": prose})
}

func TestRoutineDraftAnswersTheCreateBodyWithoutServerFieldsAndNoProblems(t *testing.T) {
	r := newSO(t)
	r.answers(routineDraftFields())
	code, body := r.routineDraft(routineProse)
	if code != 200 {
		t.Fatalf("draft = %d %v, want 200", code, body)
	}
	d, _ := body["draft"].(map[string]any)
	if d["name"] != "Nightly sweep" || d["when"] != "30 8 * * 1-5" || d["before_acting"] != "ask_first" || d["enabled"] != true {
		t.Errorf("draft = %v, want the judge's routine", d)
	}
	if do, _ := d["do"].(map[string]any); do["prompt"] != "sweep the board" || do["launch"] != nil {
		t.Errorf("draft.do = %v, want a prompt and no launch", d["do"])
	}
	for _, k := range []string{"id", "off_by", "created_at", "updated_at"} {
		if _, has := d[k]; has {
			t.Errorf("draft holds %q: a draft is a create body", k)
		}
	}
	if p, ok := body["problems"].([]any); !ok || len(p) != 0 {
		t.Errorf("problems = %v, want an empty list", body["problems"])
	}
	if code, list := r.do("GET", "/api/fleets/work/routines", nil); code != 200 || len(list["routines"].([]any)) != 0 {
		t.Errorf("after a draft the fleet's routines are %v: nothing may be saved", list)
	}
}

func TestRoutineDraftReportsAProblemForARoutineValidateRefuses(t *testing.T) {
	r := newSO(t)
	fields := routineDraftFields()
	fields["when"] = "every morning"
	r.answers(fields)
	code, body := r.routineDraft(routineProse)
	p, _ := body["problems"].([]any)
	if code != 200 || len(p) == 0 || p[0].(map[string]any)["field"] != "when" {
		t.Errorf("draft = %d %v, want 200 with a problem on when", code, body)
	}
}

func TestRoutineDraftRefusals(t *testing.T) {
	r := newSO(t)
	r.answers(routineDraftFields())
	for name, prose := range map[string]string{"empty": "", "4001 characters": runes(4001, "é")} {
		if code, body := r.routineDraft(prose); code != 400 || body["error"] == nil {
			t.Errorf("%s: %d %v, want 400", name, code, body)
		}
	}
	if code, body := r.do("POST", "/api/fleets/play/routines/draft", map[string]any{"prose": routineProse}); code != 409 || !strings.Contains(body["reason"].(string), "claude") {
		t.Errorf("no judge harness: %d %v, want 409 with a reason", code, body)
	}
	r.reply = func(judge.Call) (judge.Reply, error) {
		return judge.Reply{}, &judge.Failure{Kind: judge.KindTimeout, Detail: "no answer"}
	}
	if code, body := r.routineDraft(routineProse); code != 502 || body["kind"] != "timeout" {
		t.Errorf("judge failure: %d %v, want 502 kind timeout", code, body)
	}
}

func TestRoutineDraftMakesOneStrongCallAndItsPromptIsNotTheStandingOrderDrafters(t *testing.T) {
	r := newSO(t)
	r.answers(routineDraftFields())
	r.routineDraft(routineProse)
	r.answers(goodDraft())
	r.draft(draftProse)
	calls := r.called()
	if len(calls) != 2 {
		t.Fatalf("%d judge calls, want 2", len(calls))
	}
	rc := calls[0]
	if rc.Kind != judge.Strong || rc.BudgetUSD != 0.30 || rc.Timeout != 2*time.Minute || len(rc.Schema) == 0 {
		t.Errorf("routine draft call = kind %v, budget %v, timeout %v, schema %d bytes; want Strong, 0.30, 2m and a schema", rc.Kind, rc.BudgetUSD, rc.Timeout, len(rc.Schema))
	}
	if a, b := strings.SplitN(rc.Prompt, "\n", 2)[0], strings.SplitN(calls[1].Prompt, "\n", 2)[0]; a == b {
		t.Errorf("both drafters open with %q: the fake judge tells them apart by the first line", a)
	}
	if !strings.Contains(rc.Prompt, routineProse) || !strings.Contains(rc.Prompt, "data") {
		t.Errorf("the routine prompt does not fence the prose as data:\n%s", rc.Prompt)
	}
}
