package config_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// resConfig is a config with fleet "work" and the given top-level resources
// object ("" for none).
func resConfig(resources string) string {
	c := `{"version":1,"fleets":{"work":{"name":"Work","claude":{}}}`
	if resources != "" {
		c += `,"resources":` + resources
	}
	return c + `}`
}

func TestResourcesParse(t *testing.T) {
	cfg, err := config.Parse([]byte(resConfig(`{
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

	cfg, err = config.Parse([]byte(resConfig(``)))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Resources) != 0 {
		t.Errorf("Resources with no object = %v, want none", cfg.Resources)
	}
}

func TestResourcesParseErrors(t *testing.T) {
	cases := []struct{ name, config, want string }{
		{"capacity 0", resConfig(`{"phone":{"capacity":0}}`), "resources.phone.capacity"},
		{"check empty", resConfig(`{"phone":{"check":[]}}`), "resources.phone.check"},
		{"check has an empty string", resConfig(`{"phone":{"check":["sh",""]}}`), "resources.phone.check"},
		{"unknown key", resConfig(`{"phone":{"colour":"red"}}`), "resources.phone.colour"},
		{"name with a space", resConfig(`{"my phone":{}}`), "resources.my phone"},
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

// Admiral's own writes of fleets keep the top-level resources object.
func TestSetFleetsKeepsTheResourcesObject(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, resConfig(`{"phone":{}}`))
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
