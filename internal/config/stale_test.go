package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/standing"
)

// A preset switch is judged on the file being edited, not on the config loaded before a hand edit.
func TestAPresetSaveReadsThePresetFromTheFileNotTheLoadedConfig(t *testing.T) {
	dir := t.TempDir()
	stale := `{"version":1,"fleets":{"work":{"name":"Work"}}}`
	hand := `{"version":1,"fleets":{"work":{"name":"Work","preset":"home","limits":{"write_deny":[".env"]}}}}`
	loaded, err := Parse([]byte(stale))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	home := "home"
	if err := editFleet(dir, "work", FleetFields{Preset: &home, Limits: map[string]json.RawMessage{"wall_clock_minutes": json.RawMessage(`30`)}}, loaded, nil); err != nil {
		t.Fatalf("editFleet: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	l := cfg.Fleets[0].Limits
	if cfg.Fleets[0].Preset != standing.Home || len(l.WriteDeny) != 1 || l.WallClockMinutes != 30 {
		t.Errorf("preset %q, write_deny %v, wall clock %d; want home with the hand-edited write_deny kept", cfg.Fleets[0].Preset, l.WriteDeny, l.WallClockMinutes)
	}
}
