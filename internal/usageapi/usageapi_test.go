package usageapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/usage"
	"github.com/jbuffin/admiral/internal/usageapi"
)

// A Wednesday morning in New York, after the clocks went back on 2026-11-01.
var (
	zone = mustZone("America/New_York")
	now  = time.Date(2026, 11, 12, 10, 0, 0, 0, zone)
)

func mustZone(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

type rig struct {
	t     *testing.T
	home  string
	store *usage.Store
	fleet []config.Fleet
	ships []ships.Ship
	mux   *http.ServeMux
}

func newRig(t *testing.T, fleets ...config.Fleet) *rig {
	t.Helper()
	r := &rig{t: t, home: t.TempDir(), store: usage.NewStore(func() time.Time { return now }), fleet: fleets}
	svc := &usageapi.Service{
		Home:   r.home,
		Config: func() *config.Config { return &config.Config{Fleets: r.fleet} },
		Live:   r.store,
		Ships:  func() []ships.Ship { return r.ships },
		Now:    func() time.Time { return now },
		Zone:   zone,
	}
	r.mux = http.NewServeMux()
	svc.Routes(r.mux)
	return r
}

func dock(harness, dir string) config.Harness {
	v := map[string]string{"claude": "CLAUDE_CONFIG_DIR", "codex": "CODEX_HOME", "copilot": "COPILOT_HOME"}[harness]
	return config.Harness{Name: harness, Found: true, Env: map[string]string{v: dir}}
}

func fleetOf(key string, docks ...config.Harness) config.Fleet {
	return config.Fleet{Key: key, Name: "Fleet " + strings.ToUpper(key), Harnesses: docks}
}

// line is one outcome-log line as the writer makes it, around the fields usage.ExitFields gives a usage record.
func line(ts time.Time, ship, event string, fields map[string]any) string {
	m := map[string]any{}
	for k, v := range fields {
		m[k] = v
	}
	m["v"], m["ts"], m["ship"], m["event"] = 1, ts.UTC().Format(time.RFC3339), ship, event
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func usageLine(ts time.Time, ship, fleet, harness string, own *usage.Figures, crew ...usage.Boat) string {
	return line(ts, ship, "usage", usage.ExitFields(fleet, harness, 0, own, crew))
}

func advisedLine(ts time.Time, ship, fleet, harness string, cost float64) string {
	f := map[string]any{"stakes": "low", "harness": harness, "cost": cost}
	if fleet != "" {
		f["fleet"] = fleet
	}
	return line(ts, ship, "advised", f)
}

// seed appends lines to the log file of the UTC month of each line's own ts, the way the writer names files.
func (r *rig) seed(entries ...stamped) {
	r.t.Helper()
	dir := filepath.Join(r.home, "outcomes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		r.t.Fatal(err)
	}
	for _, e := range entries {
		f, err := os.OpenFile(filepath.Join(dir, e.ts.UTC().Format("2006-01")+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			r.t.Fatal(err)
		}
		if _, err := f.WriteString(e.text + "\n"); err != nil {
			r.t.Fatal(err)
		}
		f.Close()
	}
}

type stamped struct {
	ts   time.Time
	text string
}

func at(ts time.Time, text string) stamped { return stamped{ts, text} }

func in(n int64) *usage.Figures { return &usage.Figures{Tokens: usage.Tokens{Input: n}} }

func est(amount float64) *usage.Money {
	return &usage.Money{Amount: amount, Basis: usage.BasisEstimate}
}

func (r *rig) get(path string) (int, map[string]any) {
	r.t.Helper()
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		r.t.Fatalf("GET %s = %d with a body that is not a JSON object: %q", path, rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), r.home) {
		r.t.Errorf("GET %s leaks the home directory: %s", path, rec.Body.String())
	}
	return rec.Code, out
}

func (r *rig) fleetUsage(key string) map[string]any {
	r.t.Helper()
	code, body := r.get("/api/usage?fleet=" + key)
	if code != 200 {
		r.t.Fatalf("GET /api/usage?fleet=%s = %d: %v", key, code, body)
	}
	return body
}

func obj(t *testing.T, v any, what string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s = %v, want an object", what, v)
	}
	return m
}

func list(t *testing.T, v any, what string) []any {
	t.Helper()
	l, ok := v.([]any)
	if !ok {
		t.Fatalf("%s = %v, want a list (never null)", what, v)
	}
	return l
}

func keys(m map[string]any) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// harnessOf finds a harness's entry in one period of a fleet's response.
func harnessOf(t *testing.T, fleet map[string]any, period, harness string) map[string]any {
	t.Helper()
	p := obj(t, obj(t, fleet["periods"], "periods")[period], "period "+period)
	for _, h := range list(t, p["harnesses"], period+".harnesses") {
		if m := obj(t, h, "harness entry"); m["harness"] == harness {
			return m
		}
	}
	t.Fatalf("period %s has no %s entry: %v", period, harness, p["harnesses"])
	return nil
}

func inputOf(t *testing.T, h map[string]any) float64 {
	t.Helper()
	return obj(t, h["tokens"], "tokens")["input"].(float64)
}

func TestPeriodsCountLocalDays(t *testing.T) {
	r := newRig(t, fleetOf("f", dock("claude", t.TempDir())))
	utc := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	rows := []struct {
		ts    string
		input int64
	}{
		{"2026-11-12T04:59:00Z", 1},       // 23:59 yesterday: in 7 and 30 days, not today
		{"2026-11-12T05:00:00Z", 10},      // local midnight: today
		{"2026-11-06T04:59:00Z", 100},     // 23:59 on the eighth day back: out of 7 days, in 30
		{"2026-11-06T05:00:00Z", 1000},    // 00:00 six days back: the first moment of 7 days
		{"2026-10-13T06:00:00Z", 10000},   // 02:00 on Oct 13 EDT: a 30 * 24 h window would take it
		{"2026-10-14T03:59:00Z", 100000},  // 23:59 on Oct 13: out of 30 days
		{"2026-10-14T04:00:00Z", 1000000}, // 00:00 on Oct 14: the first moment of 30 days
	}
	for _, row := range rows {
		ts := utc(row.ts)
		r.seed(at(ts, usageLine(ts, "s", "f", "claude", in(row.input))))
	}
	f := r.fleetUsage("f")
	for period, want := range map[string]float64{"today": 10, "7d": 1011, "30d": 1001111} {
		if got := inputOf(t, harnessOf(t, f, period, "claude")); got != want {
			t.Errorf("%s input = %v, want %v", period, got, want)
		}
	}
}

func TestMonthToDateIsTheLocalMonth(t *testing.T) {
	r := newRig(t, fleetOf("f", dock("copilot", t.TempDir())))
	credits := func(a float64) *usage.Figures {
		return &usage.Figures{Money: &usage.Money{Amount: a, Basis: usage.BasisCredits}}
	}
	for _, row := range []struct {
		ts time.Time
		a  float64
	}{
		{time.Date(2026, 10, 31, 23, 59, 0, 0, zone), 1},
		{time.Date(2026, 11, 1, 0, 0, 0, 0, zone), 2},
		{time.Date(2026, 11, 9, 12, 0, 0, 0, zone), 4},
	} {
		r.seed(at(row.ts, usageLine(row.ts, "s", "f", "copilot", credits(row.a))))
	}
	got := obj(t, harnessOf(t, r.fleetUsage("f"), "month", "copilot")["money"], "money")
	if got["amount"] != 6.0 || got["basis"] != "credits" {
		t.Errorf("month money = %v, want 6 credits (November's records only)", got)
	}
}

func TestMoneyStaysPerHarnessAndNothingIsSummedAcross(t *testing.T) {
	r := newRig(t, fleetOf("f"))
	ts := now.Add(-time.Hour)
	three := 3.0
	r.seed(
		at(ts, usageLine(ts, "a", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 100}, Money: est(0.25)})),
		at(ts, usageLine(ts, "b", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 50}, Money: est(0.5)})),
		at(ts, usageLine(ts, "c", "f", "copilot", &usage.Figures{Tokens: usage.Tokens{Input: 10},
			Money: &usage.Money{Amount: 2.5, Basis: usage.BasisCredits, PremiumRequestCost: &three}})),
		at(ts, usageLine(ts, "d", "f", "codex", &usage.Figures{Tokens: usage.Tokens{Input: 7}})),
	)
	f := r.fleetUsage("f")
	if got, want := keys(f), []string{"key", "name", "periods", "quota"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fleet keys = %v, want %v: no total of any kind", got, want)
	}
	p := obj(t, obj(t, f["periods"], "periods")["today"], "today")
	if got, want := keys(p), []string{"from", "harnesses"}; !reflect.DeepEqual(got, want) {
		t.Errorf("period keys = %v, want %v", got, want)
	}
	if from, err := time.Parse(time.RFC3339, p["from"].(string)); err != nil || !from.Equal(time.Date(2026, 11, 12, 0, 0, 0, 0, zone)) {
		t.Errorf("today from = %v, want local midnight 2026-11-12T00:00-05:00", p["from"])
	}
	if got := len(list(t, p["harnesses"], "harnesses")); got != 3 {
		t.Errorf("harness entries = %d, want 3 (claude, codex, copilot)", got)
	}
	claude := harnessOf(t, f, "today", "claude")
	if m := obj(t, claude["money"], "claude money"); m["amount"] != 0.75 || m["basis"] != "estimate" || m["incomplete"] != false {
		t.Errorf("claude money = %v, want 0.75 estimate, complete", m)
	}
	if got := inputOf(t, claude); got != 150 {
		t.Errorf("claude input = %v, want 150", got)
	}
	cp := obj(t, harnessOf(t, f, "today", "copilot")["money"], "copilot money")
	if cp["amount"] != 2.5 || cp["basis"] != "credits" || cp["premium_requests"] != 3.0 {
		t.Errorf("copilot money = %v, want 2.5 credits and 3 premium requests", cp)
	}
	if m := harnessOf(t, f, "today", "codex")["money"]; m != nil {
		t.Errorf("codex money = %v, want null", m)
	}
}

