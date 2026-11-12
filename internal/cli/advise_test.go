package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
)

// adviseHarness stands in for claude on a judge call: it logs its argv and prompt under
// $ADVISE_LOG, then acts out $ADVISE_MODE.
const adviseHarness = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "2.1.300 (fake)"; exit 0; fi
mkdir -p "$ADVISE_LOG"
printf '%s\0' "$@" >"$ADVISE_LOG/argv-$$"
cat >"$ADVISE_LOG/prompt-$$"
case "$ADVISE_MODE" in
hang) sleep 30; exit 0 ;;
error) echo "boom $ADVISE_MARKER" >&2; exit 1 ;;
budget) echo '{"type":"result","subtype":"error_max_budget_usd","is_error":true,"errors":["Reached maximum budget"],"total_cost_usd":0.0123}'; exit 1 ;;
garbage) echo '{"type":"result","subtype":"success","is_error":false,"result":"hello '"$ADVISE_MARKER"'"}'; exit 0 ;;
low) body="{\"stakes\":\"low\",\"reason\":$ADVISE_REASON}" ;;
high) body="{\"stakes\":\"high\",\"reason\":$ADVISE_REASON}" ;;
missing) body="{\"reason\":$ADVISE_REASON}" ;;
badstakes) body="{\"stakes\":\"$ADVISE_MARKER\",\"reason\":\"$ADVISE_MARKER\"}" ;;
esac
cost=""
[ -n "$ADVISE_COST" ] && cost=",\"total_cost_usd\":$ADVISE_COST"
printf '{"type":"result","subtype":"success","is_error":false,"result":"","structured_output":%s%s}\n' "$body" "$cost"
`

const (
	adviseMarker = "decoy-secret-5e1a9c"
	validAdvice  = `{"question":"Rebase onto main?","options":["yes","no"],"recommendation":"yes"}`
)

// adviseRig is a ship in fleet "work" whose claude is the inline fake.
type adviseRig struct {
	home  string
	log   string
	ship  string
	fleet config.Fleet
	// lookups holds the (home, ship) pairs the fleet seam was asked about.
	lookups [][2]string
}

// newAdviseRig seeds the judge probe passes for claude and points the fleet seam at the fake.
// env adds to the fake's environment.
func newAdviseRig(t *testing.T, mode string, passes []capabilities.ID, env map[string]string) *adviseRig {
	t.Helper()
	r := &adviseRig{home: t.TempDir(), log: filepath.Join(t.TempDir(), "log"), ship: "saltpaw-ii"}
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte(adviseHarness), 0o755); err != nil {
		t.Fatal(err)
	}
	e := map[string]string{"ADVISE_LOG": r.log, "ADVISE_MODE": mode, "ADVISE_MARKER": adviseMarker, "ADVISE_REASON": `"stays on the ship's branch"`}
	for k, v := range env {
		e[k] = v
	}
	r.fleet = config.Fleet{Key: "work", Name: "Work", Harnesses: []config.Harness{{Name: "claude", Executable: exe, Found: true, Env: e}}}
	store := capabilities.Open(r.home)
	if err := store.RecordVersion("work", "claude", "2.1.300 (fake)", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, id := range passes {
		if err := store.Record("work", "claude", "2.1.300 (fake)", id, capabilities.Result{Passed: true, At: time.Now(), Detail: "seeded"}); err != nil {
			t.Fatal(err)
		}
	}
	old := shipFleet
	shipFleet = func(home, ship string) (config.Fleet, bool) {
		r.lookups = append(r.lookups, [2]string{home, ship})
		return r.fleet, true
	}
	t.Cleanup(func() { shipFleet = old })
	return r
}

var (
	judgePasses = []capabilities.ID{capabilities.JudgeNoTools, capabilities.JudgeNoNetwork, capabilities.JudgeJSONSchema}
	cappedPass  = append(append([]capabilities.ID{}, judgePasses...), capabilities.JudgeCallCap)
)

// advise runs `admiral advise` as the rig's ship, with HOME in a temp dir.
func (r *adviseRig) advise(t *testing.T, stdin string) (error, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ADMIRAL_SHIP", r.ship)
	t.Setenv("ADMIRAL_REPORT", filepath.Join(r.home, "reports", r.ship+".json"))
	t.Setenv("ADMIRAL_HOME", "")
	var out, errb bytes.Buffer
	err := Advise(nil, IO{In: strings.NewReader(stdin), Out: &out, Err: &errb})
	return err, out.String(), errb.String()
}

func (r *adviseRig) ran() bool {
	files, _ := filepath.Glob(filepath.Join(r.log, "argv-*"))
	return len(files) > 0
}

type advice struct {
	Stakes string          `json:"stakes"`
	Reason string          `json:"reason"`
	Cost   json.RawMessage `json:"cost"`
}

// oneLine parses stdout as exactly one JSON line with the three keys.
func oneLine(t *testing.T, out string) advice {
	t.Helper()
	if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
		t.Fatalf("stdout = %q, want exactly one line", out)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &keys); err != nil {
		t.Fatalf("stdout is not JSON: %q", out)
	}
	if len(keys) != 3 || keys["stakes"] == nil || keys["reason"] == nil || keys["cost"] == nil {
		t.Fatalf("stdout keys = %v, want stakes, reason and cost", keys)
	}
	var a advice
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func (r *adviseRig) outcomes(t *testing.T) []map[string]any {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(r.home, "outcomes", "*.jsonl"))
	var out []map[string]any
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if l == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Fatalf("outcomes line %q: %v", l, err)
			}
			out = append(out, m)
		}
	}
	return out
}

func TestAdviseRefusesBadInputAsAUsageErrorAndMakesNoCall(t *testing.T) {
	big := `{"question":"` + strings.Repeat("x", inbox.MaxItem) + `","options":["a","b"],"recommendation":"a"}`
	cases := []struct{ name, stdin string }{
		{"empty input", ""},
		{"not JSON", "should I merge?"},
		{"no question", `{"options":["yes","no"],"recommendation":"yes"}`},
		{"an empty question", `{"question":"","options":["yes","no"],"recommendation":"yes"}`},
		{"one option", `{"question":"Merge?","options":["yes"],"recommendation":"yes"}`},
		{"a recommendation that is not an option", `{"question":"Merge?","options":["yes","no"],"recommendation":"maybe"}`},
		{"over the size limit", big},
		{"data after the object", validAdvice + ` {"x":1}`},
		{"an unknown field", `{"question":"Merge?","options":["yes","no"],"recommendation":"yes","extra":1}`},
		{"an empty option", `{"question":"Merge?","options":["yes",""],"recommendation":"yes"}`},
		{"a repeated option", `{"question":"Merge?","options":["yes","yes"],"recommendation":"yes"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAdviseRig(t, "low", judgePasses, nil)
			err, out, _ := r.advise(t, c.stdin)
			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("Advise = %v, want a *UsageError (exit 2)", err)
			}
			if out != "" {
				t.Errorf("stdout = %q, want nothing", out)
			}
			if r.ran() {
				t.Error("the judge harness ran on bad input")
			}
		})
	}
}

