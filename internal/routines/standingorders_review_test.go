package routines_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/routines"
)

func numbered(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("stored check %02d", i))
	}
	return out
}

func TestStandingOrderDraftCountsTheFleetsStoredChecksAgainstTheCap(t *testing.T) {
	r := newSO(t)
	r.put("work", map[string]any{"checks": numbered(18)})
	r.answers(map[string]any{"limits": map[string]any{}, "checks": []any{"add tests one", "add tests two", "add tests three"}, "guidance": ""})
	_, got, _ := r.draft("Always add tests one. Always add tests two. Always add tests three.")
	if !reflect.DeepEqual(got.Draft.Checks, []string{"add tests one", "add tests two"}) || !hasProblem(got, "checks") {
		t.Errorf("checks %v, problems %+v; want the 2 that fit beside 18 stored and a checks problem", got.Draft.Checks, got.Problems)
	}
}

func TestStandingOrderDraftDropsACheckEqualToAStoredOne(t *testing.T) {
	r := newSO(t)
	r.put("work", map[string]any{"checks": []string{"run make check"}})
	r.answers(map[string]any{"limits": map[string]any{}, "checks": []any{" run make check ", "add tests"}, "guidance": ""})
	_, got, _ := r.draft("Always  run make check. Always add tests.")
	if !reflect.DeepEqual(got.Draft.Checks, []string{"add tests"}) || !hasProblem(got, "checks") || !strings.Contains(got.Problems[0].Message, "run make check") {
		t.Errorf("checks %v, problems %+v; want the duplicate dropped and named", got.Draft.Checks, got.Problems)
	}
}

func TestStandingOrderDraftCountsStoredGuidanceJoinedByABlankLine(t *testing.T) {
	r := newSO(t)
	stored := runes(1990, "s")
	r.put("work", map[string]any{"guidance": stored})
	r.answers(map[string]any{"limits": map[string]any{}, "checks": []any{}, "guidance": "Prefer small PRs."})
	_, got, _ := r.draft("Prefer small PRs.")
	if got.Draft.Guidance != nil || !hasProblem(got, "guidance") {
		t.Errorf("guidance %v, problems %+v; want it dropped with a guidance problem (1990 + 2 + 17 > 2000)", got.Draft.Guidance, got.Problems)
	}
	r.put("work", map[string]any{"guidance": runes(1900, "s")})
	_, got, _ = r.draft("Prefer small PRs.")
	if got.Draft.Guidance == nil || hasProblem(got, "guidance") {
		t.Errorf("guidance %v, problems %+v; want it kept when stored + blank line + drafted fits", got.Draft.Guidance, got.Problems)
	}
}

func TestStandingOrderDraftChecksTheRepoAllowlistLikeASave(t *testing.T) {
	r := newSO(t)
	r.cfg.SetRepoLookup(func(string) []string { return []string{"acme/widget"} })
	fields := goodDraft()
	fields["limits"] = map[string]any{"repo_allowlist": []any{"acme/other"}}
	r.answers(fields)
	code, got, raw := r.draft(draftProse)
	if code != 200 || raw["draft"].(map[string]any)["limits"] != nil {
		t.Fatalf("draft = %d %v, want 200 with null limits", code, raw["draft"])
	}
	if !hasProblem(got, "limits") || !strings.Contains(got.Problems[0].Message, "repo_allowlist") {
		t.Errorf("problems = %+v, want one on limits naming repo_allowlist", got.Problems)
	}
	if err := r.cfg.SetFleetFields("work", config.FleetFields{Limits: map[string]json.RawMessage{"repo_allowlist": json.RawMessage(`["acme/other"]`)}}); err == nil {
		t.Error("a save of the same patch was accepted; preview and save must agree")
	}
}

func TestStandingOrderDraftKeepsOnlyWholeWordMatches(t *testing.T) {
	r := newSO(t)
	r.answers(map[string]any{"limits": map[string]any{}, "checks": []any{"run", "rerun the tests"}, "guidance": "tests."})
	_, got, _ := r.draft("Always rerun the tests. Write tests.")
	if !reflect.DeepEqual(got.Draft.Checks, []string{"rerun the tests"}) || !hasProblem(got, "checks") {
		t.Errorf("checks %v, problems %+v; want the mid-word \"run\" dropped", got.Draft.Checks, got.Problems)
	}
	if got.Draft.Guidance == nil || *got.Draft.Guidance != "tests." {
		t.Errorf("guidance = %v, want a whole-word match kept", got.Draft.Guidance)
	}
	r.answers(map[string]any{"limits": map[string]any{}, "checks": []any{}, "guidance": "ests."})
	_, got, _ = r.draft("Write tests.")
	if got.Draft.Guidance != nil || !hasProblem(got, "guidance") {
		t.Errorf("guidance = %v, problems %+v; want a mid-word start dropped", got.Draft.Guidance, got.Problems)
	}
}

