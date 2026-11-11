package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// fleetViews opens a config holding these fleets (key, color or "") and
// returns what the UI receives for them, as raw JSON objects keyed by fleet key.
func fleetViews(t *testing.T, fleets [][2]string) []map[string]any {
	t.Helper()
	dir := t.TempDir()
	body := `{"version": 1, "fleets": {`
	for i, f := range fleets {
		if i > 0 {
			body += ","
		}
		body += `"` + f[0] + `": {"name": "` + f[0] + `", "search_roots": ["/srv/x"], "codex": {}`
		if f[1] != "" {
			body += `, "color": "` + f[1] + `"`
		}
		body += `}`
	}
	body += `}}`
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	raw, err := json.Marshal(m.View())
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Error  any              `json:"error"`
		Fleets []map[string]any `json:"fleets"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Error != nil {
		t.Fatalf("config did not load: %v", v.Error)
	}
	return v.Fleets
}

// colorIndex returns the fleet's color_index, and whether the field is there.
func colorIndex(t *testing.T, f map[string]any) (int, bool) {
	t.Helper()
	v, ok := f["color_index"]
	if !ok {
		return 0, false
	}
	n, isNum := v.(float64)
	if !isNum {
		t.Fatalf("color_index = %v (%T), want a number", v, v)
	}
	return int(n), true
}

// Fleets without a color count through the old palette; the index is the raw
// counter, not wrapped (the client wraps it by the redesign sequence's length).
func TestColorIndex_DerivedFleetsCountUnwrapped(t *testing.T) {
	fleets := fleetViews(t, [][2]string{{"a", ""}, {"b", ""}, {"c", ""}, {"d", ""}, {"e", ""}, {"f", ""}})
	for i, f := range fleets {
		got, ok := colorIndex(t, f)
		if !ok || got != i {
			t.Errorf("fleet %d color_index = %d (present %v), want %d", i, got, ok, i)
		}
	}
	// The old UI's color and ink are unchanged: the sixth wraps to the first.
	if f := fleets[5]; f["color"] != "#3366E0" || f["ink"] != "#FFFFFF" {
		t.Errorf("sixth fleet color/ink = %v/%v, want #3366E0/#FFFFFF", f["color"], f["ink"])
	}
	if f := fleets[1]; f["color"] != "#F28C28" || f["ink"] != "#14304A" {
		t.Errorf("second fleet color/ink = %v/%v, want #F28C28/#14304A", f["color"], f["ink"])
	}
}

// A stored color that is an old palette entry (any case, as `admiral setup`
// writes the default hex) takes that entry's position; a custom hex has none.
// Neither kind advances the counter of the fleets that have no color.
func TestColorIndex_StoredPaletteHexAndCustom(t *testing.T) {
	fleets := fleetViews(t, [][2]string{
		{"a", ""},        // first derived: 0
		{"b", "#e2447a"}, // palette rose, lower case: 4
		{"c", "#7A5AF0"}, // palette violet: 2
		{"d", "#112233"}, // custom: no index
		{"e", ""},        // second derived: 1
	})
	want := []struct {
		idx int
		ok  bool
	}{{0, true}, {4, true}, {2, true}, {0, false}, {1, true}}
	for i, w := range want {
		got, ok := colorIndex(t, fleets[i])
		if ok != w.ok || (ok && got != w.idx) {
			t.Errorf("fleet %d color_index = %d (present %v), want %d (present %v)", i, got, ok, w.idx, w.ok)
		}
	}
	// color and ink keep their old-palette values for the old UI.
	if fleets[1]["color"] != "#e2447a" || fleets[1]["ink"] != "#FFFFFF" {
		t.Errorf("stored fleet color/ink = %v/%v", fleets[1]["color"], fleets[1]["ink"])
	}
	if fleets[3]["color"] != "#112233" {
		t.Errorf("custom fleet color = %v, want #112233", fleets[3]["color"])
	}
}
