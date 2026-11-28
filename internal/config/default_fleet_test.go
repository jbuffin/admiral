package config_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

const alphaBravo = `"fleets":{"alpha":{"name":"Alpha","claude":{}},"bravo":{"name":"Bravo","claude":{}}}`

func wantDefault(t *testing.T, v config.View, key string, fallback bool) {
	t.Helper()
	if v.DefaultFleet != key || v.DefaultFleetFallback != fallback {
		t.Errorf("view default = %q (fallback %v), want %q (fallback %v)", v.DefaultFleet, v.DefaultFleetFallback, key, fallback)
	}
}

func TestDefaultFleetNamesAFleetOfTheFile(t *testing.T) {
	m, _ := openView(t, `{"version":1,"default_fleet":"bravo",`+alphaBravo+`}`)
	wantDefault(t, m.View(), "bravo", false)
}

func TestDefaultFleetFallsBackToTheFirstFleet(t *testing.T) {
	cases := map[string]string{
		"no key":         `{"version":1,` + alphaBravo + `}`,
		"a fleet's gone": `{"version":1,"default_fleet":"gone",` + alphaBravo + `}`,
	}
	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := openView(t, file)
			if e := m.View().Error; e != nil {
				t.Fatalf("config refused: %s", e.Message)
			}
			wantDefault(t, m.View(), "alpha", true)
		})
	}
}

func TestDefaultFleetWithNoFleetsIsEmptyAndFallsBack(t *testing.T) {
	m, _ := openView(t, `{"version":1,"fleets":{}}`)
	wantDefault(t, m.View(), "", true)
}

func TestDefaultFleetThatIsNotAStringIsAParseError(t *testing.T) {
	for _, v := range []string{`5`, `true`, `["bravo"]`, `{"a":1}`} {
		if _, err := config.Parse([]byte(`{"version":1,"default_fleet":` + v + `,` + alphaBravo + `}`)); err == nil {
			t.Errorf("default_fleet %s parsed, want an error", v)
		}
	}
}

func TestDefaultFleetFollowsTheLastGoodConfigWhileTheFileIsInvalid(t *testing.T) {
	m, dir := openView(t, `{"version":1,"default_fleet":"bravo",`+alphaBravo+`}`)
	writeFile(t, dir, `{"version":1,"default_fleet":"alpha",`)
	m.Reload()
	v := m.View()
	if v.Error == nil {
		t.Fatal("the broken file shows no banner")
	}
	wantDefault(t, v, "bravo", false)
}

func TestSetDefaultFleetKeepsEveryOtherKey(t *testing.T) {
	m, dir := openView(t, richConfig)
	path := filepath.Join(dir, "config.json")
	before := decode(t, path)

	if err := m.SetDefaultFleet("alpha"); err != nil {
		t.Fatalf("SetDefaultFleet: %v", err)
	}
	after := decode(t, path)
	if string(after["default_fleet"]) != `"alpha"` {
		t.Errorf("default_fleet = %s, want \"alpha\": picking the first fleet still writes the key", after["default_fleet"])
	}
	for k, want := range before {
		if !jsonEqual(t, want, after[k]) {
			t.Errorf("%q changed: before %s, after %s", k, want, after[k])
		}
	}
	if len(after) != len(before)+1 {
		t.Errorf("top-level keys = %d, want %d", len(after), len(before)+1)
	}
	wantDefault(t, m.View(), "alpha", false)
}

func TestAPickedFirstFleetStaysTheDefaultWhenAReorderMovesAnotherFirst(t *testing.T) {
	m, dir := openView(t, `{"version":1,`+alphaBravo+`}`)
	if err := m.SetDefaultFleet("alpha"); err != nil {
		t.Fatalf("SetDefaultFleet: %v", err)
	}
	reordered := `{"bravo":{"name":"Bravo","claude":{}},"alpha":{"name":"Alpha","claude":{}}}`
	if err := config.SetFleets(dir, json.RawMessage(reordered)); err != nil {
		t.Fatalf("SetFleets: %v", err)
	}
	m.Reload()
	if got := keysOf(m.View()); got[0] != "bravo" {
		t.Fatalf("fleet order = %v, want bravo first", got)
	}
	wantDefault(t, m.View(), "alpha", false)
}

func TestRemovingTheDefaultFleetIsNeverRefusedAndFallsBack(t *testing.T) {
	m, dir := openView(t, `{"version":1,"default_fleet":"bravo",`+alphaBravo+`}`)
	if err := config.SetFleets(dir, json.RawMessage(`{"alpha":{"name":"Alpha","claude":{}}}`)); err != nil {
		t.Fatalf("removing the default fleet was refused: %v", err)
	}
	m.Reload()
	wantDefault(t, m.View(), "alpha", true)
	if got := decode(t, filepath.Join(dir, "config.json"))["default_fleet"]; string(got) != `"bravo"` {
		t.Errorf("default_fleet = %s, want the stale \"bravo\" left alone", got)
	}
}

func TestSetDefaultFleetRefusesAFleetThatIsNotInTheFile(t *testing.T) {
	m, dir := openView(t, richConfig)
	path := filepath.Join(dir, "config.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetDefaultFleet("nope"); !errors.Is(err, config.ErrNoFleet) {
		t.Fatalf("SetDefaultFleet(nope) = %v, want ErrNoFleet", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Errorf("the refused write changed the file:\n%s", after)
	}
}

// A fleet added to the file behind the manager's last reload counts: the check reads the file it writes.
func TestSetDefaultFleetChecksTheFileNotTheLastLoadedConfig(t *testing.T) {
	m, dir := openView(t, `{"version":1,"fleets":{"alpha":{"name":"Alpha","claude":{}}}}`)
	writeFile(t, dir, `{"version":1,`+alphaBravo+`}`)
	if err := m.SetDefaultFleet("bravo"); err != nil {
		t.Fatalf("SetDefaultFleet(bravo) = %v, want it accepted: the file has bravo", err)
	}
}
