package moor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestViewServesWhyGitHubWontMergeAnOpenPR(t *testing.T) {
	const url = "https://github.com/acme/widgets/pull/7"
	open := func(mod func(*Status)) Status {
		st := Status{State: "OPEN", Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE", MergeState: "CLEAN"}
		mod(&st)
		return st
	}
	for name, c := range map[string]struct {
		st   Status
		want []string
	}{
		"approved, green, behind a strict base":  {open(func(s *Status) { s.MergeState, s.StrictBase = "BEHIND", true }), []string{"behind"}},
		"behind a base that does not say strict": {open(func(s *Status) { s.MergeState = "BEHIND" }), []string{"behind"}},
		"a conflict by mergeable":                {open(func(s *Status) { s.Mergeable = "CONFLICTING"; s.MergeState = "DIRTY" }), []string{"conflicts"}},
		"a conflict by merge state alone":        {open(func(s *Status) { s.MergeState = "DIRTY" }), []string{"conflicts"}},
		"a failing check":                        {open(func(s *Status) { s.Checks, s.MergeState = "FAILURE", "BLOCKED" }), []string{"checks_failing"}},
		"an errored check":                       {open(func(s *Status) { s.Checks, s.MergeState = "ERROR", "BLOCKED" }), []string{"checks_failing"}},
		"a check still running":                  {open(func(s *Status) { s.Checks, s.MergeState = "PENDING", "BLOCKED" }), []string{"checks_pending"}},
		"a check still expected":                 {open(func(s *Status) { s.Checks, s.MergeState = "EXPECTED", "BLOCKED" }), []string{"checks_pending"}},
		"changes requested":                      {open(func(s *Status) { s.Review, s.MergeState = "CHANGES_REQUESTED", "BLOCKED" }), []string{"changes_requested"}},
		"a review still required":                {open(func(s *Status) { s.Review, s.MergeState = "REVIEW_REQUIRED", "BLOCKED" }), []string{"review_required"}},
		"GitHub still computing mergeable":       {open(func(s *Status) { s.Mergeable, s.MergeState = "UNKNOWN", "UNKNOWN" }), []string{"unknown"}},
		"GitHub still computing merge state":     {open(func(s *Status) { s.MergeState = "UNKNOWN" }), []string{"unknown"}},
		"blocked by a rule nothing else names":   {open(func(s *Status) { s.MergeState = "BLOCKED" }), []string{"held"}},
		"a draft is a draft, not held":           {open(func(s *Status) { s.Draft, s.MergeState = true, "BLOCKED" }), []string{"draft"}},
		"several reasons, in the documented order": {open(func(s *Status) {
			s.Draft, s.MergeState, s.Mergeable, s.Checks, s.Review = true, "BEHIND", "CONFLICTING", "FAILURE", "CHANGES_REQUESTED"
		}), []string{"draft", "behind", "conflicts", "checks_failing", "changes_requested"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := View(url, c.st, true)
			if !reflect.DeepEqual(got.MergeReasons, c.want) || got.Ready {
				t.Errorf("View(%+v) = reasons %v ready %v, want %v and not ready", c.st, got.MergeReasons, got.Ready, c.want)
			}
		})
	}
}

func TestViewServesNoMergeReasonsForAReadyOrNotOpenPR(t *testing.T) {
	const url = "https://github.com/acme/widgets/pull/7"
	for name, c := range map[string]struct {
		st    Status
		found bool
	}{
		"ready":     {Status{State: "OPEN", Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE", MergeState: "CLEAN"}, true},
		"merged":    {Status{State: "MERGED", Mergeable: "CONFLICTING", MergeState: "DIRTY"}, true},
		"closed":    {Status{State: "CLOSED", MergeState: "BEHIND"}, true},
		"not found": {Status{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := View(url, c.st, c.found); len(got.MergeReasons) != 0 {
				t.Errorf("View = reasons %v, want none", got.MergeReasons)
			}
		})
	}
}

func TestBehindKeepsItsMeaningForWakeDecisions(t *testing.T) {
	const url = "https://github.com/acme/widgets/pull/7"
	st := Status{State: "OPEN", Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE", MergeState: "BEHIND"}
	if got := View(url, st, true); got.Behind || !reflect.DeepEqual(got.MergeReasons, []string{"behind"}) {
		t.Errorf("View on a base with no strict rule = behind %v reasons %v, want behind false and the reason behind", got.Behind, got.MergeReasons)
	}
	st.StrictBase = true
	if got := View(url, st, true); !got.Behind {
		t.Errorf("View on a strict base = behind %v, want true", got.Behind)
	}
}

func TestMergeReasonsAreServedUnderTheirDocumentedName(t *testing.T) {
	b, err := json.Marshal(PR{URL: "u", State: "OPEN", MergeReasons: []string{"behind"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"merge_reasons":["behind"]`) {
		t.Errorf("PR JSON = %s, want merge_reasons", b)
	}
	b, _ = json.Marshal(PR{URL: "u", State: "OPEN"})
	if strings.Contains(string(b), "merge_reasons") {
		t.Errorf("PR JSON = %s, want merge_reasons omitted when empty", b)
	}
}