func TestStoreTrimsChecksAndRefusesDuplicatesAfterTrimming(t *testing.T) {
	r := newSO(t)
	padded := []string{"  Run make check "}
	if _, err := r.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &padded}); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.store.StandingOrders("work"); !reflect.DeepEqual(got.Checks, []string{"Run make check"}) {
		t.Errorf("stored checks = %q, want them trimmed", got.Checks)
	}
	dup := []string{"Run make check", " Run make check"}
	if _, err := r.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &dup}); !errors.Is(err, routines.ErrInvalid) || !strings.Contains(err.Error(), "listed twice") {
		t.Errorf("error = %v, want ErrInvalid: listed twice", err)
	}
}

func TestStandingOrdersRefusalsSayInvalidStandingOrders(t *testing.T) {
	r := newSO(t)
	code, body := r.do("PUT", "/api/fleets/work/standing-orders", map[string]any{"checks": numbered(21)})
	msg, _ := body["error"].(string)
	if code != 400 || !strings.HasPrefix(msg, "invalid standing orders: ") {
		t.Errorf("PUT = %d %q, want 400 \"invalid standing orders: …\"", code, msg)
	}
	long := []string{runes(301, "x")}
	if _, err := r.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &long}); !errors.Is(err, routines.ErrInvalid) {
		t.Errorf("error = %v, want errors.Is ErrInvalid to stay true", err)
	}
}

func TestStandingOrderDraftWithNoLimitsAnswersNullLimits(t *testing.T) {
	r := newSO(t)
	r.answers(map[string]any{"limits": map[string]any{}, "checks": []any{}, "guidance": ""})
	_, raw := r.do("POST", "/api/fleets/work/standing-orders/draft", map[string]any{"prose": draftProse})
	if d, _ := raw["draft"].(map[string]any); d["limits"] != nil {
		t.Errorf("draft.limits = %v, want null when the draft changes no Limit", d["limits"])
	}
}

func TestCopyRefusedOnMembershipNeverTouchesTheTargetsOrdersFile(t *testing.T) {
	r := newSO(t)
	r.cfg.SetRepoLookup(func(fleet string) []string {
		if fleet == "work" {
			return []string{"acme/widget"}
		}
		return nil
	})
	r.put("work", map[string]any{"checks": []string{"new check"}})
	r.setLimits("work", config.FleetFields{Limits: map[string]json.RawMessage{"repo_allowlist": json.RawMessage(`["acme/widget"]`)}})
	r.put("play", map[string]any{"checks": []string{"old check"}})
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(r.file("play"), past, past); err != nil {
		t.Fatal(err)
	}
	if code, body := r.do("POST", "/api/fleets/work/standing-orders/copy", `{"to":"play"}`); code != 400 {
		t.Fatalf("copy = %d %v, want 400", code, body)
	}
	if fi, err := os.Stat(r.file("play")); err != nil || !fi.ModTime().Equal(past) {
		t.Errorf("target's orders file after a refused copy: %v, %v; want it untouched (a preview refuses before any write)", fi, err)
	}
}

func TestOrdersPromptBoundsComeFromTheLimitsConstants(t *testing.T) {
	r := newSO(t)
	r.answers(map[string]any{"limits": map[string]any{}, "checks": []any{}, "guidance": ""})
	r.draft(draftProse)
	prompt := r.called()[0].Prompt
	b := config.Bounds
	for _, want := range []string{
		fmt.Sprintf("`launches_per_hour` (1-%d)", b.LaunchRate),
		fmt.Sprintf("`launches_per_sender_per_day` (1-%d)", b.LaunchRate),
		fmt.Sprintf("`concurrent` (1-%d)", b.Concurrent),
		fmt.Sprintf("from 1 to %d", b.WallClockMinutes),
		fmt.Sprintf("from 0 to %d", b.SpendUSD),
		fmt.Sprintf("\"events\": 1-%d", b.ThresholdEvents),
		fmt.Sprintf("\"days\": 1-%d", b.ThresholdDays),
		fmt.Sprintf("from 1 to %d", b.QuotaHoldPercent),
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the drafter's prompt lacks %q", want)
		}
	}
	if strings.Contains(prompt, "{{") {
		t.Error("the prompt still holds a placeholder")
	}
	if !strings.Contains(prompt, "never dropped") {
		t.Error("the prompt doesn't say that prose no Limit can express becomes a Check or Guidance")
	}
}
