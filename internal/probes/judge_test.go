package probes_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/probes"
)

// judgeHarness stands in for claude, codex and copilot (named by its file) on a judge call.
// It logs argv under $PROBE_FAKE_LOG, then acts out $PROBE_FAKE_MODE on the paths and URLs it
// finds in the prompt. Every answer fits {"reply": string}, the probes' reply schema.
const judgeHarness = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "${FAKE_VERSION:-2.1.300 (fake)}"; exit 0; fi
log=$PROBE_FAKE_LOG
mkdir -p "$log"
printf '%s\0' "$@" >"$log/argv-$$"
kind=$(basename "$0")
tools=""
for a in "$@"; do [ "$a" = --tools ] && tools=1; done
if [ "$kind" = claude ] && [ -z "$tools" ]; then echo "I will not run that."; exit 0; fi
prompt=$(cat)
mode=${PROBE_FAKE_MODE:-pass}
if [ "$mode" = hang-first ] && mkdir "$log/hung" 2>/dev/null; then sleep 30; exit 0; fi
[ "$mode" = error ] && { echo "boom: not logged in" >&2; exit 3; }
out=""; schema=""; budget=""; prev=""
for a in "$@"; do
  [ "$prev" = -o ] && out=$a
  case "$a" in --json-schema=*) schema=1 ;; --max-budget-usd=*) budget=${a#*=} ;; esac
  prev=$a
done
if [ "$kind" = claude ] && [ -n "$budget" ] && [ "$mode" != cap-success ]; then
  if [ "$mode" = cap-other ]; then
    echo '{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["it broke"],"total_cost_usd":0.0001}'; exit 1
  fi
  if awk -v b="$budget" 'BEGIN{exit !(b+0 < 0.001)}'; then
    echo '{"type":"result","subtype":"error_max_budget_usd","is_error":true,"errors":["Reached maximum budget"],"total_cost_usd":0.0001}'; exit 1
  fi
fi
urls=$(printf '%s' "$prompt" | grep -o 'http://[^ ]*' | sed "s/[.,;)\"']*\$//")
rest=$(printf '%s' "$prompt" | sed 's#https\{0,1\}://[^ ]*##g')
paths=$(printf '%s' "$rest" | grep -o '/[A-Za-z0-9_.-]*/[A-Za-z0-9_./-]*' | sed 's/[.]*$//')
text=ok
case "$mode" in
write) for p in $paths; do [ -e "$p" ] || touch "$p" 2>/dev/null; done ;;
read) for p in $paths; do [ -f "$p" ] && text=$(cat "$p"); done ;;
net) for u in $urls; do curl -s -m 5 "$u" >/dev/null 2>&1; done ;;
esac
case "$mode" in
plain) body=hello ;;
misfit) body='{"nope":1}' ;;
*) body="{\"reply\":\"$text\"}" ;;
esac
if [ "$kind" = claude ]; then
  if [ -n "$schema" ] && [ "$mode" != result-only ] && [ "$mode" != plain ]; then
    printf '{"type":"result","subtype":"success","is_error":false,"result":"","structured_output":%s,"total_cost_usd":0.0001}\n' "$body"
  else
    esc=$(printf '%s' "$body" | sed 's/"/\\"/g')
    printf '{"type":"result","subtype":"success","is_error":false,"result":"%s","total_cost_usd":0.0001}\n' "$esc"
  fi
  exit 0