func TestAdvisePassesAValidLowReplyThroughWithItsCost(t *testing.T) {
	r := newAdviseRig(t, "low", judgePasses, map[string]string{"ADVISE_COST": "0.0123"})
	err, out, _ := r.advise(t, validAdvice)
	if err != nil {
		t.Fatalf("Advise = %v, want exit 0", err)
	}
	a := oneLine(t, out)
	if a.Stakes != "low" || a.Reason != "stays on the ship's branch" || string(a.Cost) != "0.0123" {
		t.Errorf("answer = %+v, want low, the model's reason and cost 0.0123", a)
	}
}

func TestAdvisePassesAValidHighReplyThrough(t *testing.T) {
	r := newAdviseRig(t, "high", judgePasses, map[string]string{"ADVISE_COST": "0.02", "ADVISE_REASON": `"it posts to a shared channel"`})
	err, out, _ := r.advise(t, validAdvice)
	if err != nil {
		t.Fatalf("Advise = %v, want exit 0", err)
	}
	if a := oneLine(t, out); a.Stakes != "high" || a.Reason != "it posts to a shared channel" {
		t.Errorf("answer = %+v, want the model's high answer and reason", a)
	}
}

func TestAdviseAnswersNullCostWhenTheHarnessReportsNone(t *testing.T) {
	r := newAdviseRig(t, "low", judgePasses, nil)
	err, out, _ := r.advise(t, validAdvice)
	if err != nil {
		t.Fatal(err)
	}
	if a := oneLine(t, out); string(a.Cost) != "null" {
		t.Errorf("cost = %s, want null", a.Cost)
	}
}

func TestAdviseStripsControlCharactersFromTheModelsReason(t *testing.T) {
	reason, _ := json.Marshal("ok\x1b[31m\nred\a\u200b\u202e\ufeff" + strings.Repeat("é", 250))
	r := newAdviseRig(t, "low", judgePasses, map[string]string{"ADVISE_REASON": string(reason)})
	err, out, _ := r.advise(t, validAdvice)
	if err != nil {
		t.Fatal(err)
	}
	a := oneLine(t, out)
	for _, c := range a.Reason {
		if c < 0x20 || c == 0x7f || unicode.Is(unicode.Cf, c) {
			t.Errorf("reason %q holds control character %U", a.Reason, c)
		}
	}
	if !strings.HasPrefix(a.Reason, "ok") || len([]rune(a.Reason)) > 300 {
		t.Errorf("reason = %q, want the model's text, at most 300 characters", a.Reason)
	}
}

