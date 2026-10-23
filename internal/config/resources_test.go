package config_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

// The interface these tests assume (#118, locked resources):
//
//	Config.Resources map[string]config.Resource        // by resource name
//	config.Resource{Capacity int, Check []string}      // Check nil when none
//	PanelAutopilot.Resources []string                  // nil when the entry names none
//
// A panel's names are checked against the top-level resources after the whole
// file is parsed, so the order of keys in the file doesn't matter.

// resConfig is a config with fleet "work", a source "s" whose autopilot entry
// for panel "p" names `panelRes` (a JSON array, or "" for none), and the given
// top-level resources object. The resources key comes after sources on purpose.
func resConfig(panelRes, resources string) string {
	extra := ""
	if panelRes != "" {
		extra = `,"resources":` + panelRes
	}
	c := `{"version":1,"fleets":{"work":{"name":"Work","claude":{}}},` +
		`"sources":{"s":{"plugin":"freeform","autopilot":{"p":` + apEntry(`"enabled":true`+extra) + `}}}`
	if resources != "" {
		c += `,"resources":` + resources
	}
	return c + `}`
}

func TestResourcesParse(t *testing.T) {
	cfg, err := config.Parse([]byte(resConfig(`["phone","ipad"]`, `{
		"phone": {"capacity": 1, "check": ["sh", "-c", "adb devices | grep -q X"]},
		"ipad": {},
		"farm": {"capacity": 250}}`)))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]config.Resource{
		"phone": {Capacity: 1, Check: []string{"sh", "-c", "adb devices | grep -q X"}},
		"ipad":  {Capacity: 1}, // capacity defaults to 1; no check
		"farm":  {Capacity: 250},
	}
	if !reflect.DeepEqual(cfg.Resources, want) {
		t.Errorf("Resources = %+v, want %+v", cfg.Resources, want)
	}
	src, _ := cfg.Source("s")
	if got := src.Autopilot["p"].Resources; !reflect.DeepEqual(got, []string{"phone", "ipad"}) {
		t.Errorf("panel resources = %v, want [phone ipad]", got)
	}

	cfg, err = config.Parse([]byte(resConfig(``, ``)))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Resources) != 0 {
		t.Errorf("Resources with no object = %v, want none", cfg.Resources)
	}
}

func TestResourcesParseErrors(t *testing.T) {
	cases := []struct{ name, config, want string }{
		{"panel names an undefined resource", resConfig(`["phone"]`, `{"ipad":{}}`), "sources.s.autopilot.p.resources"},
		{"panel names a resource with no resources object", resConfig(`["phone"]`, ``), "sources.s.autopilot.p.resources"},
		{"panel resources not strings", resConfig(`[1]`, `{"phone":{}}`), "sources.s.autopilot.p.resources"},
		{"capacity 0", resConfig(``, `{"phone":{"capacity":0}}`), "resources.phone.capacity"},
		{"capacity not an integer", resConfig(``, `{"phone":{"capacity":1.5}}`), "resources.phone.capacity"},
		{"check empty", resConfig(``, `{"phone":{"check":[]}}`), "resources.phone.check"},
		{"check has an empty string", resConfig(``, `{"phone":{"check":["sh",""]}}`), "resources.phone.check"},
		{"check not an array", resConfig(``, `{"phone":{"check":"adb devices"}}`), "resources.phone.check"},
		{"unknown key", resConfig(``, `{"phone":{"colour":"red"}}`), "resources.phone.colour"},
		{"name with a space", resConfig(``, `{"my phone":{}}`), "resources.my phone"},
		{"name too long", resConfig(``, `{"`+strings.Repeat("a", 65)+`":{}}`), "resources."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := config.Parse([]byte(c.config))
			if err == nil {
				t.Fatalf("Parse accepted the config, want an error containing %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to contain %q", err, c.want)
			}
		})
	}
}

// AC 4: a panel that names an undefined resource fails validation, and the
// manager keeps the last good config.
func TestManagerKeepsTheLastGoodConfigWhenAPanelNamesAnUndefinedResource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, resConfig(`["phone"]`, `{"phone":{"capacity":2}}`))
	m, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	if got := m.Current().Resources["phone"].Capacity; got != 2 {
		t.Fatalf("capacity after the first load = %d, want 2", got)
	}

	writeFile(t, dir, resConfig(`["phone","tablet"]`, `{"phone":{"capacity":3}}`))
	deadline := time.Now().Add(5 * time.Second)
	for m.View().Error == nil {
		if time.Now().After(deadline) {
			t.Fatal("the bad file never showed as an error")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if msg := m.View().Error.Message; !strings.Contains(msg, "tablet") {
		t.Errorf("error = %q, want it to name tablet", msg)
	}
	if got := m.Current().Resources["phone"].Capacity; got != 2 {
		t.Errorf("capacity after the bad file = %d, want the last good 2", got)
	}
}

// SetSources runs the same cross-check, against the resources already in the
// file, wherever the resources key sits; a refused write leaves the file as it was.
func TestSetSourcesChecksPanelResourcesAgainstTheFile(t *testing.T) {
	dir := t.TempDir()
	good := `{"version":1,"fleets":{"work":{"name":"Work","claude":{}}},"resources":{"phone":{"capacity":2}}}`
	p := writeFile(t, dir, good)
	panel := func(res string) json.RawMessage {
		return json.RawMessage(`{"s":{"plugin":"freeform","autopilot":{"p":` + apEntry(`"resources":`+res) + `}}}`)
	}

	if err := config.SetSources(dir, panel(`["nope"]`)); err == nil {
		t.Fatal("SetSources accepted a panel naming an undefined resource")
	}
	if b, _ := os.ReadFile(p); string(b) != good {
		t.Errorf("a refused write changed config.json:\n%s", b)
	}

	if err := config.SetSources(dir, panel(`["phone"]`)); err != nil {
		t.Fatalf("SetSources with a defined resource: %v", err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"capacity": 2`) && !strings.Contains(string(b), `"capacity":2`) {
		t.Errorf("config.json lost the top-level resources object:\n%s", b)
	}
}

// Admiral's own writes of fleets keep the top-level resources object.
func TestSetFleetsKeepsTheResourcesObject(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, resConfig(`["phone"]`, `{"phone":{}}`))
	if err := config.SetFleets(dir, json.RawMessage(`{"work":{"name":"Work 2","claude":{}}}`)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	cfg, err := config.Parse(b)
	if err != nil {
		t.Fatalf("config.json after SetFleets does not parse: %v\n%s", err, b)
	}
	if _, ok := cfg.Resources["phone"]; !ok {
		t.Errorf("SetFleets dropped the resources object:\n%s", b)
	}
}
