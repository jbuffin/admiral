package config_test

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/standing"
)

func limitsFleet(preset, limits string) string {
	body := `"name":"Work"`
	if preset != "" {
		body += `,"preset":"` + preset + `"`
	}
	if limits != "" {
		body += `,"limits":` + limits
	}
	return withFleets(`"work":{` + body + `}`)
}

const fullLimits = `{
  "ceilings": {
    "t1": {"level": "A4", "asks_first": true},
    "t2": {"level": "A3", "restricted": "draft_fix"},
    "t3": {"level": "A1"},
    "t4": {"level": "A3", "restricted": "investigate", "threshold": {"events": 8, "days": 7, "level": "A3", "restricted": "draft_fix"}},
    "fleet": {"level": "A4"}
  },
  "sender_allowlist": ["alice@acme.test"],
  "rates": {"launches_per_hour": 5, "launches_per_sender_per_day": 4, "concurrent": 3},
  "quiet_hours": [{"start": "22:00", "end": "06:00", "weekdays": ["fri", "sat"]}],
  "repo_allowlist": ["acme/widget"],
  "wall_clock_minutes": 45,
  "spend": {"lookout_call_usd": 0.5, "daily_worker_usd": 12.25, "reviewer_call_usd": 0.15},
  "quota_hold_percent": 80,
  "write_deny": [".github/workflows/**"],
  "skip": {
    "labels": [{"value": "wontfix"}, {"value": "chore", "routine": "triage"}],
    "senders": [{"value": "bot@ci.test", "routine": "triage"}],
    "dedupe_keys": [{"value": "sentry:123"}]
  }
}`

