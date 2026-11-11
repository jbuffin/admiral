package judge_test

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/judge"
)

func sameJSON(t *testing.T, got, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(g, w)
}

func failure(t *testing.T, err error) *judge.Failure {
	t.Helper()
	var f *judge.Failure
	if !errors.As(err, &f) {
		t.Fatalf("err = %v, want *judge.Failure", err)
	}
	if f.Detail == "" || strings.Contains(f.Detail, "\n") {
		t.Errorf("detail = %q, want one line of text", f.Detail)
	}
	return f
}

func wantFields(t *testing.T, rep judge.Reply) {
	t.Helper()
	if rep.Fields["severity"] != "low" || rep.Fields["summary"] != "ok" || rep.Fields["count"] != float64(2) {
		t.Errorf("fields = %v, want %s", rep.Fields, validFields)
	}
}

func TestClaudeCallReturnsValidatedFieldsAndCostUnderTheCap(t *testing.T) {
	r := newRig(t, "claude", full...)
	r.reply(claudeStructured(validFields, 0.00126809))
	rep := r.mustRun(judge.Lookout, injection, []byte(schema), 0.25)
	wantFields(t, rep)
	if !rep.Cost.Reported || rep.Cost.USD != 0.00126809 {
		t.Errorf("cost = %+v, want 0.00126809 reported", rep.Cost)
	}
	if !rep.Budget.Available || rep.Budget.USD != 0.25 {
		t.Errorf("budget = %+v, want a 0.25 cap", rep.Budget)
	}

	argv := r.argv()
	if !has(argv, "-p") && !has(argv, "--print") {
		t.Errorf("argv %q: want headless mode", argv)
	}
	if v, ok := flag(argv, "--tools"); !ok || v != "" {
		t.Errorf("argv %q: want --tools with an empty list", argv)
	}
	if !has(argv, "--strict-mcp-config") {
		t.Errorf("argv %q: want --strict-mcp-config", argv)
	}
	if v, ok := flag(argv, "--setting-sources"); !ok || v != "" {
		t.Errorf("argv %q: want --setting-sources with an empty list, so no settings hooks run", argv)
	}
	if v, _ := flag(argv, "--output-format"); v != "json" {
		t.Errorf("argv %q: want --output-format json", argv)
	}
	if v, ok := flag(argv, "--json-schema"); !ok || !sameJSON(t, v, schema) {
		t.Errorf("argv %q: want --json-schema with the schema", argv)
	}
	if v, ok := flag(argv, "--max-budget-usd"); !ok {
		t.Errorf("argv %q: want --max-budget-usd", argv)
	} else if f, err := strconv.ParseFloat(v, 64); err != nil || f != 0.25 {
		t.Errorf("--max-budget-usd = %q, want 0.25", v)
	}
}

func TestClaudeCallLeavesOutSchemaAndCapFlagsUntilProvenAndPutsTheSchemaInThePrompt(t *testing.T) {
	r := newRig(t, "claude", basic...)
	r.reply(claudeResult(validFields, 0.002))
	rep := r.mustRun(judge.Strong, injection, []byte(schema), 0.25)
	wantFields(t, rep)
	if rep.Budget.Available {
		t.Errorf("budget = %+v, want not available while the cap is not proven", rep.Budget)
	}
	argv := r.argv()
	for _, f := range []string{"--json-schema", "--max-budget-usd"} {
		if has(argv, f) {
			t.Errorf("argv %q: %s is there although it is not proven", argv, f)
		}
	}
	if v, _ := flag(argv, "--tools"); v != "" || !has(argv, "--tools") {
		t.Errorf("argv %q: tools must stay off", argv)
	}
	stdin := r.read("stdin")
	if !strings.Contains(stdin, injection) || !strings.Contains(stdin, schema) {
		t.Errorf("stdin = %q, want the prompt and the schema bytes as given", stdin)
	}
}

