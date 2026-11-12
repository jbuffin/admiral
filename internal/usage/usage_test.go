package usage

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
)

func sample(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func wantTokens(t *testing.T, got, want Tokens) {
	t.Helper()
	if got != want {
		t.Errorf("tokens = %+v, want %+v", got, want)
	}
}

func claudeState(cost, models, unknown string) string {
	return `{"type":"cost-state","totalCostUSD":` + cost + `,"modelUsage":{` + models + `},"hasUnknownModelCost":` + unknown + `}`
}

func TestClaudeRecordedSample(t *testing.T) {
	f, err := ParseClaude(strings.NewReader(sample(t, "claude-recorded.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	wantTokens(t, f.Tokens, Tokens{Input: 2, Output: 4, CacheRead: 11382, CacheWrite: 18024})
	if f.Money == nil || f.Money.Basis != BasisEstimate {
		t.Fatalf("money = %+v, want an estimate", f.Money)
	}
	near(t, "money", f.Money.Amount, 0.00372082)
	if f.Money.Incomplete {
		t.Error("Incomplete = true for hasUnknownModelCost false")
	}
}

func TestClaudeExistingFixtureSumsModels(t *testing.T) {
	f, err := ParseClaude(strings.NewReader(sample(t, "../../conversation/testdata/claude-sample.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	// Ephemeral 5m/1h split cacheCreationInputTokens and must not be added again.
	wantTokens(t, f.Tokens, Tokens{Input: 2342 + 5449, Output: 26 + 2446, Reasoning: 480, CacheRead: 817779, CacheWrite: 84288})
	near(t, "money", f.Money.Amount, 0.8582489999999999)
}

func TestClaudeLastCostStateWins(t *testing.T) {
	in := claudeState("0.01", `"m":{"inputTokens":1,"outputTokens":1}`, "false") + "\n" +
		`{"type":"user","message":"x"}` + "\n" +
		claudeState("0.05", `"m":{"inputTokens":9,"outputTokens":3}`, "false") + "\n" +
		`{"type":"assistant"}` + "\n"
	f, err := ParseClaude(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	wantTokens(t, f.Tokens, Tokens{Input: 9, Output: 3})
	near(t, "money", f.Money.Amount, 0.05)
}

func TestClaudeSumsSeveralModelsAndKeepsUnknownFlag(t *testing.T) {
	in := claudeState("0.30",
		`"a":{"inputTokens":10,"outputTokens":20,"thinkingTokens":5,"cacheReadInputTokens":100,"cacheCreationInputTokens":40,"cacheCreationEphemeral5mInputTokens":30,"cacheCreationEphemeral1hInputTokens":10},`+
			`"b":{"inputTokens":1,"outputTokens":2,"thinkingTokens":3,"cacheReadInputTokens":4,"cacheCreationInputTokens":5,"cacheCreationEphemeral5mInputTokens":5}`,
		"true")
	f, err := ParseClaude(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	wantTokens(t, f.Tokens, Tokens{Input: 11, Output: 22, Reasoning: 8, CacheRead: 104, CacheWrite: 45})
	if !f.Money.Incomplete {
		t.Error("Incomplete = false, want true for hasUnknownModelCost")
	}
}

func TestClaudeNoFiguresIsNotZeros(t *testing.T) {
	for name, in := range map[string]string{
		"empty":         "",
		"no cost-state": `{"type":"user"}` + "\n" + `{"type":"assistant"}` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseClaude(strings.NewReader(in)); !errors.Is(err, ErrNoFigures) {
				t.Errorf("err = %v, want ErrNoFigures", err)
			}
		})
	}
}

func TestClaudeSkipsTruncatedTrailingLine(t *testing.T) {
	good := claudeState("0.02", `"m":{"inputTokens":7,"outputTokens":1}`, "false")
	f, err := ParseClaude(strings.NewReader(good + "\n" + `{"type":"cost-state","totalCostUSD":0.9,"modelUs`))
	if err != nil {
		t.Fatal(err)
	}
	wantTokens(t, f.Tokens, Tokens{Input: 7, Output: 1})
	near(t, "money", f.Money.Amount, 0.02)

	// A truncated line alone holds no figures.
	if _, err := ParseClaude(strings.NewReader(`{"type":"cost-st`)); !errors.Is(err, ErrNoFigures) {
		t.Errorf("err = %v, want ErrNoFigures", err)
	}
}

func codexLine(info, rate string) string {
	return `{"type":"event_msg","payload":{"type":"token_count","info":` + info + `,"rate_limits":` + rate + `}}`
}

func codexInfo(in, cached, out, reasoning int) string {
	return fmt.Sprintf(`{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"cache_write_input_tokens":0,"output_tokens":%d,"reasoning_output_tokens":%d},"last_token_usage":{"input_tokens":1,"cached_input_tokens":1,"output_tokens":1,"reasoning_output_tokens":1}}`, in, cached, out, reasoning)
}

func rate(primary, secondary string) string {
	return `{"primary":{"used_percent":` + primary + `,"window_minutes":300,"resets_at":1000},"secondary":{"used_percent":` + secondary + `,"window_minutes":10080,"resets_at":2000},"plan_type":"team"}`
}

func TestCodexRecordedSample(t *testing.T) {
	f, err := ParseCodex(strings.NewReader(sample(t, "codex-recorded.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	// cached is part of input, reasoning part of output: reported as given, not re-added.
	// Classes are disjoint: Codex's cached input is inside input_tokens, so Input is what is left.
	wantTokens(t, f.Tokens, Tokens{Input: 20281 - 12672, Output: 5, CacheRead: 12672})
	if f.Money != nil {
		t.Errorf("money = %+v, Codex reports none", f.Money)
	}
	if f.Quota == nil || f.Quota.Primary == nil || f.Quota.Secondary == nil {
		t.Fatalf("quota = %+v, want both windows", f.Quota)
	}
	near(t, "primary used", f.Quota.Primary.UsedPercent, 0)
	near(t, "secondary used", f.Quota.Secondary.UsedPercent, 4)
	if f.Quota.Primary.WindowMinutes != 300 || f.Quota.Primary.ResetsAt != 1794470989 {
		t.Errorf("primary = %+v", f.Quota.Primary)
	}
	if f.Quota.Secondary.WindowMinutes != 10080 || f.Quota.Secondary.ResetsAt != 1794518916 {
		t.Errorf("secondary = %+v", f.Quota.Secondary)
	}
	if f.Quota.PlanType != "team" {
		t.Errorf("plan = %q", f.Quota.PlanType)
	}
}

func TestCodexExistingFixtureLastTokenCountWinsUsingTotals(t *testing.T) {
	f, err := ParseCodex(strings.NewReader(sample(t, "../../conversation/testdata/codex-sample.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	// total_token_usage of the last line, not last_token_usage (20521 input).
	wantTokens(t, f.Tokens, Tokens{Input: 61306 - 53248, Output: 110 - 9, Reasoning: 9, CacheRead: 53248})
}

func TestCodexNullInfoKeepsTokensUpdatesQuota(t *testing.T) {
	in := codexLine(codexInfo(500, 400, 50, 10), rate("1.0", "2.0")) + "\n" +
		`{"type":"event_msg","payload":{"type":"agent_message"}}` + "\n" +
		codexLine("null", rate("7.5", "9.5")) + "\n"
	f, err := ParseCodex(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	wantTokens(t, f.Tokens, Tokens{Input: 100, Output: 40, Reasoning: 10, CacheRead: 400})
	near(t, "primary used", f.Quota.Primary.UsedPercent, 7.5)
	near(t, "secondary used", f.Quota.Secondary.UsedPercent, 9.5)
}

func TestCodexNoFiguresIsNotZeros(t *testing.T) {
	for name, in := range map[string]string{
		"empty":          "",
		"no token_count": `{"type":"event_msg","payload":{"type":"agent_message"}}` + "\n",
		"only null info": codexLine("null", rate("1.0", "2.0")) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseCodex(strings.NewReader(in)); !errors.Is(err, ErrNoFigures) {
				t.Errorf("err = %v, want ErrNoFigures", err)
			}
		})
	}
}

func TestCodexSkipsTruncatedTrailingLine(t *testing.T) {
	in := codexLine(codexInfo(30, 10, 5, 1), rate("1.0", "2.0")) + "\n" +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_us`
	f, err := ParseCodex(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	wantTokens(t, f.Tokens, Tokens{Input: 20, Output: 4, Reasoning: 1, CacheRead: 10})
}

func TestCopilotRecordedShutdown(t *testing.T) {
	f, err := ParseCopilot(strings.NewReader(sample(t, "copilot-recorded.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	// Copilot's inputTokens (35736) includes the cache writes; its own tokenDetails say input is 4.
	wantTokens(t, f.Tokens, Tokens{Input: 4, Output: 4, CacheWrite: 35732})
	if f.Money == nil || f.Money.Basis != BasisCredits {
		t.Fatalf("money = %+v, want credits", f.Money)
	}
	near(t, "credits", f.Money.Amount, 17.8756)
	near(t, "premium request cost", f.Money.PremiumRequestCost, 15)
}

func TestCopilotExistingFixtureHasNoFigures(t *testing.T) {
	// Its usage lines are scrubbed to {"scrubbed":true}: nothing to read.
	if _, err := ParseCopilot(strings.NewReader(sample(t, "../../conversation/testdata/copilot-sample.jsonl"))); !errors.Is(err, ErrNoFigures) {
		t.Errorf("err = %v, want ErrNoFigures", err)
	}
}

func copilotModel(in, out, cacheRead, cacheWrite, reasoning, nano string) string {
	return `{"requests":{"count":1,"cost":1},"usage":{"inputTokens":` + in + `,"outputTokens":` + out + `,"cacheReadTokens":` + cacheRead + `,"cacheWriteTokens":` + cacheWrite + `,"reasoningTokens":` + reasoning + `},"totalNanoAiu":` + nano + `}`
}

func TestCopilotShutdownSumsModelsNotAgents(t *testing.T) {
	a := copilotModel("100", "10", "50", "20", "2", "1500000000")
	b := copilotModel("200", "30", "5", "0", "4", "2500000000")
	in := `{"type":"session.shutdown","data":{"totalNanoAiu":4000000000,"modelMetrics":{"m1":` + a + `,"m2":` + b + `},` +
		`"agentMetrics":{"main":{"totalNanoAiu":4000000000,"modelMetrics":{"m1":` + a + `,"m2":` + b + `}},"sub":{"totalNanoAiu":4000000000,"modelMetrics":{"m1":` + a + `,"m2":` + b + `}}},` +
		`"accountingSnapshot":{"totalPremiumRequestCost":3.0}}}` + "\n"
	f, err := ParseCopilot(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	wantTokens(t, f.Tokens, Tokens{Input: 225, Output: 40, Reasoning: 6, CacheRead: 55, CacheWrite: 20})
	near(t, "credits", f.Money.Amount, 4)
	near(t, "premium request cost", f.Money.PremiumRequestCost, 3)
}

func usageRecord(in, out, cacheRead, cacheWrite, reasoning, nano string) string {
	return `{"type":"session.usage_record","data":{"usage":{"inputTokens":` + in + `,"outputTokens":` + out + `,"cacheReadTokens":` + cacheRead + `,"cacheWriteTokens":` + cacheWrite + `,"reasoningTokens":` + reasoning + `,"copilotUsage":{"totalNanoAiu":` + nano + `}}}}`
}

func TestCopilotWithoutShutdownSumsUsageRecords(t *testing.T) {
	in := usageRecord("100", "10", "50", "20", "2", "1000000000") + "\n" +
		`{"type":"assistant.message"}` + "\n" +
		usageRecord("200", "30", "5", "0", "4", "500000000") + "\n"
	f, err := ParseCopilot(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	wantTokens(t, f.Tokens, Tokens{Input: 225, Output: 40, Reasoning: 6, CacheRead: 55, CacheWrite: 20})
	if f.Money == nil || f.Money.Basis != BasisCredits {
		t.Fatalf("money = %+v, want credits", f.Money)
	}
	near(t, "credits", f.Money.Amount, 1.5)
}

func TestCopilotShutdownWinsOverUsageRecords(t *testing.T) {
	in := usageRecord("1", "1", "0", "0", "0", "1000000000") + "\n" +
		`{"type":"session.shutdown","data":{"totalNanoAiu":9000000000,"modelMetrics":{"m":` + copilotModel("7", "8", "0", "0", "0", "9000000000") + `}}}` + "\n"
	f, err := ParseCopilot(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	wantTokens(t, f.Tokens, Tokens{Input: 7, Output: 8})
	near(t, "credits", f.Money.Amount, 9)
}

func TestCopilotNoFiguresIsNotZeros(t *testing.T) {
	for name, in := range map[string]string{
		"empty":       "",
		"no usage":    `{"type":"assistant.message"}` + "\n",
		"scrubbed":    `{"type":"session.usage_record","data":{"scrubbed":true}}` + "\n",
		"scrubbed sd": `{"type":"session.shutdown","data":{"scrubbed":true,"shutdownType":"routine"}}` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseCopilot(strings.NewReader(in)); !errors.Is(err, ErrNoFigures) {
				t.Errorf("err = %v, want ErrNoFigures", err)
			}
		})
	}
}

func TestCopilotSkipsTruncatedTrailingLine(t *testing.T) {
	in := usageRecord("100", "10", "0", "0", "0", "1000000000") + "\n" +
		`{"type":"session.usage_record","data":{"usage":{"inputTokens":99`
	f, err := ParseCopilot(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	wantTokens(t, f.Tokens, Tokens{Input: 100, Output: 10})
	near(t, "credits", f.Money.Amount, 1)
}
