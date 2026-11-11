package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

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

// The client wraps the index by the redesign sequence's length.
func TestColorIndex_DerivedFleetsCountUnwrapped(t *testing.T) {
	fleets := fleetViews(t, [][2]string{{"a", ""}, {"b", ""}, {"c", ""}, {"d", ""}, {"e", ""}, {"f", ""}})
	for i, f := range fleets {
		got, ok := colorIndex(t, f)
		if !ok || got != i {
			t.Errorf("fleet %d color_index = %d (present %v), want %d", i, got, ok, i)
		}
	}
}

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
	if fleets[3]["color"] != "#112233" {
		t.Errorf("a custom color = %v, want it served as stored, #112233", fleets[3]["color"])
	}
}