func TestRecordWithoutMoneyMakesItsHarnessSumIncomplete(t *testing.T) {
	r := newRig(t, fleetOf("f"))
	ts := now.Add(-time.Hour)
	r.seed(
		at(ts, usageLine(ts, "a", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 1}, Money: est(0.25)})),
		at(ts, usageLine(ts, "b", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 1}})),
	)
	m := obj(t, harnessOf(t, r.fleetUsage("f"), "today", "claude")["money"], "money")
	if m["amount"] != 0.25 || m["incomplete"] != true {
		t.Errorf("money = %v, want 0.25 and incomplete: true", m)
	}
}

func TestCrewIsCountedOnceThroughTheShipsTotal(t *testing.T) {
	r := newRig(t, fleetOf("f"))
	ts := now.Add(-time.Hour)
	boat := usage.Boat{Name: "scout", Harness: "claude", Figures: &usage.Figures{Tokens: usage.Tokens{Input: 30}, Money: est(0.1)}}
	other := usage.Boat{Name: "pilot", Harness: "copilot", Figures: &usage.Figures{Tokens: usage.Tokens{Input: 5},
		Money: &usage.Money{Amount: 9, Basis: usage.BasisCredits}}}
	r.seed(at(ts, usageLine(ts, "a", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 100}, Money: est(0.4)}, boat, other)))
	claude := harnessOf(t, r.fleetUsage("f"), "today", "claude")
	if got := inputOf(t, claude); got != 135 {
		t.Errorf("claude input = %v, want 135 (own 100, scout 30, pilot 5, each once)", got)
	}
	if m := obj(t, claude["money"], "money"); m["amount"] != 0.5 || m["basis"] != "estimate" {
		t.Errorf("claude money = %v, want 0.5 estimate: the copilot boat's credits are not dollars", m)
	}
	today := obj(t, obj(t, r.fleetUsage("f")["periods"], "periods")["today"], "today")
	if n := len(list(t, today["harnesses"], "harnesses")); n != 1 {
		t.Errorf("harness entries = %d, want 1: a crew boat on another harness makes none of its own", n)
	}
}