// Every way a call can fail reads as high-stakes with Admiral's own text, never the harness's.
func TestAdviseAnswersHighWithFixedTextWhenTheCallFails(t *testing.T) {
	cases := []struct {
		name, mode string
		env        map[string]string
	}{
		{"the harness errors", "error", nil},
		{"the harness does not answer in time", "hang", nil},
		{"the reply is not JSON", "garbage", nil},
		{"the call's budget is spent", "budget", nil},
		{"the reply has no stakes", "missing", nil},
		{"the stakes are neither low nor high", "badstakes", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAdviseRig(t, c.mode, judgePasses, c.env)
			if c.mode == "hang" {
				old := adviseTimeout
				adviseTimeout = time.Second
				t.Cleanup(func() { adviseTimeout = old })
			}
			err, out, errb := r.advise(t, validAdvice)
			if err != nil {
				t.Fatalf("Advise = %v, want exit 0", err)
			}
			a := oneLine(t, out)
			if a.Stakes != "high" || a.Reason == "" {
				t.Errorf("answer = %+v, want high with a reason", a)
			}
			if strings.Contains(out+errb, adviseMarker) || strings.Contains(out+errb, "boom") || strings.Contains(out, "ship's branch") {
				t.Errorf("harness text reached the output:\nstdout %q\nstderr %q", out, errb)
			}
			if !r.ran() {
				t.Error("the harness never ran, so the failure was not the one under test")
			}
		})
	}
}

func TestAdviseReportsWhatAFailedCallCost(t *testing.T) {
	r := newAdviseRig(t, "budget", cappedPass, nil)
	err, out, _ := r.advise(t, validAdvice)
	if err != nil {
		t.Fatal(err)
	}
	if a := oneLine(t, out); a.Stakes != "high" || string(a.Cost) != "0.0123" {
		t.Errorf("answer = %+v, want high with the 0.0123 the harness reported", a)
	}
}

func TestAdviseAnswersHighWhenTheFleetHasNoJudgeHarness(t *testing.T) {
	r := newAdviseRig(t, "low", []capabilities.ID{capabilities.JudgeNoTools}, nil) // no network proof
	err, out, _ := r.advise(t, validAdvice)
	if err != nil {
		t.Fatalf("Advise = %v, want exit 0", err)
	}
	if a := oneLine(t, out); a.Stakes != "high" || a.Reason == "" || string(a.Cost) != "null" {
		t.Errorf("answer = %+v, want high, a reason and a null cost", a)
	}
	if r.ran() {
		t.Error("an unproven harness ran a judge call")
	}
}

func TestAdviseAnswersHighWithoutAShipOrAHome(t *testing.T) {
	cases := []struct {
		name, ship, report string
	}{
		{"no ship", "", "report"},
		{"no home", "saltpaw-ii", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAdviseRig(t, "low", judgePasses, nil)
			userHome := t.TempDir()
			t.Setenv("HOME", userHome)
			t.Setenv("ADMIRAL_SHIP", c.ship)
			t.Setenv("ADMIRAL_HOME", "")
			t.Setenv("ADMIRAL_REPORT", "")
			if c.report != "" {
				t.Setenv("ADMIRAL_REPORT", filepath.Join(r.home, "reports", "x.json"))
			}
			var out, errb bytes.Buffer
			if err := Advise(nil, IO{In: strings.NewReader(validAdvice), Out: &out, Err: &errb}); err != nil {
				t.Fatalf("Advise = %v, want exit 0", err)
			}
			if a := oneLine(t, out.String()); a.Stakes != "high" || a.Reason == "" {
				t.Errorf("answer = %+v, want high with a reason", a)
			}
			if r.ran() {
				t.Error("a call outside a ship ran the harness")
			}
			if _, err := os.Stat(filepath.Join(userHome, ".admiral")); err == nil {
				t.Error("advise created ~/.admiral")
			}
		})
	}
}

func TestAdviseAnswersHighWhenTheShipsFleetIsUnknown(t *testing.T) {
	r := newAdviseRig(t, "low", judgePasses, nil)
	shipFleet = func(string, string) (config.Fleet, bool) { return config.Fleet{}, false }
	err, out, _ := r.advise(t, validAdvice)
	if err != nil {
		t.Fatal(err)
	}
	if a := oneLine(t, out); a.Stakes != "high" {
		t.Errorf("answer = %+v, want high", a)
	}
	if r.ran() {
		t.Error("a ship with no fleet ran the harness")
	}
}

