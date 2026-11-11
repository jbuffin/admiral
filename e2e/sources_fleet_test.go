package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// fleetsOnly is a config with fleets zulu then alpha and the given sources.
func (w *world) fleetsOnly(sources string) string {
	return `{"version":1,"fleets":{` + w.seedFleetsOf("zulu", "alpha") + `},"sources":` + sources + `}`
}

func (w *world) addTestSource(req map[string]any) (int, string) {
	w.t.Helper()
	p := w.newTP("added")
	req["plugin"] = "testplug"
	req["settings"] = p.settings(nil)
	return w.srcReq(http.MethodPost, "", req)
}

func TestAddSourceTakesAFleet(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.installPlugin("testplug", "testplug")
	writeConfig(t, w.home, w.fleetsOnly(`{}`))
	w.start()

	// No fleet named: the first fleet in the file.
	code, body := w.addTestSource(map[string]any{"name": "Default"})
	w.mustOK(code, body, "POST /api/sources with no fleet")
	var added struct {
		Key   string `json:"key"`
		Fleet string `json:"fleet"`
	}
	if err := json.Unmarshal([]byte(body), &added); err != nil || added.Fleet != "zulu" {
		t.Fatalf("POST with no fleet answered %s, want the source on fleet zulu", body)
	}
	if got := w.configSources()[added.Key]["fleet"]; got != "zulu" {
		t.Errorf("config.json has fleet %v for %s, want zulu", got, added.Key)
	}

	code, body = w.addTestSource(map[string]any{"name": "Chosen", "fleet": "alpha"})
	w.mustOK(code, body, "POST /api/sources with fleet alpha")
	_ = json.Unmarshal([]byte(body), &added)
	if got := w.configSources()[added.Key]["fleet"]; got != "alpha" {
		t.Errorf("config.json has fleet %v for %s, want alpha", got, added.Key)
	}

	before := w.readFile(configPath(w.home))
	code, body = w.addTestSource(map[string]any{"name": "Lost", "fleet": "ghost"})
	if code/100 != 4 || !strings.Contains(body, "ghost") {
		t.Errorf("POST with an unknown fleet = %d %s, want 4xx naming it", code, body)
	}
	if got := w.readFile(configPath(w.home)); got != before {
		t.Errorf("a refused add changed config.json:\n%s", got)
	}
}

func TestAddSourceWithNoFleetsIsRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.installPlugin("testplug", "testplug")
	writeConfig(t, w.home, `{"version":1,"fleets":{},"sources":{}}`)
	w.start()

	code, body := w.addTestSource(map[string]any{"name": "Nowhere"})
	w.wantConflict(code, body, "POST /api/sources with no fleets")
	if !strings.Contains(strings.ToLower(body), "fleet") {
		t.Errorf("the refusal %q does not say to add a fleet first", body)
	}
	if _, err := os.Stat(w.dataDir("testplug")); err == nil {
		t.Errorf("a refused add created data/testplug")
	}
	if got := w.configSources(); len(got) != 0 {
		t.Errorf("config sources = %v, want none", got)
	}
}

func TestSettingsSaveKeepsEverySourcesFleet(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.installPlugin("testplug", "testplug")
	a, b := w.newTP("sa"), w.newTP("sb")
	one, _ := json.Marshal(map[string]any{"plugin": "testplug", "name": "Sa", "fleet": "alpha", "settings": a.settings(nil)})
	two, _ := json.Marshal(map[string]any{"plugin": "testplug", "name": "Sb", "fleet": "zulu", "settings": b.settings(nil)})
	writeConfig(t, w.home, w.fleetsOnly(`{"sa":`+string(one)+`,"sb":`+string(two)+`}`))
	w.start()
	w.waitSource("sa", 10*time.Second, "state ok", isOK)

	code, body := w.srcReq(http.MethodPut, "/sa", map[string]any{"name": "Renamed", "settings": a.settings(nil)})
	w.mustOK(code, body, "PUT /api/sources/sa")
	got := w.configSources()
	if got["sa"]["fleet"] != "alpha" || got["sb"]["fleet"] != "zulu" || got["sa"]["name"] != "Renamed" {
		t.Errorf("after a settings save the sources are %v, want sa on alpha (renamed) and sb on zulu", got)
	}
}