func TestEachHarnessGetsItsFixedModelPairAndTheEngineersPromptOnlyOnStdin(t *testing.T) {
	for _, name := range []string{"claude", "codex", "copilot"} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, name, basic...)
			body := validFields
			if name == "claude" {
				body = claudeResult(validFields, 0.002)
			}
			r.reply(body)
			var got [2]string
			for i, kind := range []judge.CallKind{judge.Lookout, judge.Strong} {
				r.mustRun(kind, injection, []byte(schema), 0.25)
				argv := r.argv()
				got[i], _ = flag(argv, "--model", "-m")
				for _, a := range argv {
					if name == "copilot" && strings.HasPrefix(a, injection) {
						t.Errorf("argv element %q starts with the prompt: it would be read as a flag", a)
					}
					if name != "copilot" && strings.Contains(a, injection) {
						t.Errorf("argv element %q holds the prompt: it belongs on stdin", a)
					}
				}
				sent := r.read("stdin") + strings.Join(argv, "\x00")
				if name != "copilot" && !strings.Contains(r.read("stdin"), injection) {
					t.Errorf("stdin = %q, want the prompt", r.read("stdin"))
				}
				if !strings.Contains(sent, injection) {
					t.Errorf("the prompt reached neither stdin nor an argument: %q", sent)
				}
			}
			if got[0] == "" || got[1] == "" || got[0] == got[1] {
				t.Errorf("models = %q, want two non-empty models that differ", got)
			}
			if name == "claude" && got != [2]string{"haiku", "sonnet"} {
				t.Errorf("models = %q, want haiku for the Lookout and sonnet for the rest", got)
			}
		})
	}
}

func TestCodexCallKeepsToolsAndTheSandboxOffAndTakesTheSchemaAsAFile(t *testing.T) {
	r := newRig(t, "codex", capabilities.JudgeNoTools, capabilities.JudgeNoNetwork, capabilities.JudgeJSONSchema)
	r.reply(validFields)
	rep := r.mustRun(judge.Strong, injection, []byte(schema), 0.25)
	wantFields(t, rep)
	if rep.Cost.Reported {
		t.Errorf("cost = %+v, want not reported: codex reports no USD", rep.Cost)
	}
	if rep.Budget.Available {
		t.Errorf("budget = %+v, want not available: codex has no per-call cap", rep.Budget)
	}
	argv := r.argv()
	if len(argv) == 0 || argv[0] != "exec" {
		t.Errorf("argv %q: want codex exec", argv)
	}
	if v, _ := flag(argv, "--sandbox", "-s"); v != "read-only" {
		t.Errorf("argv %q: want --sandbox read-only", argv)
	}
	for _, f := range []string{"--ephemeral", "--skip-git-repo-check"} {
		if !has(argv, f) {
			t.Errorf("argv %q: want %s", argv, f)
		}
	}
	for _, f := range []string{"shell_tool", "unified_exec", "apps", "plugins", "browser_use", "in_app_browser", "computer_use", "multi_agent", "hooks"} {
		if !hasPair(argv, "--disable", f) {
			t.Errorf("argv %q: want --disable %s", argv, f)
		}
	}
	if !hasPair(argv, "-c", `web_search="disabled"`) {
		t.Errorf("argv %q: want -c web_search=\"disabled\" as one element", argv)
	}
	if _, ok := flag(argv, "--output-schema"); !ok {
		t.Fatalf("argv %q: want --output-schema while the schema is proven", argv)
	}
	if got := r.read("schema"); !sameJSON(t, got, schema) {
		t.Errorf("the schema file held %q, want the schema", got)
	}
}

func TestCodexCallPutsTheSchemaInThePromptUntilProven(t *testing.T) {
	r := newRig(t, "codex", basic...)
	r.reply(validFields)
	r.mustRun(judge.Strong, injection, []byte(schema), 0)
	if has(r.argv(), "--output-schema") {
		t.Errorf("argv %q: --output-schema is there although it is not proven", r.argv())
	}
	if stdin := r.read("stdin"); !strings.Contains(stdin, schema) {
		t.Errorf("stdin = %q, want the schema in the prompt", stdin)
	}
}