func TestAdviseFindsTheHomeFromTheReportBeforeADMIRAL_HOME(t *testing.T) {
	r := newAdviseRig(t, "low", judgePasses, nil)
	t.Setenv("ADMIRAL_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	other := os.Getenv("ADMIRAL_HOME")
	t.Setenv("ADMIRAL_SHIP", r.ship)
	t.Setenv("ADMIRAL_REPORT", filepath.Join(r.home, "reports", r.ship+".json"))
	var out bytes.Buffer
	if err := Advise(nil, IO{In: strings.NewReader(validAdvice), Out: &out, Err: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if len(r.lookups) != 1 || r.lookups[0] != [2]string{r.home, r.ship} {
		t.Errorf("fleet lookups = %v, want one for home %s and ship %s", r.lookups, r.home, r.ship)
	}
	if got, _ := filepath.Glob(filepath.Join(other, "outcomes", "*")); len(got) != 0 {
		t.Errorf("ADMIRAL_HOME got a record: %v", got)
	}
	if len(r.outcomes(t)) != 1 {
		t.Errorf("the record did not land under the report's home")
	}
}

func TestAdviseKeepsTheQuestionInThePromptAndOffTheCommandLine(t *testing.T) {
	r := newAdviseRig(t, "low", cappedPass, nil)
	if err, _, _ := r.advise(t, validAdvice); err != nil {
		t.Fatal(err)
	}
	argvs, _ := filepath.Glob(filepath.Join(r.log, "argv-*"))
	prompts, _ := filepath.Glob(filepath.Join(r.log, "prompt-*"))
	if len(argvs) != 1 || len(prompts) != 1 {
		t.Fatalf("calls = %d argv, %d prompt files, want one call", len(argvs), len(prompts))
	}
	rawArgv, _ := os.ReadFile(argvs[0])
	argv := strings.Split(strings.TrimSuffix(string(rawArgv), "\x00"), "\x00")
	for _, a := range argv {
		if strings.Contains(a, "Rebase") {
			t.Errorf("argument %q carries the question", a)
		}
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--model sonnet") {
		t.Errorf("argv = %q, want the strong model: the advisor is a Strong call", joined)
	}
	if !strings.Contains(joined, "--max-budget-usd=") {
		t.Errorf("argv = %q, want a per-call budget where the harness has a proven cap", joined)
	}
	prompt, _ := os.ReadFile(prompts[0])
	for _, want := range []string{"Rebase onto main?", "yes", "no"} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if !strings.Contains(strings.ToLower(string(prompt)), "high") || !strings.Contains(strings.ToLower(string(prompt)), "low") {
		t.Error("the prompt does not define low and high stakes")
	}
}

func TestAdviseLogsOneAdvisedLineWithNoQuestionText(t *testing.T) {
	cases := []struct {
		name, mode string
		cost       string
		failure    string
		stakes     string
	}{
		{"a low answer", "low", "0.0123", "", "low"},
		{"a spent budget", "budget", "", "budget spent", "high"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := map[string]string{}
			if c.cost != "" {
				env["ADVISE_COST"] = c.cost
			}
			r := newAdviseRig(t, c.mode, cappedPass, env)
			if err, _, _ := r.advise(t, validAdvice); err != nil {
				t.Fatal(err)
			}
			recs := r.outcomes(t)
			if len(recs) != 1 {
				t.Fatalf("outcomes = %v, want one advised line", recs)
			}
			rec := recs[0]
			if rec["event"] != "advised" || rec["ship"] != r.ship || rec["stakes"] != c.stakes || rec["harness"] != "claude" {
				t.Errorf("record = %v, want advised by claude for %s with stakes %s", rec, r.ship, c.stakes)
			}
			if f, has := rec["failure"]; c.failure == "" && has || c.failure != "" && f != c.failure {
				t.Errorf("failure = %v (present %v), want %q", f, has, c.failure)
			}
			if rec["fleet"] != "work" {
				t.Errorf("fleet = %v, want work: the record must say which fleet the judge call served", rec["fleet"])
			}
			wantCost := 0.0123
			if rec["cost"] != wantCost {
				t.Errorf("cost = %v, want %v", rec["cost"], wantCost)
			}
			raw, _ := json.Marshal(rec)
			for _, leak := range []string{"Rebase", "ship's branch", "options"} {
				if strings.Contains(string(raw), leak) {
					t.Errorf("the record holds %q: %s", leak, raw)
				}
			}
		})
	}
}
