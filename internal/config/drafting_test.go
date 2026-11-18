package config_test

import (
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

func TestADraftingSkillOnAFleetLoads(t *testing.T) {
	cfg, err := config.Parse([]byte(withFleets(`"work":{"name":"Work","drafting":{"skill":"wayfinder"}}`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.Fleets[0].DraftingSkill; got != "wayfinder" {
		t.Errorf("DraftingSkill = %q, want wayfinder", got)
	}
}

func TestAFleetWithoutDraftingHasNoSkill(t *testing.T) {
	cfg, err := config.Parse([]byte(withFleets(`"work":{"name":"Work"},"empty":{"name":"Empty","drafting":{}}`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, f := range cfg.Fleets {
		if f.DraftingSkill != "" {
			t.Errorf("%s DraftingSkill = %q, want none", f.Key, f.DraftingSkill)
		}
	}
}

func TestAnUnknownKeyInDraftingIsRefused(t *testing.T) {
	_, err := config.Parse([]byte(withFleets(`"work":{"name":"Work","drafting":{"skill":"wayfinder","color":"red"}}`)))
	if err == nil || !strings.Contains(err.Error(), "fleets.work.drafting.color") {
		t.Fatalf("Parse error = %v, want one naming fleets.work.drafting.color", err)
	}
}

func TestSavingTheDraftingSkillWritesItAndLeavesTheRestOfTheFleet(t *testing.T) {
	m, dir := openView(t, withFleets(`"work":{"name":"Work","owners":["acme"],"claude":{}}`))
	skill := "wayfinder"
	if err := m.SetFleetFields("work", config.FleetFields{DraftingSkill: &skill}); err != nil {
		t.Fatalf("SetFleetFields: %v", err)
	}
	if got := m.Current().Fleets[0].DraftingSkill; got != "wayfinder" {
		t.Errorf("DraftingSkill after save = %q, want wayfinder", got)
	}
	file := string(mustRead(t, dir+"/config.json"))
	if !strings.Contains(file, `"drafting"`) || !strings.Contains(file, `"skill": "wayfinder"`) && !strings.Contains(file, `"skill":"wayfinder"`) {
		t.Errorf("config.json = %s, want drafting.skill wayfinder", file)
	}
	if got := m.Current().Fleets[0].Owners; len(got) != 1 || got[0] != "acme" {
		t.Errorf("owners after saving the drafting skill = %v, want [acme]", got)
	}
}

func TestSavingAnEmptyDraftingSkillRemovesTheKey(t *testing.T) {
	m, dir := openView(t, withFleets(`"work":{"name":"Work","drafting":{"skill":"wayfinder"},"claude":{}}`))
	empty := ""
	if err := m.SetFleetFields("work", config.FleetFields{DraftingSkill: &empty}); err != nil {
		t.Fatalf("SetFleetFields: %v", err)
	}
	if got := m.Current().Fleets[0].DraftingSkill; got != "" {
		t.Errorf("DraftingSkill after clearing = %q, want none", got)
	}
	if file := string(mustRead(t, dir+"/config.json")); strings.Contains(file, "wayfinder") {
		t.Errorf("config.json still holds the skill: %s", file)
	}
}

func TestSavingOtherFieldsAddsNoDraftingKey(t *testing.T) {
	m, dir := openView(t, withFleets(`"work":{"name":"Work","claude":{}}`))
	owners := []string{"acme"}
	if err := m.SetFleetFields("work", config.FleetFields{Owners: &owners}); err != nil {
		t.Fatalf("SetFleetFields: %v", err)
	}
	if file := string(mustRead(t, dir+"/config.json")); strings.Contains(file, "drafting") {
		t.Errorf("config.json gained a drafting key: %s", file)
	}
}
