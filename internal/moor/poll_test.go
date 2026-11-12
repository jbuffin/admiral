package moor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRemoveDeletesTheRecordAndToleratesASecondRemove(t *testing.T) {
	home := t.TempDir()
	for _, n := range []string{"Nelson", "Hood"} {
		if err := Write(home, Record{Name: n, Session: n}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Remove(home, "Nelson"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := Remove(home, "Nelson"); err != nil {
		t.Errorf("a second Remove of the same record = %v, want nil: two endings race", err)
	}
	got, _ := List(home)
	if len(got) != 1 || got[0].Name != "Hood" {
		t.Errorf("records after Remove = %v, want only Hood", got)
	}
	if err := Remove(home, "../escape"); err == nil {
		t.Errorf("Remove accepted a name that is a path")
	}
	if _, err := os.Stat(filepath.Join(home, "moored", "Hood.json")); err != nil {
		t.Errorf("Remove touched another record: %v", err)
	}
}

func TestViewServesTheChipsOfAnOpenPR(t *testing.T) {
	const url = "https://github.com/acme/widgets/pull/7"
	for name, c := range map[string]struct {
		st    Status
		found bool
		want  PR
	}{
		"every chip": {Status{State: "OPEN", Review: "CHANGES_REQUESTED", Checks: "FAILURE", Mergeable: "CONFLICTING"}, true,
			PR{URL: url, State: "OPEN", Review: "CHANGES_REQUESTED", Checks: "FAILURE", Mergeable: "CONFLICTING", Conflicts: true}},
		"no review and no checks": {Status{State: "OPEN", Mergeable: "MERGEABLE"}, true,
			PR{URL: url, State: "OPEN", Mergeable: "MERGEABLE"}},
		"mergeable unknown is no conflict": {Status{State: "OPEN", Review: "APPROVED", Checks: "PENDING", Mergeable: "UNKNOWN"}, true,
			PR{URL: url, State: "OPEN", Review: "APPROVED", Checks: "PENDING", Mergeable: "UNKNOWN"}},
		"a merged PR serves no conflicts": {Status{State: "MERGED", Mergeable: "CONFLICTING"}, true,
			PR{URL: url, State: "MERGED", Mergeable: "CONFLICTING"}},
		"a closed PR serves no conflicts": {Status{State: "CLOSED", Mergeable: "CONFLICTING"}, true,
			PR{URL: url, State: "CLOSED", Mergeable: "CONFLICTING"}},
		"NOT_FOUND counts as closed": {Status{}, false, PR{URL: url, State: "CLOSED"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := View(url, c.st, c.found)
			if got.Conflicts != c.want.Conflicts || got.State != c.want.State || got.URL != url {
				t.Errorf("View = %+v, want state %s, conflicts %v", got, c.want.State, c.want.Conflicts)
			}
			if c.want.State == "OPEN" && !reflect.DeepEqual(got, c.want) {
				t.Errorf("View = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestPRServesItsChipsUnderTheNamesTheAPIDocuments(t *testing.T) {
	b, err := json.Marshal(PR{URL: "u", State: "OPEN", Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	want := map[string]any{"url": "u", "state": "OPEN", "review": "APPROVED", "checks": "SUCCESS", "mergeable": "MERGEABLE", "conflicts": false, "ready": false}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("a PR as JSON = %v, want %v", m, want)
	}
}

func TestEndReasonSaysWhyEveryPRIsDone(t *testing.T) {
	pr := func(states ...string) []PR {
		var out []PR
		for _, s := range states {
			out = append(out, PR{State: s})
		}
		return out
	}
	for name, c := range map[string]struct {
		prs    []PR
		reason string
		ended  bool
	}{
		"all merged":                 {pr("MERGED", "MERGED"), "merged", true},
		"all closed":                 {pr("CLOSED", "CLOSED"), "closed", true},
		"one merged and one closed":  {pr("MERGED", "CLOSED"), "prs_done", true},
		"one open keeps it moored":   {pr("MERGED", "OPEN"), "", false},
		"all open keeps it moored":   {pr("OPEN"), "", false},
		"a single merged PR":         {pr("MERGED"), "merged", true},
		"merged and closed and open": {pr("MERGED", "CLOSED", "OPEN"), "", false},
	} {
		t.Run(name, func(t *testing.T) {
			reason, ended := EndReason(c.prs)
			if reason != c.reason || ended != c.ended {
				t.Errorf("EndReason = %q, %v; want %q, %v", reason, ended, c.reason, c.ended)
			}
		})
	}
}

func TestViewReadsAPRReadyOnlyWhenEveryConditionHolds(t *testing.T) {
	const url = "https://github.com/acme/widgets/pull/7"
	ready := Status{State: "OPEN", Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE", MergeState: "CLEAN"}
	if !View(url, ready, true).Ready {
		t.Fatalf("an open, approved, green, mergeable, clean PR = %+v, want ready", View(url, ready, true))
	}
	for name, mod := range map[string]func(*Status){
		"merged":              func(s *Status) { s.State = "MERGED" },
		"closed":              func(s *Status) { s.State = "CLOSED" },
		"no review decision":  func(s *Status) { s.Review = "" },
		"review required":     func(s *Status) { s.Review = "REVIEW_REQUIRED" },
		"changes requested":   func(s *Status) { s.Review = "CHANGES_REQUESTED" },
		"no checks":           func(s *Status) { s.Checks = "" },
		"checks pending":      func(s *Status) { s.Checks = "PENDING" },
		"checks failing":      func(s *Status) { s.Checks = "FAILURE" },
		"mergeable unknown":   func(s *Status) { s.Mergeable = "UNKNOWN" },
		"conflicting":         func(s *Status) { s.Mergeable = "CONFLICTING" },
		"merge state blocked": func(s *Status) { s.MergeState = "BLOCKED" },
		"merge state behind":  func(s *Status) { s.MergeState = "BEHIND" },
		"merge state dirty":   func(s *Status) { s.MergeState = "DIRTY" },
		"merge state draft":   func(s *Status) { s.MergeState = "DRAFT" },
	} {
		t.Run(name, func(t *testing.T) {
			st := ready
			mod(&st)
			if got := View(url, st, true); got.Ready {
				t.Errorf("View(%+v) = %+v, want not ready", st, got)
			}
		})
	}
	if View(url, Status{}, false).Ready {
		t.Errorf("a PR GitHub answered NOT_FOUND for reads ready")
	}
}

func TestStackReadyNamesTheHighestLayerWhoseLowerLayersAreAllReady(t *testing.T) {
	pr := func(n string, ready bool) PR {
		return PR{URL: "https://github.com/acme/widgets/pull/" + n, State: "OPEN", Ready: ready}
	}
	one, two, three := pr("1", true), pr("2", true), pr("3", true)
	for name, c := range map[string]struct {
		prs   []PR
		ready bool
		upTo  string
	}{
		"every layer ready":       {[]PR{one, two, three}, true, three.URL},
		"a single ready PR":       {[]PR{one}, true, one.URL},
		"the top layer not ready": {[]PR{one, two, pr("3", false)}, false, two.URL},
		"the middle layer not":    {[]PR{one, pr("2", false), three}, false, one.URL},
		"the lowest layer not":    {[]PR{pr("1", false), two, three}, false, ""},
		"a single PR not ready":   {[]PR{pr("1", false)}, false, ""},
		"no PRs":                  {nil, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			ready, upTo := StackReady(c.prs)
			if ready != c.ready || upTo != c.upTo {
				t.Errorf("StackReady = (%v, %q), want (%v, %q)", ready, upTo, c.ready, c.upTo)
			}
		})
	}
}
