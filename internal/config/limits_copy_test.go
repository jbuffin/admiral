package config_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/standing"
)

func rawJSON(s string) json.RawMessage { return json.RawMessage(s) }

func TestPreviewLimitsLayersAPatchOverTheStoredLimitsWithoutSaving(t *testing.T) {
	_, dir := openView(t, limitsFleet("", `{"wall_clock_minutes":30}`))
	before := mustRead(t, dir+"/config.json")
	p, err := config.PreviewLimits(dir, "work", config.FleetFields{Limits: map[string]json.RawMessage{"quota_hold_percent": rawJSON(`75`)}})
	if err != nil {
		t.Fatalf("PreviewLimits: %v", err)
	}
	if p.Limits.WallClockMinutes != 30 || p.Limits.QuotaHoldPercent != 75 || p.Preset != standing.Work {
		t.Errorf("preview wall clock %d, quota %d, preset %q; want 30, 75 and work", p.Limits.WallClockMinutes, p.Limits.QuotaHoldPercent, p.Preset)
	}
	if len(p.Sentences) == 0 {
		t.Error("preview has no sentences")
	}
	if after := mustRead(t, dir+"/config.json"); string(after) != string(before) {
		t.Error("PreviewLimits wrote the config file")
	}
}

func TestPreviewLimitsNullResetsALimitToThePreset(t *testing.T) {
	_, dir := openView(t, limitsFleet("home", `{"wall_clock_minutes":30}`))
	p, err := config.PreviewLimits(dir, "work", config.FleetFields{Limits: map[string]json.RawMessage{"wall_clock_minutes": rawJSON(`null`)}})
	if err != nil {
		t.Fatalf("PreviewLimits: %v", err)
	}
	if p.Limits.WallClockMinutes != 60 {
		t.Errorf("wall clock after a null reset = %d, want the Home preset's 60", p.Limits.WallClockMinutes)
	}
}

func TestPreviewLimitsSwitchesThePresetAndKeepsStoredQuietHours(t *testing.T) {
	_, dir := openView(t, limitsFleet("", `{"quiet_hours":[{"start":"22:00","end":"06:00","weekdays":["fri"]}]}`))
	home := "home"
	p, err := config.PreviewLimits(dir, "work", config.FleetFields{Preset: &home})
	if err != nil {
		t.Fatalf("PreviewLimits: %v", err)
	}
	if p.Preset != standing.Home || len(p.Limits.QuietHours) != 1 {
		t.Errorf("preset %q, %d quiet windows; want home and the stored window kept", p.Preset, len(p.Limits.QuietHours))
	}
}

func TestPreviewLimitsRefusesWhatSaveRefusesNamingThePath(t *testing.T) {
	_, dir := openView(t, limitsFleet("", ""))
	_, err := config.PreviewLimits(dir, "work", config.FleetFields{Limits: map[string]json.RawMessage{"quota_hold_percent": rawJSON(`101`)}})
	if err == nil || !strings.HasPrefix(err.Error(), "limits.quota_hold_percent: ") {
		t.Errorf("error = %v, want one of the form \"limits.quota_hold_percent: <message>\"", err)
	}
}

func TestPreviewLimitsForAnUnknownFleetIsErrNoFleet(t *testing.T) {
	_, dir := openView(t, limitsFleet("", ""))
	_, err := config.PreviewLimits(dir, "nowhere", config.FleetFields{})
	if err == nil || !strings.Contains(err.Error(), config.ErrNoFleet.Error()) {
		t.Errorf("error = %v, want ErrNoFleet", err)
	}
}

const copyFleets = `"work":{"name":"Work","claude":{},"limits":{"quiet_hours":[{"start":"22:00","end":"06:00","weekdays":["fri"]}],"rates":{"concurrent":9}}},` +
	`"home":{"name":"Home","preset":"home","claude":{},"limits":{"wall_clock_minutes":45,"ceilings":{"t2":{"level":"A3","restricted":"investigate"}}}}`

func TestCopyLimitsMakesTheTargetsEffectiveLimitsTheSourcesAcrossAPresetSwitch(t *testing.T) {
	m, _ := openView(t, withFleets(copyFleets))
	src := fleetOf(m.View(), "home")
	old := fleetOf(m.View(), "work")
	before, after, err := m.CopyLimits("home", "work")
	if err != nil {
		t.Fatalf("CopyLimits: %v", err)
	}
	got := fleetOf(m.View(), "work")
	if !reflect.DeepEqual(got.Limits, src.Limits) || got.Preset != src.Preset {
		t.Errorf("target limits %+v preset %q, want the source's %+v preset %q (stored quiet hours and rates must not survive)", got.Limits, got.Preset, src.Limits, src.Preset)
	}
	if !reflect.DeepEqual(before, old.Limits) || !reflect.DeepEqual(after, src.Limits) {
		t.Errorf("returned before/after do not match the target's old and the source's Limits")
	}
}

func TestCopyLimitsOnTheSamePresetClearsAnExtraStoredKeyOfTheTarget(t *testing.T) {
	m, _ := openView(t, withFleets(`"a":{"name":"A","claude":{},"limits":{"wall_clock_minutes":45}},"b":{"name":"B","claude":{},"limits":{"rates":{"concurrent":9},"quota_hold_percent":70}}`))
	if _, _, err := m.CopyLimits("a", "b"); err != nil {
		t.Fatalf("CopyLimits: %v", err)
	}
	if got, want := fleetOf(m.View(), "b").Limits, fleetOf(m.View(), "a").Limits; !reflect.DeepEqual(got, want) {
		t.Errorf("target limits %+v, want the source's %+v", got, want)
	}
}

func TestCopyLimitsRefusesATargetThatLacksARepoTheSourceAllowsAndWritesNothing(t *testing.T) {
	m, dir := openView(t, withFleets(`"a":{"name":"A","claude":{},"limits":{"repo_allowlist":["acme/widget"]}},"b":{"name":"B","claude":{}}`))
	m.SetRepoLookup(func(fleet string) []string {
		if fleet == "a" {
			return []string{"acme/widget"}
		}
		return nil
	})
	before := mustRead(t, dir+"/config.json")
	if _, _, err := m.CopyLimits("a", "b"); err == nil || !strings.Contains(err.Error(), "repo_allowlist") {
		t.Fatalf("error = %v, want one naming repo_allowlist", err)
	}
	if after := mustRead(t, dir+"/config.json"); string(after) != string(before) {
		t.Error("a refused copy changed config.json")
	}
}

func TestCopyLimitsNamesAMissingFleet(t *testing.T) {
	m, _ := openView(t, withFleets(copyFleets))
	for _, c := range [][2]string{{"home", "nowhere"}, {"nowhere", "work"}} {
		if _, _, err := m.CopyLimits(c[0], c[1]); err == nil || !strings.Contains(err.Error(), config.ErrNoFleet.Error()) {
			t.Errorf("CopyLimits(%s, %s) error = %v, want ErrNoFleet", c[0], c[1], err)
		}
	}
}