func TestSpendersAreShipsAdmiralBotAndWorkers(t *testing.T) {
	r := newRig(t, fleetOf("f"))
	ts := now.Add(-time.Hour)
	r.seed(
		at(ts, usageLine(ts, "a", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 100}, Money: est(1)})),
		at(ts, usageLine(ts, "_chat", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 20}, Money: est(0.2)})),
		at(ts, advisedLine(ts, "a", "f", "claude", 0.05)),
		at(ts, advisedLine(ts, "a", "f", "claude", 0.07)),
		at(ts, line(ts, "a", "advised", map[string]any{"fleet": "f", "harness": "claude", "stakes": "high", "failure": "timeout"})),
		at(ts, advisedLine(ts, "a", "", "claude", 5)),
		at(ts, advisedLine(ts, "a", "g", "claude", 7)),
	)
	claude := harnessOf(t, r.fleetUsage("f"), "today", "claude")
	sp := obj(t, claude["spenders"], "spenders")
	if got, want := keys(sp), []string{"chat", "ships", "workers"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("spender keys = %v, want %v", got, want)
	}
	if got := obj(t, obj(t, sp["ships"], "ships")["tokens"], "tokens")["input"]; got != 100.0 {
		t.Errorf("ships input = %v, want 100", got)
	}
	if got := obj(t, obj(t, sp["chat"], "chat")["tokens"], "tokens")["input"]; got != 20.0 {
		t.Errorf("AdmiralBot input = %v, want 20", got)
	}
	w := obj(t, sp["workers"], "workers")
	if m := obj(t, w["money"], "workers money"); m["amount"] != 0.12 || m["basis"] != "estimate" {
		t.Errorf("workers money = %v, want 0.12 estimate: the advised costs of this fleet only", m)
	}
	if w["tokens"] != nil {
		t.Errorf("workers tokens = %v, want null: advised records carry no tokens", w["tokens"])
	}
	if m := obj(t, claude["money"], "money"); m["amount"] != 1.32 {
		t.Errorf("claude money = %v, want 1.32 (ships 1 + AdmiralBot 0.2 + workers 0.12)", m)
	}
}