func TestCopilotCallKeepsToolsAndMCPOffAndPutsTheSchemaInThePrompt(t *testing.T) {
	r := newRig(t, "copilot", basic...)
	r.reply(validFields)
	rep := r.mustRun(judge.Strong, injection, []byte(schema), 0.25)
	wantFields(t, rep)
	if rep.Cost.Reported || rep.Budget.Available {
		t.Errorf("cost = %+v, budget = %+v, want neither: copilot reports no USD and has no proven cap", rep.Cost, rep.Budget)
	}
	argv := r.argv()
	if !has(argv, "--disable-builtin-mcps") {
		t.Errorf("argv %q: want --disable-builtin-mcps", argv)
	}
	if !has(argv, "--available-tools") && !has(argv, "--excluded-tools") && !has(argv, "--deny-tool") {
		t.Errorf("argv %q: want a flag that keeps tools off", argv)
	}
	if has(argv, "--json-schema") || has(argv, "--output-schema") {
		t.Errorf("argv %q: copilot has no schema flag", argv)
	}
	sent := r.read("stdin") + strings.Join(argv, "\x00")
	if !strings.Contains(sent, schema) {
		t.Errorf("what copilot was sent = %q, want the schema in the prompt", sent)
	}
}

func TestTheHarnessEnvironmentLosesShipAndPaneVariablesAndKeepsTheFleets(t *testing.T) {
	for _, k := range []string{"ADMIRAL_SHIP", "TMUX"} {
		if !contains(capabilities.ScrubbedEnv, k) {
			t.Fatalf("capabilities.ScrubbedEnv = %q, want it to hold %s", capabilities.ScrubbedEnv, k)
		}
	}
	for _, k := range []string{"ADMIRAL_AUTOPILOT", "ADMIRAL_PROBE_NONCE", "ADMIRAL_PROBE_DIR", "ADMIRAL_SETUP_TERMINAL", "TMUX_TMPDIR", "ADMIRAL_HOME"} {
		if !contains(capabilities.ScrubbedEnv, k) {
			t.Fatalf("capabilities.ScrubbedEnv = %q, want it to hold %s", capabilities.ScrubbedEnv, k)
		}
	}
	for _, k := range capabilities.ScrubbedEnv {
		t.Setenv(k, "leaked")
	}
	r := newRig(t, "claude", basic...)
	r.reply(claudeResult(validFields, 0.001))
	r.mustRun(judge.Lookout, "hello", []byte(schema), 0)
	env := envOf(r.read("env"))
	for _, k := range capabilities.ScrubbedEnv {
		if v, set := env[k]; set {
			t.Errorf("%s=%q reaches the judge harness", k, v)
		}
	}
	if env["FAKE_MODE"] != "ok" {
		t.Error("the fleet's own env did not reach the harness")
	}
}

func hasPair(argv []string, name, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == name && argv[i+1] == value {
			return true
		}
	}
	return false
}

func TestASchemaTheValidatorDoesNotCheckIsRefusedBeforeAnyHarnessCall(t *testing.T) {
	cases := map[string]string{
		"unknown type":         `{"type":"object","properties":{"a":{"type":"null"}}}`,
		"type as array":        `{"type":"object","properties":{"a":{"type":["string","null"]}}}`,
		"minimum":              `{"type":"object","properties":{"a":{"type":"number","minimum":0}}}`,
		"const":                `{"type":"object","properties":{"a":{"const":"high"}}}`,
		"nil schema":           `null`,
		"root not an object":   `{"type":"string"}`,
		"unsupported in items": `{"type":"object","properties":{"a":{"type":"array","items":{"type":"string","pattern":"x"}}}}`,
	}
	for name, sch := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, "claude", full...)
			r.reply(claudeStructured(`{"a":7}`, 0.002))
			_, err := r.run(judge.Strong, "hello", []byte(sch), 0.25)
			f := failure(t, err)
			if f.Kind != judge.KindError || f.Cost.Reported {
				t.Errorf("failure = %+v, want error with no cost", f)
			}
			if _, statErr := os.Stat(r.log + "/argv"); statErr == nil {
				t.Error("the harness was called")
			}
		})
	}
}