func TestLimitsParseEveryCatalogField(t *testing.T) {
	cfg, err := config.Parse([]byte(limitsFleet("home", fullLimits)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	f := cfg.Fleets[0]
	if f.Preset != standing.Home {
		t.Errorf("Preset = %q, want home", f.Preset)
	}
	want := standing.Limits{
		Ceilings: standing.Ceilings{
			T1:    standing.Ceiling{Level: standing.A4, AsksFirst: true},
			T2:    standing.Ceiling{Level: standing.A3, Restricted: standing.DraftFix},
			T3:    standing.Ceiling{Level: standing.A1},
			T4:    standing.Ceiling{Level: standing.A3, Restricted: standing.Investigate, Threshold: &standing.Threshold{Events: 8, Days: 7, Level: standing.A3, Restricted: standing.DraftFix}},
			Fleet: standing.Ceiling{Level: standing.A4},
		},
		SenderAllowlist:  []string{"alice@acme.test"},
		Rates:            standing.Rates{LaunchesPerHour: 5, LaunchesPerSenderPerDay: 4, Concurrent: 3},
		QuietHours:       []standing.Window{{Start: "22:00", End: "06:00", Weekdays: []string{"fri", "sat"}}},
		RepoAllowlist:    []string{"acme/widget"},
		WallClockMinutes: 45,
		Spend:            standing.Spend{LookoutCallUSD: 0.5, DailyWorkerUSD: 12.25, ReviewerCallUSD: 0.15},
		QuotaHoldPercent: 80,
		WriteDeny:        []string{".github/workflows/**"},
		Skip: standing.Skip{
			Labels:     []standing.Scoped{{Value: "wontfix"}, {Value: "chore", Routine: "triage"}},
			Senders:    []standing.Scoped{{Value: "bot@ci.test", Routine: "triage"}},
			DedupeKeys: []standing.Scoped{{Value: "sentry:123"}},
		},
	}
	if !reflect.DeepEqual(f.Limits, want) {
		t.Errorf("Limits =\n%+v\nwant\n%+v", f.Limits, want)
	}
}

func TestAFleetWithNoLimitsReadsAsItsPreset(t *testing.T) {
	for _, c := range []struct {
		name, preset string
		want         standing.Preset
		t3           standing.Level
		perHour      int
		t4           standing.Level
		hasThreshold bool
	}{
		{"no preset reads as Work", "", standing.Work, standing.A0, 2, standing.A3, false},
		{"Work", "work", standing.Work, standing.A0, 2, standing.A3, false},
		{"Home", "home", standing.Home, standing.A1, 4, standing.A3, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := config.Parse([]byte(limitsFleet(c.preset, "")))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			f := cfg.Fleets[0]
			if f.Preset != c.want || f.Limits.Ceilings.T3.Level != c.t3 || f.Limits.Rates.LaunchesPerHour != c.perHour ||
				f.Limits.Ceilings.T4.Level != c.t4 || (f.Limits.Ceilings.T4.Threshold != nil) != c.hasThreshold {
				t.Errorf("preset %q, T3 %v, per hour %d, T4 %v, threshold %v; want %q, %v, %d, %v, %v",
					f.Preset, f.Limits.Ceilings.T3.Level, f.Limits.Rates.LaunchesPerHour, f.Limits.Ceilings.T4.Level, f.Limits.Ceilings.T4.Threshold != nil,
					c.want, c.t3, c.perHour, c.t4, c.hasThreshold)
			}
		})
	}
}

func TestLimitsGivenInTheFileOverrideOnlyWhatTheyName(t *testing.T) {
	cfg, err := config.Parse([]byte(limitsFleet("work", `{"rates":{"concurrent":5},"wall_clock_minutes":30}`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	l := cfg.Fleets[0].Limits
	if l.Rates.Concurrent != 5 || l.WallClockMinutes != 30 {
		t.Errorf("concurrent %d, wall clock %d; want 5 and 30", l.Rates.Concurrent, l.WallClockMinutes)
	}
	if l.Rates.LaunchesPerHour != 2 || l.Rates.LaunchesPerSenderPerDay != 1 || l.QuotaHoldPercent != 90 {
		t.Errorf("per hour %d, per sender %d, quota %d; want the Work values 2, 1 and 90", l.Rates.LaunchesPerHour, l.Rates.LaunchesPerSenderPerDay, l.QuotaHoldPercent)
	}
}

func TestLimitsRefuseWhatIsInvalid(t *testing.T) {
	many := func(n int, format string) string {
		items := make([]string, n)
		for i := range items {
			items[i] = `"` + strings.Replace(format, "%d", strconv.Itoa(i), 1) + `"`
		}
		return `[` + strings.Join(items, ",") + `]`
	}
	for _, c := range []struct {
		name, limits, path string
	}{
		{"an unknown key in limits", `{"bogus":1}`, "fleets.work.limits.bogus"},
		{"an unknown ceiling", `{"ceilings":{"t5":{"level":"A1"}}}`, "fleets.work.limits.ceilings.t5"},
		{"an unknown key in a ceiling", `{"ceilings":{"t1":{"level":"A4","bogus":1}}}`, "fleets.work.limits.ceilings.t1.bogus"},
		{"an unknown key in rates", `{"rates":{"per_week":1}}`, "fleets.work.limits.rates.per_week"},
		{"an unknown key in spend", `{"spend":{"weekly_usd":1}}`, "fleets.work.limits.spend.weekly_usd"},
		{"an unknown key in a window", `{"quiet_hours":[{"start":"22:00","end":"06:00","weekdays":["fri"],"tz":"UTC"}]}`, "fleets.work.limits.quiet_hours[0].tz"},
		{"an unknown key in a skip filter", `{"skip":{"labels":[{"value":"x","scope":"y"}]}}`, "fleets.work.limits.skip.labels[0].scope"},
		{"limits that is not an object", `"all"`, "fleets.work.limits"},

		{"A2 is not a rung", `{"ceilings":{"t2":{"level":"A2"}}}`, "fleets.work.limits.ceilings.t2.level"},
		{"a level past A5", `{"ceilings":{"t2":{"level":"A6"}}}`, "fleets.work.limits.ceilings.t2.level"},
		{"a level that is not a level", `{"ceilings":{"t2":{"level":"high"}}}`, "fleets.work.limits.ceilings.t2.level"},
		{"T3 above A1", `{"ceilings":{"t3":{"level":"A3","restricted":"investigate"}}}`, "fleets.work.limits.ceilings.t3.level"},
		{"T3 at A4", `{"ceilings":{"t3":{"level":"A4"}}}`, "fleets.work.limits.ceilings.t3.level"},
		{"an A3 ceiling with no restricted level", `{"ceilings":{"t2":{"level":"A3"}}}`, "fleets.work.limits.ceilings.t2.restricted"},
		{"a restricted level on an A4 ceiling", `{"ceilings":{"t2":{"level":"A4","restricted":"investigate"}}}`, "fleets.work.limits.ceilings.t2.restricted"},
		{"a restricted level that is not one", `{"ceilings":{"t2":{"level":"A3","restricted":"yolo"}}}`, "fleets.work.limits.ceilings.t2.restricted"},
		{"A3 as the fleet ceiling with no restricted level", `{"ceilings":{"fleet":{"level":"A3"}}}`, "fleets.work.limits.ceilings.fleet.restricted"},
		{"asks first outside T1", `{"ceilings":{"t2":{"level":"A4","asks_first":true}}}`, "fleets.work.limits.ceilings.t2.asks_first"},
		{"a threshold outside T4", `{"ceilings":{"t2":{"level":"A1","threshold":{"events":1,"days":1,"level":"A1"}}}}`, "fleets.work.limits.ceilings.t2.threshold"},
		{"a threshold of no events", `{"ceilings":{"t4":{"level":"A3","restricted":"investigate","threshold":{"events":0,"days":14,"level":"A3","restricted":"draft_fix"}}}}`, "fleets.work.limits.ceilings.t4.threshold.events"},
		{"a threshold of no days", `{"ceilings":{"t4":{"level":"A3","restricted":"investigate","threshold":{"events":10,"days":0,"level":"A3","restricted":"draft_fix"}}}}`, "fleets.work.limits.ceilings.t4.threshold.days"},
		{"a threshold at A3 with no restricted level", `{"ceilings":{"t4":{"level":"A3","restricted":"investigate","threshold":{"events":10,"days":14,"level":"A3"}}}}`, "fleets.work.limits.ceilings.t4.threshold.restricted"},

		{"no launches an hour", `{"rates":{"launches_per_hour":0}}`, "fleets.work.limits.rates.launches_per_hour"},
		{"a negative sender rate", `{"rates":{"launches_per_sender_per_day":-1}}`, "fleets.work.limits.rates.launches_per_sender_per_day"},
		{"a fractional concurrency", `{"rates":{"concurrent":1.5}}`, "fleets.work.limits.rates.concurrent"},
		{"an absurd hourly rate", `{"rates":{"launches_per_hour":1000000000}}`, "fleets.work.limits.rates.launches_per_hour"},
		{"a rate that is a string", `{"rates":{"concurrent":"2"}}`, "fleets.work.limits.rates.concurrent"},
		{"a zero wall clock", `{"wall_clock_minutes":0}`, "fleets.work.limits.wall_clock_minutes"},
		{"a wall clock past a day", `{"wall_clock_minutes":1441}`, "fleets.work.limits.wall_clock_minutes"},
		{"a negative spend cap", `{"spend":{"daily_worker_usd":-1}}`, "fleets.work.limits.spend.daily_worker_usd"},
		{"a spend cap finer than a cent", `{"spend":{"lookout_call_usd":0.001}}`, "fleets.work.limits.spend.lookout_call_usd"},
		{"a reviewer budget finer than a cent", `{"spend":{"reviewer_call_usd":0.105}}`, "fleets.work.limits.spend.reviewer_call_usd"},
		{"a quota hold of 0", `{"quota_hold_percent":0}`, "fleets.work.limits.quota_hold_percent"},
		{"a quota hold past 100", `{"quota_hold_percent":101}`, "fleets.work.limits.quota_hold_percent"},

		{"a quiet window with a bad time", `{"quiet_hours":[{"start":"25:00","end":"06:00","weekdays":["fri"]}]}`, "fleets.work.limits.quiet_hours[0].start"},
		{"a quiet window with a time not in HH:MM", `{"quiet_hours":[{"start":"7:00","end":"08:00","weekdays":["fri"]}]}`, "fleets.work.limits.quiet_hours[0].start"},
		{"a quiet window with a bad minute", `{"quiet_hours":[{"start":"22:00","end":"06:60","weekdays":["fri"]}]}`, "fleets.work.limits.quiet_hours[0].end"},
		{"a quiet window that starts where it ends", `{"quiet_hours":[{"start":"22:00","end":"22:00","weekdays":["fri"]}]}`, "fleets.work.limits.quiet_hours[0]"},
		{"a quiet window on no weekday", `{"quiet_hours":[{"start":"22:00","end":"06:00","weekdays":[]}]}`, "fleets.work.limits.quiet_hours[0].weekdays"},
		{"a quiet window naming a weekday twice", `{"quiet_hours":[{"start":"22:00","end":"06:00","weekdays":["fri","fri"]}]}`, "fleets.work.limits.quiet_hours[0].weekdays"},
		{"a quiet window naming a day that is not one", `{"quiet_hours":[{"start":"22:00","end":"06:00","weekdays":["friday"]}]}`, "fleets.work.limits.quiet_hours[0].weekdays"},

		{"a repo that is only an owner", `{"repo_allowlist":["acme"]}`, "fleets.work.limits.repo_allowlist[0]"},
		{"a repo with three parts", `{"repo_allowlist":["acme/widget/extra"]}`, "fleets.work.limits.repo_allowlist[0]"},
		{"an empty repo", `{"repo_allowlist":["acme/widget",""]}`, "fleets.work.limits.repo_allowlist[1]"},
		{"a repo listed twice", `{"repo_allowlist":["acme/widget","acme/widget"]}`, "fleets.work.limits.repo_allowlist[1]"},
		{"an empty sender", `{"sender_allowlist":[""]}`, "fleets.work.limits.sender_allowlist[0]"},
		{"a sender listed twice", `{"sender_allowlist":["a@b.test","a@b.test"]}`, "fleets.work.limits.sender_allowlist[1]"},
		{"an empty write-deny glob", `{"write_deny":[""]}`, "fleets.work.limits.write_deny[0]"},
		{"an absolute write-deny glob", `{"write_deny":["/etc/passwd"]}`, "fleets.work.limits.write_deny[0]"},
		{"a write-deny glob leaving the repo", `{"write_deny":["../secrets/**"]}`, "fleets.work.limits.write_deny[0]"},
		{"a write-deny glob with a dot-dot inside", `{"write_deny":["a/../b"]}`, "fleets.work.limits.write_deny[0]"},
		{"an empty skip value", `{"skip":{"labels":[{"value":""}]}}`, "fleets.work.limits.skip.labels[0].value"},
		{"a skip entry listed twice", `{"skip":{"senders":[{"value":"a@b.test"},{"value":"a@b.test"}]}}`, "fleets.work.limits.skip.senders[1]"},
		{"too many senders", `{"sender_allowlist":` + many(5001, "u%d@acme.test") + `}`, "fleets.work.limits.sender_allowlist"},
		{"too many repos", `{"repo_allowlist":` + many(5001, "acme/r%d") + `}`, "fleets.work.limits.repo_allowlist"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := config.Parse([]byte(limitsFleet("", c.limits)))
			if err == nil || !strings.Contains(err.Error(), c.path) {
				t.Fatalf("Parse error = %v, want one naming %s", err, c.path)
			}
		})
	}
}

func TestPresetMustBeWorkOrHome(t *testing.T) {
	_, err := config.Parse([]byte(limitsFleet("office", "")))
	if err == nil || !strings.Contains(err.Error(), "fleets.work.preset") || !strings.Contains(err.Error(), "home") {
		t.Fatalf("Parse error = %v, want one naming fleets.work.preset and the allowed values", err)
	}
}

func TestLimitsAcceptTheEdgesOfEveryBound(t *testing.T) {
	for _, c := range []struct{ name, limits string }{
		{"wall clock of 1 minute", `{"wall_clock_minutes":1}`},
		{"wall clock of a day", `{"wall_clock_minutes":1440}`},
		{"quota hold of 1", `{"quota_hold_percent":1}`},
		{"quota hold of 100", `{"quota_hold_percent":100}`},
		{"a spend cap of nothing", `{"spend":{"daily_worker_usd":0}}`},
		{"a spend cap in cents", `{"spend":{"reviewer_call_usd":0.07}}`},
		{"a window ending at midnight", `{"quiet_hours":[{"start":"23:00","end":"00:00","weekdays":["sun"]}]}`},
		{"two windows", `{"quiet_hours":[{"start":"22:00","end":"06:00","weekdays":["fri"]},{"start":"12:00","end":"13:00","weekdays":["sun"]}]}`},
		{"a T3 ceiling of A1", `{"ceilings":{"t3":{"level":"A1"}}}`},
		{"a T3 ceiling of A0", `{"ceilings":{"t3":{"level":"A0"}}}`},
		{"a skip filter scoped to a routine", `{"skip":{"dedupe_keys":[{"value":"k","routine":"r"}]}}`},
		{"a repo allowlist", `{"repo_allowlist":["acme/widget","Acme/other-1.x"]}`},
		{"no windows", `{"quiet_hours":[]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := config.Parse([]byte(limitsFleet("", c.limits))); err != nil {
				t.Fatalf("Parse: %v", err)
			}
		})
	}
}

func TestABadLimitShowsAsTheConfigBanner(t *testing.T) {
	m, _ := openView(t, limitsFleet("", `{"ceilings":{"t3":{"level":"A3","restricted":"investigate"}}}`))
	e := m.View().Error
	if e == nil || !strings.Contains(e.Message, "fleets.work.limits.ceilings.t3.level") {
		t.Fatalf("view error = %+v, want a banner naming fleets.work.limits.ceilings.t3.level", e)
	}
}

func TestAMissingRepoInTheAllowlistDoesNotTakeTheConfigDown(t *testing.T) {
	m, _ := openView(t, limitsFleet("", `{"repo_allowlist":["acme/moved-away"]}`))
	m.SetRepoLookup(func(string) []string { return []string{"acme/widget"} })
	v := m.View()
	if v.Error != nil {
		t.Fatalf("view error = %+v, want none: a moved checkout must not fail the config", v.Error)
	}
	if got := fleetOf(v, "work").Limits.RepoAllowlist; !reflect.DeepEqual(got, []string{"acme/moved-away"}) {
		t.Errorf("repo allowlist in the view = %v, want [acme/moved-away]", got)
	}
}

func TestSaveLimitsShowsInTheViewAndTheFile(t *testing.T) {
	m, dir := openView(t, withFleets(`"work":{"name":"Work","owners":["acme"],"claude":{}},"home":{"name":"Home","claude":{}}`))
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	err := m.SetFleetFields("work", config.FleetFields{Limits: map[string]json.RawMessage{
		"wall_clock_minutes": raw(`30`),
		"ceilings":           raw(`{"t2":{"level":"A3","restricted":"investigate"}}`),
	}})
	if err != nil {
		t.Fatalf("SetFleetFields: %v", err)
	}
	v := fleetOf(m.View(), "work")
	if v.Limits.WallClockMinutes != 30 || v.Limits.Ceilings.T2.Level != standing.A3 || v.Limits.Ceilings.T2.Restricted != standing.Investigate {
		t.Errorf("view limits = wall clock %d, T2 %+v; want 30 and A3 investigate", v.Limits.WallClockMinutes, v.Limits.Ceilings.T2)
	}
	if v.Limits.Rates.LaunchesPerHour != 2 || v.Preset != "work" {
		t.Errorf("view preset %q, per hour %d; want work and the Work value 2", v.Preset, v.Limits.Rates.LaunchesPerHour)
	}
	if len(v.Sentences) == 0 {
		t.Error("view has no sentences")
	}
	if got := fleetOf(m.View(), "home").Limits.WallClockMinutes; got != 60 {
		t.Errorf("home's wall clock = %d, want its preset's 60", got)
	}
	if !reflect.DeepEqual(v.Owners, []string{"acme"}) {
		t.Errorf("owners after saving only limits = %v, want [acme]", v.Owners)
	}
	cfg, err := config.Parse(mustRead(t, dir+"/config.json"))
	if err != nil || cfg.Fleets[0].Limits.WallClockMinutes != 30 {
		t.Fatalf("the written file parses to %+v, %v; want wall clock 30", cfg.Fleets[0].Limits, err)
	}

	// A later save of one Limit leaves the other stored Limits as they are.
	if err := m.SetFleetFields("work", config.FleetFields{Limits: map[string]json.RawMessage{"quota_hold_percent": raw(`75`)}}); err != nil {
		t.Fatalf("second SetFleetFields: %v", err)
	}
	v = fleetOf(m.View(), "work")
	if v.Limits.WallClockMinutes != 30 || v.Limits.QuotaHoldPercent != 75 || v.Limits.Ceilings.T2.Restricted != standing.Investigate {
		t.Errorf("after a second save: wall clock %d, quota %d, T2 %+v; want 30, 75 and A3 investigate kept", v.Limits.WallClockMinutes, v.Limits.QuotaHoldPercent, v.Limits.Ceilings.T2)
	}
}

func TestSaveLimitsRefusesWhatParseRefusesAndWritesNothing(t *testing.T) {
	m, dir := openView(t, withFleets(`"work":{"name":"Work","claude":{}}`))
	before := string(mustRead(t, dir+"/config.json"))
	for _, c := range []struct{ name, key, value, path string }{
		{"T3 above A1", "ceilings", `{"t3":{"level":"A3","restricted":"investigate"}}`, "limits.ceilings.t3.level"},
		{"A2", "ceilings", `{"t2":{"level":"A2"}}`, "limits.ceilings.t2.level"},
		{"a wall clock of 0", "wall_clock_minutes", `0`, "limits.wall_clock_minutes"},
		{"an unknown Limit", "bogus", `1`, "limits.bogus"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := m.SetFleetFields("work", config.FleetFields{Limits: map[string]json.RawMessage{c.key: json.RawMessage(c.value)}})
			if err == nil || !strings.Contains(err.Error(), c.path) {
				t.Fatalf("SetFleetFields error = %v, want one naming %s", err, c.path)
			}
			if got := string(mustRead(t, dir+"/config.json")); got != before {
				t.Errorf("a refused save changed config.json:\n%s", got)
			}
		})
	}
}

func TestSavingTheRepoAllowlistChecksMembershipButParseDoesNot(t *testing.T) {
	m, _ := openView(t, withFleets(`"work":{"name":"Work","claude":{}}`))
	m.SetRepoLookup(func(fleet string) []string {
		if fleet != "work" {
			t.Errorf("lookup asked about fleet %q, want work", fleet)
		}
		return []string{"acme/widget"}
	})
	save := func(list string) error {
		return m.SetFleetFields("work", config.FleetFields{Limits: map[string]json.RawMessage{"repo_allowlist": json.RawMessage(list)}})
	}
	err := save(`["acme/widget","acme/nowhere"]`)
	if err == nil || !strings.Contains(err.Error(), "limits.repo_allowlist[1]") {
		t.Fatalf("saving an unknown repo: error = %v, want one naming limits.repo_allowlist[1]", err)
	}
	if got := fleetOf(m.View(), "work").Limits.RepoAllowlist; len(got) != 0 {
		t.Errorf("a refused save stored %v", got)
	}
	if err := save(`["acme/widget"]`); err != nil {
		t.Fatalf("saving a known repo: %v", err)
	}
	if got := fleetOf(m.View(), "work").Limits.RepoAllowlist; !reflect.DeepEqual(got, []string{"acme/widget"}) {
		t.Errorf("stored repo allowlist = %v, want [acme/widget]", got)
	}
}

func TestSavingAnotherLimitIsNotBlockedByAStoredRepoThatMoved(t *testing.T) {
	m, _ := openView(t, limitsFleet("", `{"repo_allowlist":["acme/moved-away"]}`))
	m.SetRepoLookup(func(string) []string { return []string{"acme/widget"} })
	err := m.SetFleetFields("work", config.FleetFields{Limits: map[string]json.RawMessage{"wall_clock_minutes": json.RawMessage(`30`)}})
	if err != nil {
		t.Fatalf("saving the wall clock: %v", err)
	}
	v := fleetOf(m.View(), "work")
	if v.Limits.WallClockMinutes != 30 || !reflect.DeepEqual(v.Limits.RepoAllowlist, []string{"acme/moved-away"}) {
		t.Errorf("wall clock %d, repo allowlist %v; want 30 and the stored entry kept", v.Limits.WallClockMinutes, v.Limits.RepoAllowlist)
	}
}

func TestSavingThePresetAloneSwitchesIt(t *testing.T) {
	m, _ := openView(t, withFleets(`"work":{"name":"Work","claude":{}}`))
	home := "home"
	if err := m.SetFleetFields("work", config.FleetFields{Preset: &home}); err != nil {
		t.Fatalf("SetFleetFields: %v", err)
	}
	v := fleetOf(m.View(), "work")
	if v.Preset != "home" || v.Limits.Rates.LaunchesPerHour != 4 || v.Limits.Ceilings.T1.AsksFirst {
		t.Errorf("preset %q, per hour %d, T1 asks first %v; want home, 4 and false", v.Preset, v.Limits.Rates.LaunchesPerHour, v.Limits.Ceilings.T1.AsksFirst)
	}
	bad := "office"
	if err := m.SetFleetFields("work", config.FleetFields{Preset: &bad}); err == nil || !strings.Contains(err.Error(), "preset") {
		t.Errorf("saving preset office: error = %v, want one naming preset", err)
	}
}

func TestSwitchingPresetClearsOverridesAndKeepsQuietHours(t *testing.T) {
	m, dir := openView(t, limitsFleet("work", `{
	  "wall_clock_minutes": 30,
	  "rates": {"concurrent": 9},
	  "quiet_hours": [{"start":"22:00","end":"06:00","weekdays":["fri"]}]
	}`))
	home := "home"
	if err := m.SetFleetFields("work", config.FleetFields{Preset: &home}); err != nil {
		t.Fatalf("SetFleetFields: %v", err)
	}
	l := fleetOf(m.View(), "work").Limits
	if l.WallClockMinutes != 60 || l.Rates.Concurrent != 3 {
		t.Errorf("after the switch wall clock %d, concurrent %d; want Home's 60 and 3, overrides cleared", l.WallClockMinutes, l.Rates.Concurrent)
	}
	if want := []standing.Window{{Start: "22:00", End: "06:00", Weekdays: []string{"fri"}}}; !reflect.DeepEqual(l.QuietHours, want) {
		t.Errorf("quiet hours after the switch = %+v, want %+v", l.QuietHours, want)
	}
	cfg, err := config.Parse(mustRead(t, dir+"/config.json"))
	if err != nil || cfg.Fleets[0].Preset != standing.Home || len(cfg.Fleets[0].Limits.QuietHours) != 1 || cfg.Fleets[0].Limits.WallClockMinutes != 60 {
		t.Fatalf("the written file parses to preset %q, %+v, %v; want home with the window and Home's wall clock", cfg.Fleets[0].Preset, cfg.Fleets[0].Limits, err)
	}
}