func TestAdvisedRecordWithoutAFleetCountsInNoFleet(t *testing.T) {
	r := newRig(t, fleetOf("f"), fleetOf("g"))
	ts := now.Add(-time.Hour)
	r.seed(at(ts, advisedLine(ts, "a", "", "claude", 5)))
	code, body := r.get("/api/usage")
	if code != 200 {
		t.Fatalf("GET /api/usage = %d: %v", code, body)
	}
	for _, f := range list(t, body["fleets"], "fleets") {
		if hs := list(t, obj(t, obj(t, obj(t, f, "fleet")["periods"], "periods")["30d"], "30d")["harnesses"], "harnesses"); len(hs) != 0 {
			t.Errorf("fleet %v shows %v for an advised record that names no fleet", obj(t, f, "fleet")["key"], hs)
		}
	}
}

func TestLinesItDoesNotNeedAreSkipped(t *testing.T) {
	r := newRig(t, fleetOf("f"))
	ts := now.Add(-time.Hour)
	big := line(ts, "a", "ended", map[string]any{"report": strings.Repeat("x", 1<<20)})
	r.seed(
		at(ts, big),
		at(ts, `{"v":1,"ts":"`+ts.UTC().Format(time.RFC3339)+`","ship":"a","event":"usa`), // a crash cut it short
		at(ts, usageLine(ts, "a", "f", "claude", in(8))),
	)
	if got := inputOf(t, harnessOf(t, r.fleetUsage("f"), "today", "claude")); got != 8 {
		t.Errorf("input = %v, want 8", got)
	}
}