func TestAnAnnotatedSchemaStillRuns(t *testing.T) {
	r := newRig(t, "claude", basic...)
	r.reply(claudeResult(`{"a":"x"}`, 0.002))
	sch := `{"$schema":"http://json-schema.org/draft-07/schema#","title":"t","description":"d","type":"object","properties":{"a":{"type":"string","description":"d"}}}`
	r.mustRun(judge.Strong, "hello", []byte(sch), 0)
}

func TestClaudeRetriesExhaustedOnTheSchemaIsABadReplyThatKeepsTheCost(t *testing.T) {
	r := newRig(t, "claude", full...)
	r.reply(`{"type":"result","subtype":"error_max_structured_output_retries","is_error":true,"total_cost_usd":0.03}`)
	f := failure(t, func() error { _, err := r.run(judge.Strong, "hello", []byte(schema), 0.25); return err }())
	if f.Kind != judge.KindBadReply || !f.Cost.Reported || f.Cost.USD != 0.03 {
		t.Errorf("failure = %+v, want bad reply with cost 0.03", f)
	}
}

func TestAClaudeErrorWithNoErrorsNeverReadsSuccess(t *testing.T) {
	r := newRig(t, "claude", full...)
	r.reply(`{"type":"result","subtype":"success","is_error":true}`)
	f := failure(t, func() error { _, err := r.run(judge.Strong, "hello", []byte(schema), 0.25); return err }())
	if f.Kind != judge.KindError || strings.Contains(f.Detail, "success") {
		t.Errorf("failure = %+v, want an error whose detail is not %q", f, "success")
	}
	r.setEnv("FAKE_MODE", "exit")
	f = failure(t, func() error { _, err := r.run(judge.Strong, "hello", []byte(schema), 0.25); return err }())
	if !strings.Contains(f.Detail, "boom") {
		t.Errorf("detail = %q, want the first line of stderr", f.Detail)
	}
}

func TestAHarnessThatWritesMoreThanAMebibyteGivesABadReply(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, name, basic...)
			r.reply(`{"severity":"low","summary":"` + strings.Repeat("x", 2<<20) + `"}`)
			f := failure(t, func() error { _, err := r.run(judge.Strong, "hello", []byte(schema), 0); return err }())
			if f.Kind != judge.KindBadReply {
				t.Errorf("failure = %+v, want bad reply", f)
			}
		})
	}
}

