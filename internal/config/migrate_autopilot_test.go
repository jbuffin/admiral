package config

import "testing"

// #261: the engine's config keys migrate away. Each old autopilot part alone
// is enough to need a migration, whatever else the file holds.
func TestNeedsMigrationForTheEnginesConfigKeys(t *testing.T) {
	const fleets = `"fleets":{"work":{"name":"Work","claude":{}}}`
	const entry = `{"enabled":true,"fleet":"work","harness":"claude","skill":{"kind":"none"}}`
	cases := []struct {
		name, file string
		want       bool
	}{
		{"autopilot.max_ships, sources already in fleets",
			`{"version":1,` + fleets + `,"sources":{"gh":{"plugin":"github","fleet":"work"}},"autopilot":{"max_ships":4}}`, true},
		{"autopilot.max_ships and no sources key",
			`{"version":1,` + fleets + `,"autopilot":{"max_ships":4}}`, true},
		{"autopilot.resume_source alone",
			`{"version":1,` + fleets + `,"sources":{"gh":{"plugin":"github","fleet":"work"}},"autopilot":{"resume_source":{"source":"gh","panel":"p"}}}`, true},
		{"a panel entry, sources already in fleets",
			`{"version":1,` + fleets + `,"sources":{"gh":{"plugin":"github","fleet":"work","autopilot":{"prs":` + entry + `}}}}`, true},
		{"top-level max_ships and no autopilot anywhere",
			`{"version":1,` + fleets + `,"max_ships":4,"sources":{"gh":{"plugin":"github","fleet":"work"}}}`, false},
		{"nothing old",
			`{"version":1,` + fleets + `,"sources":{"gh":{"plugin":"github","fleet":"work"}}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NeedsMigration([]byte(c.file)); got != c.want {
				t.Errorf("NeedsMigration = %v, want %v for %s", got, c.want, c.file)
			}
		})
	}
}
