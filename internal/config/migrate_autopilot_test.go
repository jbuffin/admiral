package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// Each old autopilot part alone is enough to need a migration, whatever else the file holds.
func TestNeedsMigrationForTheEnginesConfigKeys(t *testing.T) {
	const fleets = `"fleets":{"work":{"name":"Work","claude":{}}}`
	const entry = `{"enabled":true,"fleet":"work","harness":"claude","skill":{"kind":"none"}}`
	cases := []struct {
		name, file string
		want       bool
	}{
		{"autopilot.max_ships, sources already in fleets",
			`{"version":1,` + fleets + `,"sources":{"gh":{"plugin":"github","fleet":"work","settings":{"views":[]}}},"autopilot":{"max_ships":4}}`, true},
		{"autopilot.max_ships and no sources key",
			`{"version":1,` + fleets + `,"autopilot":{"max_ships":4}}`, true},
		{"autopilot.resume_source alone",
			`{"version":1,` + fleets + `,"sources":{"gh":{"plugin":"github","fleet":"work","settings":{"views":[]}}},"autopilot":{"resume_source":{"source":"gh","panel":"p"}}}`, true},
		{"a panel entry, sources already in fleets",
			`{"version":1,` + fleets + `,"sources":{"gh":{"plugin":"github","fleet":"work","settings":{"views":[]},"autopilot":{"prs":` + entry + `}}}}`, true},
		{"top-level max_ships and no autopilot anywhere",
			`{"version":1,` + fleets + `,"max_ships":4,"sources":{"gh":{"plugin":"github","fleet":"work","settings":{"views":[]}}}}`, false},
		{"nothing old",
			`{"version":1,` + fleets + `,"sources":{"gh":{"plugin":"github","fleet":"work","settings":{"views":[]}}}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NeedsMigration([]byte(c.file)); got != c.want {
				t.Errorf("NeedsMigration = %v, want %v for %s", got, c.want, c.file)
			}
		})
	}
}

func planOut(t *testing.T, file string) (map[string]json.RawMessage, *migrationPlan) {
	t.Helper()
	p := planMigration([]byte(file))
	if p == nil {
		t.Fatalf("planMigration = nil for %s", file)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(p.out, &out); err != nil {
		t.Fatal(err)
	}
	return out, p
}

// An existing top-level max_ships wins over autopilot.max_ships, and the
// autopilot object goes either way.
func TestMigrationKeepsAnExistingTopLevelMaxShips(t *testing.T) {
	out, _ := planOut(t, `{"version":1,"fleets":{"work":{"name":"Work","claude":{}}},"max_ships":5,"autopilot":{"max_ships":2}}`)
	if string(out["max_ships"]) != "5" {
		t.Errorf("max_ships = %s, want 5", out["max_ships"])
	}
	if _, ok := out["autopilot"]; ok {
		t.Error("the autopilot object is still there")
	}
}

// An autopilot value that is not an object is dropped, and does not stop the
// fleet migration.
func TestMigrationDropsANonObjectAutopilot(t *testing.T) {
	out, _ := planOut(t, `{"version":1,"fleets":{"work":{"name":"Work","claude":{}}},"sources":{"gh":{"plugin":"github"}},"autopilot":null}`)
	if _, ok := out["autopilot"]; ok {
		t.Error("the autopilot value is still there")
	}
	if !strings.Contains(string(out["sources"]), `"fleet"`) {
		t.Errorf("sources = %s, want gh moved to a fleet", out["sources"])
	}
}

// The enabled entries of a source the migration removes are listed too.
func TestMigrationListsEnabledEntriesOfADroppedSource(t *testing.T) {
	const entry = `{"enabled":true,"fleet":"work","harness":"claude","skill":{"kind":"none"}}`
	_, p := planOut(t, `{"version":1,"fleets":{"work":{"name":"Work","claude":{}}},"sources":{"notes":{"plugin":"freeform","name":"Notes","autopilot":{"orders":`+entry+`}}}}`)
	m := &Migration{Removed: p.removed, Entries: p.entries}
	_, body := m.Notice()
	if !strings.Contains(body, "Notes") || !strings.Contains(body, "orders") {
		t.Errorf("notice body lacks the dropped source's enabled entry:\n%s", body)
	}
}

// The note no longer says routines are still to come; they have landed.
func TestMigrationNoticeNoLongerSaysRoutinesAreComing(t *testing.T) {
	const entry = `{"enabled":true,"fleet":"work","harness":"claude","skill":{"kind":"none"}}`
	_, p := planOut(t, `{"version":1,"fleets":{"work":{"name":"Work","claude":{}}},"sources":{"notes":{"plugin":"freeform","name":"Notes","autopilot":{"orders":`+entry+`}}}}`)
	m := &Migration{Removed: p.removed, Entries: p.entries}
	_, body := m.Notice()
	if strings.Contains(body, "once scheduled routines land") || !strings.Contains(body, "Notes") || !strings.Contains(body, "Automate") {
		t.Errorf("notice body should name the entry and point at Automate…, not say routines are coming:\n%s", body)
	}
}