func TestACodexReplyFileOverAMebibyteGivesABadReplyWhileStdoutIsSmall(t *testing.T) {
	r := newRig(t, "codex", basic...)
	r.setEnv("FAKE_OUT_ONLY", "1")
	r.reply(`{"severity":"low","summary":"` + strings.Repeat("x", 2<<20) + `"}`)
	f := failure(t, func() error { _, err := r.run(judge.Strong, "hello", []byte(schema), 0); return err }())
	if f.Kind != judge.KindBadReply {
		t.Errorf("failure = %+v, want bad reply", f)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestACallRunsInAThrowawayDirectoryThatIsRemoved(t *testing.T) {
	r := newRig(t, "claude", basic...)
	r.reply(claudeResult(validFields, 0.001))
	r.mustRun(judge.Lookout, "hello", []byte(schema), 0)
	cwd := strings.TrimSpace(r.read("cwd"))
	here, _ := os.Getwd()
	if cwd == "" || cwd == here {
		t.Errorf("cwd = %q, want a directory of its own", cwd)
	}
	if _, err := os.Stat(cwd); err == nil {
		t.Errorf("the call's directory %s was not removed", cwd)
	}
}

func TestANonZeroExitIsAnErrorThatKeepsTheCostWhenReported(t *testing.T) {
	r := newRig(t, "claude", full...)
	r.setEnv("FAKE_MODE", "exit")
	f := failure(t, func() error { _, err := r.run(judge.Strong, "hello", []byte(schema), 0.25); return err }())
	if f.Kind != judge.KindError || f.Cost.Reported {
		t.Errorf("failure = %+v, want error with no cost", f)
	}

	r.reply(`{"type":"result","subtype":"error_during_execution","is_error":true,"total_cost_usd":0.004}`)
	f = failure(t, func() error { _, err := r.run(judge.Strong, "hello", []byte(schema), 0.25); return err }())
	if f.Kind != judge.KindError || !f.Cost.Reported || f.Cost.USD != 0.004 {
		t.Errorf("failure = %+v, want error with cost 0.004", f)
	}
}

func TestACodexNonZeroExitIsAnErrorWithNoCost(t *testing.T) {
	r := newRig(t, "codex", basic...)
	r.setEnv("FAKE_MODE", "exit")
	f := failure(t, func() error { _, err := r.run(judge.Strong, "hello", []byte(schema), 0); return err }())
	if f.Kind != judge.KindError || f.Cost.Reported {
		t.Errorf("failure = %+v, want error with no cost", f)
	}
}

func TestACallPastItsTimeoutIsATimeout(t *testing.T) {
	r := newRig(t, "claude", full...)
	r.setEnv("FAKE_MODE", "hang")
	start := time.Now()
	_, err := judge.Run(t.Context(), r.choose(), judge.Call{Kind: judge.Lookout, Prompt: "hello", Schema: []byte(schema), BudgetUSD: 0.25, Timeout: time.Second})
	f := failure(t, err)
	if f.Kind != judge.KindTimeout || f.Cost.Reported {
		t.Errorf("failure = %+v, want timeout with no cost", f)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("Run took %v for a 1s timeout", took)
	}
}

func TestASpentBudgetIsBudgetSpentWhateverTheExitCodeAndKeepsTheCost(t *testing.T) {
	r := newRig(t, "claude", full...)
	r.reply(`{"type":"result","subtype":"error_max_budget_usd","is_error":true,"total_cost_usd":0.26}`)
	for _, mode := range []string{"exit", "ok"} {
		r.setEnv("FAKE_MODE", mode)
		f := failure(t, func() error { _, err := r.run(judge.Strong, "hello", []byte(schema), 0.25); return err }())
		if f.Kind != judge.KindBudgetSpent || !f.Cost.Reported || f.Cost.USD != 0.26 {
			t.Errorf("exit mode %s: failure = %+v, want budget spent with cost 0.26", mode, f)
		}
	}
}

func TestAReplyThatDoesNotFitTheSchemaIsABadReplyThatKeepsTheCost(t *testing.T) {
	cases := map[string]string{
		"unparseable":              "this is not json",
		"not an object":            `[1]`,
		"missing a required field": `{"severity":"low"}`,
		"wrong type":               `{"severity":"low","summary":5}`,
		"fractional integer":       `{"severity":"low","summary":"ok","count":1.5}`,
		"enum value outside":       `{"severity":"medium","summary":"ok"}`,
		"over maxLength":           `{"severity":"low","summary":"abcdefghijk"}`,
		"extra property":           `{"severity":"low","summary":"ok","extra":1}`,
		"wrong item type":          `{"severity":"low","summary":"ok","tags":[1]}`,
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, "claude", basic...)
			r.reply(claudeResult(fields, 0.002))
			_, err := r.run(judge.Strong, "hello", []byte(schema), 0.25)
			f := failure(t, err)
			if f.Kind != judge.KindBadReply || !f.Cost.Reported || f.Cost.USD != 0.002 {
				t.Errorf("failure = %+v, want bad reply with cost 0.002", f)
			}
		})
	}
}

func TestStructuredOutputIsValidatedToo(t *testing.T) {
	r := newRig(t, "claude", full...)
	r.reply(claudeStructured(`{"severity":"medium","summary":"ok"}`, 0.002))
	f := failure(t, func() error { _, err := r.run(judge.Strong, "hello", []byte(schema), 0.25); return err }())
	if f.Kind != judge.KindBadReply {
		t.Errorf("failure = %+v, want bad reply: the schema flag is not trusted", f)
	}
}

func TestACopilotReplyThatDoesNotFitTheSchemaIsABadReply(t *testing.T) {
	r := newRig(t, "copilot", basic...)
	r.reply(`{"severity":"medium","summary":"ok"}`)
	f := failure(t, func() error { _, err := r.run(judge.Strong, "hello", []byte(schema), 0); return err }())
	if f.Kind != judge.KindBadReply || f.Cost.Reported {
		t.Errorf("failure = %+v, want bad reply with no cost", f)
	}
}
