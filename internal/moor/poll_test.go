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
	want := map[string]any{"url": "u", "state": "OPEN", "review": "APPROVED", "checks": "SUCCESS", "mergeable": "MERGEABLE", "conflicts": false}
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