func TestRunningShipsLiveFiguresAddToTheRecords(t *testing.T) {
	r := newRig(t, fleetOf("f"), fleetOf("g"))
	ts := now.Add(-time.Hour)
	r.seed(at(ts, usageLine(ts, "old", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 100}, Money: est(0.5)})))
	r.ships = []ships.Ship{
		{Session: "live", Fleet: "f", Harness: "claude", Status: "underway"},
		{Session: "elsewhere", Fleet: "g", Harness: "claude", Status: "underway"},
	}
	r.store.SetFigures("live", "s1", usage.Figures{Tokens: usage.Tokens{Input: 40}, Money: est(0.25)}, now.Add(-5*time.Second))
	r.store.SetFigures("elsewhere", "s2", usage.Figures{Tokens: usage.Tokens{Input: 1000}}, now.Add(-5*time.Second))
	f := r.fleetUsage("f")
	for _, period := range []string{"today", "7d", "30d"} {
		claude := harnessOf(t, f, period, "claude")
		if got := inputOf(t, claude); got != 140 {
			t.Errorf("%s input = %v, want 140 (record 100 + live 40, none of fleet g's)", period, got)
		}
		if m := obj(t, claude["money"], "money"); m["amount"] != 0.75 {
			t.Errorf("%s money = %v, want 0.75", period, m)
		}
	}
}

func TestQuotaIsOneEntryPerLoginOfTheFleetsDocks(t *testing.T) {
	claudeDir, codexDir, copilotDir := t.TempDir(), t.TempDir(), t.TempDir()
	r := newRig(t, fleetOf("f", dock("claude", claudeDir), dock("codex", codexDir), dock("copilot", copilotDir)),
		fleetOf("g", dock("claude", t.TempDir())))
	reset := now.Add(2 * time.Hour)
	r.store.SetQuota(config.IdentityDir("claude", map[string]string{"CLAUDE_CONFIG_DIR": claudeDir}), usage.Quota{
		Primary:   &usage.Window{UsedPercent: 40, WindowMinutes: 300, ResetsAt: reset.Unix()},
		Secondary: &usage.Window{UsedPercent: 10, WindowMinutes: 10080, ResetsAt: reset.Add(24 * time.Hour).Unix()},
		Spend:     &usage.SpendLimit{UsedPercent: 30, UsedUSD: 6, LimitUSD: 20, ResetsAt: reset.Add(48 * time.Hour).Unix(), Period: "monthly"},
	}, now.Add(-90*time.Second))
	f := r.fleetUsage("f")
	for _, path := range []string{"/api/usage", "/api/usage?fleet=f"} {
		rec := httptest.NewRecorder()
		r.mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		for _, secret := range []string{claudeDir, codexDir, copilotDir, config.IdentityDir("claude", map[string]string{"CLAUDE_CONFIG_DIR": claudeDir}), os.Getenv("HOME")} {
			if secret != "" && strings.Contains(rec.Body.String(), secret) {
				t.Errorf("%s leaks %q", path, secret)
			}
		}
	}
	q := list(t, f["quota"], "quota")
	if len(q) != 2 {
		t.Fatalf("quota = %v, want the claude and codex docks only: copilot has no quota, and fleet g's login is not f's", q)
	}
	claude, codex := obj(t, q[0], "claude quota"), obj(t, q[1], "codex quota")
	if claude["harness"] != "claude" || codex["harness"] != "codex" {
		t.Fatalf("quota harnesses = %v, %v, want claude then codex, in dock order", claude["harness"], codex["harness"])
	}
	for _, e := range []map[string]any{claude, codex} {
		if l, _ := e["label"].(string); !strings.Contains(l, e["harness"].(string)) {
			t.Errorf("label %q should name the harness", e["label"])
		}
	}
	if claude["has_data"] != true || claude["age_seconds"] != 90.0 {
		t.Errorf("claude has_data/age_seconds = %v/%v, want true/90 with no ship running", claude["has_data"], claude["age_seconds"])
	}
	wins := map[string]map[string]any{}
	for _, w := range list(t, claude["windows"], "windows") {
		m := obj(t, w, "window")
		wins[m["id"].(string)] = m
	}
	if p := wins["primary"]; p == nil || p["used_percent"] != 40.0 || p["window_minutes"] != 300.0 || p["resets_at"] != reset.UTC().Format(time.RFC3339) {
		t.Errorf("primary window = %v, want 40%% of 300 min resetting at %s", p, reset.UTC().Format(time.RFC3339))
	}
	if s := wins["secondary"]; s == nil || s["used_percent"] != 10.0 {
		t.Errorf("secondary window = %v, want 10%%", s)
	}
	if s := wins["spend"]; s == nil || s["used_percent"] != 30.0 {
		t.Errorf("spend window = %v, want the Claude spend limit at 30%%", s)
	}
	if codex["has_data"] != false || len(list(t, codex["windows"], "windows")) != 0 {
		t.Errorf("codex quota = %v, want no live data yet and no windows", codex)
	}
	if _, has := codex["age_seconds"]; has && codex["age_seconds"] != nil {
		t.Errorf("codex age_seconds = %v, want none without a reading", codex["age_seconds"])
	}
}