fi
[ -n "$out" ] && printf '%s' "$body" >"$out"
printf '%s' "$body"
exit 0
`

var allHarnesses = []string{"claude", "codex", "copilot"}

var judgeIDs = []capabilities.ID{capabilities.JudgeNoTools, capabilities.JudgeNoNetwork, capabilities.JudgeJSONSchema, capabilities.JudgeCallCap}

type jrig struct {
	name  string
	fleet config.Fleet
	log   string
}

func exeNamed(t *testing.T, name, body string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(exe, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

// newJRig makes a fleet whose one harness is the inline judge fake. The log directory is
// left uncreated, so its absence proves the harness never ran.
func newJRig(t *testing.T, harness, mode string) *jrig {
	t.Helper()
	j := &jrig{name: harness, log: filepath.Join(t.TempDir(), "log")}
	env := map[string]string{"PROBE_FAKE_LOG": j.log, "PROBE_FAKE_MODE": mode}
	j.fleet = config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: harness, Executable: exeNamed(t, harness, judgeHarness), Found: true, Env: env}}}
	return j
}

func (j *jrig) target(t *testing.T) probes.Target {
	return probes.Target{Home: home(t), Fleet: j.fleet, Harness: j.name, Version: "2.1.300 (fake)"}
}

func (j *jrig) run(t *testing.T, id capabilities.ID) probes.Outcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	return probes.Judge(j.name, id).Run(ctx, j.target(t))
}

func (j *jrig) argvs(t *testing.T) [][]string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(j.log, "argv-*"))
	var out [][]string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00"))
	}
	return out
}

func flagValue(argv []string, names ...string) (string, bool) {
	for i, a := range argv {
		for _, n := range names {
			if a == n && i+1 < len(argv) {
				return argv[i+1], true
			}
			if v, ok := strings.CutPrefix(a, n+"="); ok {
				return v, true
			}
		}
	}
	return "", false
}

func oneLine(t *testing.T, o probes.Outcome) {
	t.Helper()
	if o.Detail == "" || strings.Contains(o.Detail, "\n") {
		t.Errorf("detail = %q, want one line of text", o.Detail)
	}
}

func TestEveryHarnessRegistersItsJudgeProbesAndStoresThemUnderTheVersion(t *testing.T) {
	want := map[string]map[capabilities.ID]string{ // "" means passed
		"claude":  {capabilities.JudgeNoTools: "", capabilities.JudgeNoNetwork: "", capabilities.JudgeJSONSchema: "", capabilities.JudgeCallCap: ""},
		"codex":   {capabilities.JudgeNoTools: "", capabilities.JudgeNoNetwork: "", capabilities.JudgeJSONSchema: "", capabilities.JudgeCallCap: "codex has no per-call cap"},
		"copilot": {capabilities.JudgeNoTools: "", capabilities.JudgeNoNetwork: "", capabilities.JudgeJSONSchema: "copilot has no JSON schema flag", capabilities.JudgeCallCap: "copilot caps AI credits, not USD"},
	}
	for _, name := range allHarnesses {
		t.Run(name, func(t *testing.T) {
			j := newJRig(t, name, "pass")
			h := home(t)
			rep, err := probes.NewDefault(h).Run(t.Context(), j.fleet, name)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			for id, reason := range want[name] {
				l := line(t, rep, id)
				switch {
				case reason == "" && l.Status != capabilities.Passed:
					t.Errorf("%s = %+v, want passed", id, l)
				case reason != "" && (l.Status != capabilities.Failed || l.Detail != reason):
					t.Errorf("%s = %+v, want failed with %q", id, l, reason)
				}
			}
			if l := line(t, rep, capabilities.JudgeUsage); l.Status != capabilities.NotChecked {
				t.Errorf("usage reports = %+v, want not checked: it has no probe", l)
			}
			snap, err := capabilities.Open(h).Snapshot("work", name)
			if err != nil {
				t.Fatal(err)
			}
			if snap.Version != "2.1.300 (fake)" || snap.States[capabilities.JudgeNoTools].Status != capabilities.Passed {
				t.Errorf("store: version %q, no tools %v; want the detected version and passed", snap.Version, snap.States[capabilities.JudgeNoTools].Status)
			}
		})
	}
}

func TestEachJudgeCallRunsTheSmallModelWithTheHarnessesOwnMechanism(t *testing.T) {
	for _, name := range allHarnesses {
		small, _, _ := judge.Models(name)
		t.Run(name, func(t *testing.T) {
			j := newJRig(t, name, "pass")
			j.run(t, capabilities.JudgeNoTools)
			argvs := j.argvs(t)
			if len(argvs) != 1 {
				t.Fatalf("the harness ran %d times, want one judge call", len(argvs))
			}
			if m, _ := flagValue(argvs[0], "--model", "-m"); m != small || small == "" {
				t.Errorf("model = %q, want the Lookout model %q", m, small)
			}
		})
	}
	j := newJRig(t, "claude", "pass")
	j.run(t, capabilities.JudgeJSONSchema)
	if v, ok := flagValue(j.argvs(t)[0], "--json-schema"); !ok || !strings.HasPrefix(v, "{") {
		t.Errorf("claude's schema probe ran without --json-schema: %v", j.argvs(t)[0])
	}
}

func TestNoToolsProbePassesOnlyWhenNothingWasWrittenOrRead(t *testing.T) {
	leaks := map[string]string{"write": "sentinel", "read": "nonce"}
	for _, name := range allHarnesses {
		t.Run(name+"/quiet", func(t *testing.T) {
			if o := newJRig(t, name, "pass").run(t, capabilities.JudgeNoTools); !o.Passed {
				t.Errorf("outcome = %+v, want a pass when the fake leaves no sentinel and echoes no nonce", o)
			} else {
				oneLine(t, o)
			}
		})
		details := map[string]string{}
		for _, mode := range []string{"write", "read", "error"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				o := newJRig(t, name, mode).run(t, capabilities.JudgeNoTools)
				if o.Passed {
					t.Fatalf("passed although the harness leaked (%s)", mode)
				}
				oneLine(t, o)
				if want := leaks[mode]; want != "" && !strings.Contains(strings.ToLower(o.Detail), want) {
					t.Errorf("detail = %q, want it to name the %s", o.Detail, want)
				}
				for other, d := range details {
					if d == o.Detail {
						t.Errorf("%s and %s fail with the same detail %q", mode, other, d)
					}
				}
				details[mode] = o.Detail
			})
		}
	}
}

func TestNoNetworkProbePassesOnlyWhenTheListenerSawNothing(t *testing.T) {
	for _, name := range allHarnesses {
		t.Run(name+"/quiet", func(t *testing.T) {
			if o := newJRig(t, name, "pass").run(t, capabilities.JudgeNoNetwork); !o.Passed {
				t.Errorf("outcome = %+v, want a pass when nothing is fetched", o)
			}
		})
		t.Run(name+"/fetches", func(t *testing.T) {
			o := newJRig(t, name, "net").run(t, capabilities.JudgeNoNetwork)
			if o.Passed || !strings.Contains(strings.ToLower(o.Detail), "listener") {
				t.Errorf("outcome = %+v, want a fail that names the listener", o)
			}
			oneLine(t, o)
		})
		t.Run(name+"/error", func(t *testing.T) {
			if o := newJRig(t, name, "error").run(t, capabilities.JudgeNoNetwork); o.Passed {
				t.Errorf("outcome = %+v: a call that never answered proves nothing", o)
			}
		})
	}
}

func TestJSONSchemaProbeClaudePassesOnlyOnStructuredOutputThatFits(t *testing.T) {
	if o := newJRig(t, "claude", "pass").run(t, capabilities.JudgeJSONSchema); !o.Passed {
		t.Errorf("outcome = %+v, want a pass on a structured_output that fits", o)
	}
	details := map[string]string{}
	for _, mode := range []string{"plain", "result-only", "misfit", "error"} {
		o := newJRig(t, "claude", mode).run(t, capabilities.JudgeJSONSchema)
		if o.Passed {
			t.Errorf("%s: passed, but the harness did not enforce the schema", mode)
		}
		oneLine(t, o)
		if mode == "result-only" && !strings.Contains(o.Detail, "structured") {
			t.Errorf("result-only detail = %q, want it to say the reply was not structured_output", o.Detail)
		}
		for other, d := range details {
			if d == o.Detail {
				t.Errorf("%s and %s fail with the same detail %q", mode, other, d)
			}
		}
		details[mode] = o.Detail
	}
}

func TestJSONSchemaProbeCodexPassesOnAFittingReplyAndNotesWhatItCannotSee(t *testing.T) {
	o := newJRig(t, "codex", "pass").run(t, capabilities.JudgeJSONSchema)
	if !o.Passed || !strings.Contains(strings.ToLower(o.Detail), "last message") {
		t.Errorf("outcome = %+v, want a pass whose detail notes that -o holds the last message either way", o)
	}
	for _, mode := range []string{"plain", "misfit"} {
		if o := newJRig(t, "codex", mode).run(t, capabilities.JudgeJSONSchema); o.Passed {
			t.Errorf("%s: passed with a reply the schema forbids", mode)
		}
	}
}

func TestCallCapProbePassesOnlyWhenClaudeReportsTheCapStoppedIt(t *testing.T) {
	j := newJRig(t, "claude", "pass")
	if o := j.run(t, capabilities.JudgeCallCap); !o.Passed {
		t.Fatalf("outcome = %+v, want a pass on error_max_budget_usd", o)
	}
	v, ok := flagValue(j.argvs(t)[0], "--max-budget-usd")
	if f, err := strconv.ParseFloat(v, 64); !ok || err != nil || f <= 0 || f >= 0.001 {
		t.Errorf("--max-budget-usd = %q, want a positive cap far below one call's cost", v)
	}
	for _, mode := range []string{"cap-success", "cap-other", "error"} {
		if o := newJRig(t, "claude", mode).run(t, capabilities.JudgeCallCap); o.Passed {
			t.Errorf("%s: passed although the cap did not stop the call", mode)
		}
	}
}

func TestAFixedFailRecordsItsReasonAndMakesNoHarnessCall(t *testing.T) {
	for _, c := range []struct {
		harness string
		id      capabilities.ID
		reason  string
	}{
		{"codex", capabilities.JudgeCallCap, "codex has no per-call cap"},
		{"copilot", capabilities.JudgeJSONSchema, "copilot has no JSON schema flag"},
		{"copilot", capabilities.JudgeCallCap, "copilot caps AI credits, not USD"},
	} {
		t.Run(c.harness+"/"+string(c.id), func(t *testing.T) {
			j := newJRig(t, c.harness, "pass")
			o := j.run(t, c.id)
			if o.Passed || o.Detail != c.reason {
				t.Errorf("outcome = %+v, want a fail reading %q", o, c.reason)
			}
			if _, err := os.Stat(j.log); err == nil {
				t.Errorf("the harness ran (%v): a capability it can never have needs no model call", j.argvs(t))
			}
		})
	}
}

func TestAFullClaudeRunFitsTheRunnersTimeEvenWhenOneJudgeCallHangs(t *testing.T) {
	j := newJRig(t, "claude", "hang-first")
	h := home(t)
	r := probes.NewDefault(h)
	r.Timeout = 12 * time.Second
	start := time.Now()
	rep, err := r.Run(t.Context(), j.fleet, "claude")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if took := time.Since(start); took > 12*time.Second+time.Second {
		t.Errorf("Run took %s, past its %s limit", took, r.Timeout)
	}
	passed, failed := 0, 0
	for _, id := range judgeIDs {
		switch line(t, rep, id).Status {
		case capabilities.Passed:
			passed++
		case capabilities.Failed:
			failed++
		}
	}
	if passed != 3 || failed != 1 {
		t.Errorf("judge probes: %d passed, %d failed, want the hung call to fail alone: each call gets a share of the limit, so one cannot eat the rest", passed, failed)
	}
	if snap, _ := capabilities.Open(h).Snapshot("work", "claude"); snap.Version != "2.1.300 (fake)" {
		t.Errorf("stored version = %q, want results stored under the detected version", snap.Version)
	}
}

func realFake(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "e2e", "fakeharness", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTheFakeHarnessAnswersJudgeProbesAndLeavesHeadlessRunsAlone(t *testing.T) {
	for _, leak := range []bool{false, true} {
		for _, name := range allHarnesses {
			t.Run(name+map[bool]string{false: "/default", true: "/leak"}[leak], func(t *testing.T) {
				rec := filepath.Join(t.TempDir(), "rec")
				env := map[string]string{"FAKE_RECORD_DIR": rec}
				if leak {
					env["FAKE_JUDGE"] = "leak"
				}
				fleet := config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: name, Executable: realFake(t, name), Found: true, Env: env}}}
				tg := probes.Target{Home: home(t), Fleet: fleet, Harness: name, Version: "2.1.300 (fake)"}
				// A fixed fail is the same either way; the rest pass by default and all fail on the leak cue.
				mechanism := map[string]map[capabilities.ID]bool{
					"claude":  {capabilities.JudgeNoTools: true, capabilities.JudgeNoNetwork: true, capabilities.JudgeJSONSchema: true, capabilities.JudgeCallCap: true},
					"codex":   {capabilities.JudgeNoTools: true, capabilities.JudgeNoNetwork: true, capabilities.JudgeJSONSchema: true},
					"copilot": {capabilities.JudgeNoTools: true, capabilities.JudgeNoNetwork: true},
				}[name]
				for _, id := range judgeIDs {
					o := probes.Judge(name, id).Run(t.Context(), tg)
					if want := mechanism[id] && !leak; o.Passed != want {
						t.Errorf("%s: passed = %v (%s), want %v", id, o.Passed, o.Detail, want)
					}
				}
				if _, err := os.Stat(filepath.Join(rec, "headless-runs")); err == nil {
					t.Error("judge calls appended to headless-runs: the setup tests count only guard runs")
				}
			})
		}
	}
}
