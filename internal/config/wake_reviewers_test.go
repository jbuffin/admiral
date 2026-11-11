package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

func TestWakeReviewersParseAndValidate(t *testing.T) {
	fleet := func(list string) string { return withFleets(`"work":{"name":"Work","wake_reviewers":` + list + `}`) }
	for _, c := range []struct {
		name, list string
		want       []string
		bad        string // the path an error names; empty when valid
	}{
		{"none", `[]`, []string{}, ""},
		{"logins and a bot", `["alice","Bob-2","dependabot[bot]","a"]`, []string{"alice", "Bob-2", "dependabot[bot]", "a"}, ""},
		{"39 characters", `["` + strings.Repeat("a", 39) + `"]`, []string{strings.Repeat("a", 39)}, ""},
		{"40 characters", `["` + strings.Repeat("a", 40) + `"]`, nil, "fleets.work.wake_reviewers[0]"},
		{"41 characters with hyphens", `["a` + strings.Repeat("-a", 20) + `"]`, nil, "fleets.work.wake_reviewers[0]"},
		{"39 characters with hyphens and a bot suffix", `["` + strings.Repeat("a-", 19) + `a[bot]"]`, []string{strings.Repeat("a-", 19) + "a[bot]"}, ""},
		{"empty", `["alice",""]`, nil, "fleets.work.wake_reviewers[1]"},
		{"leading hyphen", `["-alice"]`, nil, "fleets.work.wake_reviewers[0]"},
		{"trailing hyphen", `["alice-"]`, nil, "fleets.work.wake_reviewers[0]"},
		{"double hyphen", `["al--ice"]`, nil, "fleets.work.wake_reviewers[0]"},
		{"underscore", `["al_ice"]`, nil, "fleets.work.wake_reviewers[0]"},
		{"space", `["al ice"]`, nil, "fleets.work.wake_reviewers[0]"},
		{"a bare [bot]", `["[bot]"]`, nil, "fleets.work.wake_reviewers[0]"},
		{"not a list", `"alice"`, nil, "fleets.work.wake_reviewers"},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := config.Parse([]byte(fleet(c.list)))
			if c.bad != "" {
				if err == nil || !strings.Contains(err.Error(), c.bad) {
					t.Fatalf("Parse error = %v, want one naming %s", err, c.bad)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := cfg.Fleets[0].WakeReviewers; !reflect.DeepEqual(append([]string{}, got...), c.want) {
				t.Errorf("WakeReviewers = %v, want %v", got, c.want)
			}
		})
	}
}

func TestWakeReviewersSaveAndViewKeepTheRestOfTheFleet(t *testing.T) {
	m, dir := openView(t, withFleets(`"work":{"name":"Work","owners":["acme"],"claude":{}},"home":{"name":"Home","claude":{}}`))
	if got := fleetOf(m.View(), "work").WakeReviewers; got == nil || len(got) != 0 {
		t.Errorf("view of a fleet with none = %#v, want an empty list, not null", got)
	}

	list := []string{"alice", "ci-bot[bot]"}
	if err := m.SetFleetFields("work", config.FleetFields{WakeReviewers: &list}); err != nil {
		t.Fatalf("SetFleetFields: %v", err)
	}
	v := m.View()
	if got := fleetOf(v, "work").WakeReviewers; !reflect.DeepEqual(got, list) {
		t.Errorf("work's wake_reviewers in the view = %v, want %v", got, list)
	}
	if got := fleetOf(v, "work").Owners; !reflect.DeepEqual(got, []string{"acme"}) {
		t.Errorf("owners after saving only wake_reviewers = %v, want [acme]", got)
	}
	if got := fleetOf(v, "home").WakeReviewers; len(got) != 0 {
		t.Errorf("home's wake_reviewers = %v, want none", got)
	}
	cfg, err := config.Parse(mustRead(t, dir+"/config.json"))
	if err != nil || !reflect.DeepEqual(cfg.Fleets[0].WakeReviewers, list) {
		t.Fatalf("the written file parses to %v, %v; want %v", cfg.Fleets[0].WakeReviewers, err, list)
	}

	empty := []string{}
	if err := m.SetFleetFields("work", config.FleetFields{WakeReviewers: &empty}); err != nil {
		t.Fatalf("SetFleetFields to empty: %v", err)
	}
	if got := fleetOf(m.View(), "work").WakeReviewers; len(got) != 0 {
		t.Errorf("after clearing = %v, want none", got)
	}
}