func TestQuotaWindowPastItsResetReadsUnused(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, fleetOf("f", dock("codex", dir)))
	past := now.Add(-10 * time.Minute)
	r.store.SetQuota(config.IdentityDir("codex", map[string]string{"CODEX_HOME": dir}), usage.Quota{
		Primary: &usage.Window{UsedPercent: 95, WindowMinutes: 300, ResetsAt: past.Unix()},
	}, now.Add(-time.Hour))
	q := obj(t, list(t, r.fleetUsage("f")["quota"], "quota")[0], "quota")
	w := obj(t, list(t, q["windows"], "windows")[0], "window")
	if w["used_percent"] != 0.0 || w["resets_at"] != past.UTC().Format(time.RFC3339) || q["age_seconds"] != 3600.0 {
		t.Errorf("window = %v age %v, want 0%% used, its old reset time and the reading's age of 3600 s", w, q["age_seconds"])
	}
}

func TestEmptyFleetHasEveryPeriodAndNoQuotaEntries(t *testing.T) {
	r := newRig(t, fleetOf("f"))
	f := r.fleetUsage("f")
	if f["key"] != "f" || f["name"] != "Fleet F" {
		t.Errorf("key/name = %v/%v", f["key"], f["name"])
	}
	periods := obj(t, f["periods"], "periods")
	if got, want := keys(periods), []string{"30d", "7d", "month", "today"}; !reflect.DeepEqual(got, want) {
		t.Errorf("period keys = %v, want %v", got, want)
	}
	for name, p := range periods {
		if hs := list(t, obj(t, p, name)["harnesses"], name+".harnesses"); len(hs) != 0 {
			t.Errorf("%s harnesses = %v, want []", name, hs)
		}
	}
	list(t, f["quota"], "quota")
}

func TestUnknownFleetIsNotFound(t *testing.T) {
	r := newRig(t, fleetOf("f"))
	code, body := r.get("/api/usage?fleet=nope")
	if code != 404 || body["error"] == nil {
		t.Errorf("GET /api/usage?fleet=nope = %d %v, want 404 with an error", code, body)
	}
}

