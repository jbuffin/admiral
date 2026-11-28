package config

import "testing"

const migrationFleets = `"fleets":{"work":{"name":"Work","claude":{}},"home":{"name":"Home","claude":{}}}`

// The fleet a legacy source and its autopilot entries move to is the default fleet, not the first.
func TestMigrationUsesTheDefaultFleet(t *testing.T) {
	const ap = `"autopilot":{"orders":{"enabled":true,"fleet":"work","harness":"claude","skill":{"kind":"none"}}}`
	cases := []struct {
		name, key, want string
	}{
		{"a picked fleet", `"default_fleet":"home",`, "home"},
		{"a pick that is gone", `"default_fleet":"gone",`, "work"},
		{"no pick", ``, "work"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, p := planOut(t, `{"version":1,`+c.key+migrationFleets+`,"sources":{"a":{"plugin":"freeform",`+ap+`},"b":{"plugin":"jira",`+ap+`}}}`)
			for _, e := range p.entries {
				if e.Fleet != c.want {
					t.Errorf("entry %s fleet = %q, want %q", e.Key, e.Fleet, c.want)
				}
			}
			if len(p.moved) != 1 || p.moved[0].Fleet != c.want {
				t.Errorf("moved = %+v, want source b moved to %q", p.moved, c.want)
			}
			if got := string(out["default_fleet"]); c.key != "" && got == "" {
				t.Error("the migration dropped default_fleet")
			}
		})
	}
}