func TestAllFleetsAreListedWithNoGrandTotal(t *testing.T) {
	r := newRig(t, fleetOf("f"), fleetOf("g"))
	ts := now.Add(-time.Hour)
	r.seed(
		at(ts, usageLine(ts, "a", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 1}, Money: est(1)})),
		at(ts, usageLine(ts, "b", "g", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 20}, Money: est(2)})),
	)
	code, body := r.get("/api/usage")
	if code != 200 {
		t.Fatalf("GET /api/usage = %d: %v", code, body)
	}
	if got, want := keys(body), []string{"fleets"}; !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %v, want %v: no grand total across fleets", got, want)
	}
	fleets := list(t, body["fleets"], "fleets")
	if len(fleets) != 2 {
		t.Fatalf("fleets = %v, want one entry per fleet", fleets)
	}
	for i, want := range []float64{1, 20} {
		f := obj(t, fleets[i], "fleet")
		if got := inputOf(t, harnessOf(t, f, "today", "claude")); got != want {
			t.Errorf("fleet %v input = %v, want %v", f["key"], got, want)
		}
	}
}

func TestShipUsage(t *testing.T) {
	r := newRig(t, fleetOf("f"))
	earlier, later := now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	r.seed(
		at(earlier, usageLine(earlier, "done", "f", "claude", in(5))),
		at(later, usageLine(later, "done", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 9}, Money: est(0.5)})),
		at(later, usageLine(later, "other", "f", "claude", in(77))),
		at(earlier, usageLine(earlier, "live", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 3}, Money: est(0.05)})),
	)
	r.ships = []ships.Ship{
		{Session: "done", Fleet: "f", Harness: "claude", Status: "ended"},
		{Session: "live", Fleet: "f", Harness: "claude", Status: "underway"},
		{Session: "quiet", Fleet: "f", Harness: "codex", Status: "underway"},
	}
	r.store.SetFigures("live", "s1", usage.Figures{Tokens: usage.Tokens{Input: 40, Output: 2}, Money: est(0.25)}, now.Add(-7*time.Second))

	code, rec := r.get("/api/ships/done/usage")
	if code != 200 || rec["source"] != "record" || rec["harness"] != "claude" || rec["at"] != later.UTC().Format(time.RFC3339) {
		t.Errorf("ended ship = %d %v, want its records, the latest written at %s", code, rec, later.UTC().Format(time.RFC3339))
	}
	if got := obj(t, rec["tokens"], "tokens")["input"]; got != 14.0 {
		t.Errorf("ended ship input = %v, want 14: both rounds' records added", got)
	}
	if m := obj(t, rec["money"], "money"); m["amount"] != 0.5 || m["incomplete"] != true {
		t.Errorf("ended ship money = %v, want 0.5 and incomplete: the first round's record has none", m)
	}

	code, live := r.get("/api/ships/live/usage")
	if code != 200 || live["source"] != "live" || live["age_seconds"] != 7.0 {
		t.Errorf("running ship = %d %v, want live figures 7 s old", code, live)
	}
	if got := obj(t, live["tokens"], "tokens"); got["input"] != 43.0 || got["output"] != 2.0 {
		t.Errorf("running ship tokens = %v, want 43 in (an earlier round's 3 plus live 40) and 2 out", got)
	}
	if m := obj(t, live["money"], "money"); m["amount"] != 0.3 || m["basis"] != "estimate" || m["incomplete"] != false {
		t.Errorf("running ship money = %v, want 0.3 estimate, complete", m)
	}

	code, none := r.get("/api/ships/quiet/usage")
	if code != 200 || none["source"] != "none" || none["tokens"] != nil || none["money"] != nil {
		t.Errorf("a ship with neither = %d %v, want source none and null figures", code, none)
	}

	if code, _ := r.get("/api/ships/ghost/usage"); code != 404 {
		t.Errorf("unknown ship = %d, want 404", code)
	}
}

func TestShipUsageCountsOnlyThisShipsLifetime(t *testing.T) {
	r := newRig(t, fleetOf("f"))
	t0 := now.Add(-72 * time.Hour)
	t1, t2, t3 := now.Add(-48*time.Hour), now.Add(-24*time.Hour), now.Add(-2*time.Hour)
	r.seed(
		at(t0, line(t0, "saltpaw", "launched", map[string]any{"fleet": "f"})),
		at(t0, usageLine(t0, "saltpaw", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 1000}, Money: est(9)})),
		at(t1, line(t1, "saltpaw", "ended", map[string]any{})),
		at(t1, line(t1, "saltpaw", "launched", map[string]any{"fleet": "f"})),
		at(t1, usageLine(t1, "saltpaw", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 5}, Money: est(0.5)})),
		at(t2, line(t2, "saltpaw", "woken", map[string]any{"round": 1})),
		at(t3, usageLine(t3, "saltpaw", "f", "claude", &usage.Figures{Tokens: usage.Tokens{Input: 7}, Money: est(0.25)})),
	)
	r.ships = []ships.Ship{{Session: "saltpaw", Fleet: "f", Harness: "claude", Status: "underway"}}
	_, rec := r.get("/api/ships/saltpaw/usage")
	if got := obj(t, rec["tokens"], "tokens")["input"]; got != 12.0 {
		t.Errorf("input = %v, want 12: this ship's two rounds, not the earlier ship of the same name", got)
	}
	if m := obj(t, rec["money"], "money"); m["amount"] != 0.75 {
		t.Errorf("money = %v, want 0.75", m)
	}
}

func TestCopilotPremiumRequestsSumTheValuesThatArePresent(t *testing.T) {
	r := newRig(t, fleetOf("f"))
	ts := now.Add(-time.Hour)
	two, one := 2.0, 1.0
	credits := func(amount float64, p *float64) *usage.Figures {
		return &usage.Figures{Tokens: usage.Tokens{Input: 1}, Money: &usage.Money{Amount: amount, Basis: usage.BasisCredits, PremiumRequestCost: p}}
	}
	r.seed(
		at(ts, usageLine(ts, "a", "f", "copilot", credits(1, &two))),
		at(ts, usageLine(ts, "b", "f", "copilot", credits(1, &one))),
	)
	m := obj(t, harnessOf(t, r.fleetUsage("f"), "today", "copilot")["money"], "money")
	if m["premium_requests"] != 3.0 || m["incomplete"] != false {
		t.Errorf("money = %v, want 3 premium requests, complete", m)
	}
	r.seed(at(ts, usageLine(ts, "c", "f", "copilot", credits(1, nil))))
	m = obj(t, harnessOf(t, r.fleetUsage("f"), "today", "copilot")["money"], "money")
	if m["premium_requests"] != 3.0 || m["incomplete"] != true || m["amount"] != 3.0 {
		t.Errorf("money = %v, want the 3 present, amount 3, incomplete", m)
	}
}

func TestSkippedHugeLineIsNotBuffered(t *testing.T) {
	r := newRig(t, fleetOf("f"))
	ts := now.Add(-time.Hour)
	big := line(ts, "a", "ended", map[string]any{"report": strings.Repeat("x", 6<<20)})
	r.seed(at(ts, big), at(ts, usageLine(ts, "a", "f", "claude", in(8))))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f := r.fleetUsage("f")
	runtime.ReadMemStats(&after)
	if got := inputOf(t, harnessOf(t, f, "today", "claude")); got != 8 {
		t.Errorf("input = %v, want 8", got)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 2<<20 {
		t.Errorf("a request over a 6 MiB skipped line allocated %d bytes, want far less than the line", alloc)
	}
}
