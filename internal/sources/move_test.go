package sources

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const moveConfig = `{"version":1,"x_note":"kept","fleets":{"work":{"name":"Work","claude":{}},"home":{"name":"Home","claude":{}}},
  "sources":{
    "pw":{"plugin":"notinstalled","fleet":"work","name":"Passwords","settings":{"url":"https://example.com","token":"decoy-secret-0a1b2c"}}}}`

// Never started: a running GitHub source would read the machine's gh login.
const ghMoveConfig = `{"version":1,"fleets":{"work":{"name":"Work","claude":{}},"home":{"name":"Home","claude":{}}},
  "sources":{"gh":{"plugin":"github","fleet":"work","settings":{"views":[]}}}}`

func (f *serviceFixture) storedFleet(key string) string {
	f.t.Helper()
	var cfg struct {
		Sources map[string]struct {
			Fleet string `json:"fleet"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(f.file()), &cfg); err != nil {
		f.t.Fatal(err)
	}
	return cfg.Sources[key].Fleet
}

func TestSaveMovesAPluginSourceToAnotherFleet(t *testing.T) {
	f := newServiceFixture(t, moveConfig)
	f.startSources()
	code, body := f.do(http.MethodPut, "/pw", map[string]any{"fleet": "home"})
	if code != http.StatusOK {
		t.Fatalf("PUT {fleet: home} = %d %s, want 200", code, body)
	}
	if got := f.storedFleet("pw"); got != "home" {
		t.Errorf("fleet in config.json = %q, want home", got)
	}
	f.waitView("pw", "fleet home", func(s SourceView) bool { return s.Fleet == "home" })
	if got := f.stored("pw"); !strings.Contains(got, "decoy-secret-0a1b2c") {
		t.Errorf("a move without settings must keep them, got %s", got)
	}
}

func TestSaveWithTheSameOrNoFleetChangesNothing(t *testing.T) {
	f := newServiceFixture(t, moveConfig)
	for name, body := range map[string]map[string]any{
		"no fleet":       {"name": "Passwords"},
		"the same fleet": {"fleet": "work"},
	} {
		if code, b := f.do(http.MethodPut, "/pw", body); code != http.StatusOK {
			t.Fatalf("PUT with %s = %d %s, want 200", name, code, b)
		}
		if got := f.storedFleet("pw"); got != "work" {
			t.Errorf("after a save with %s the fleet is %q, want work", name, got)
		}
	}
}

func TestSaveToAnUnknownFleetIs422AndWritesNothing(t *testing.T) {
	f := newServiceFixture(t, moveConfig)
	before := f.file()
	code, body := f.do(http.MethodPut, "/pw", map[string]any{"fleet": "nosuch"})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, `fleet \"nosuch\" is not a fleet in the config`) {
		t.Errorf("PUT {fleet: nosuch} = %d %s, want 422 with the add path's wording", code, body)
	}
	if f.file() != before {
		t.Error("a refused move changed config.json")
	}
}

func TestSaveRefusesToMoveTheBuiltInGitHubSource(t *testing.T) {
	f := newServiceFixture(t, ghMoveConfig)
	before := f.file()
	code, body := f.do(http.MethodPut, "/gh", map[string]any{"fleet": "home"})
	if code != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(body), "add a github source") {
		t.Errorf("PUT {fleet: home} on the GitHub source = %d %s, want 422 saying to add a GitHub source to the other fleet", code, body)
	}
	if f.file() != before {
		t.Error("a refused move changed config.json")
	}
	if code, b := f.do(http.MethodPut, "/gh", map[string]any{"fleet": "work"}); code != http.StatusOK {
		t.Errorf("PUT with the GitHub source's own fleet = %d %s, want 200", code, b)
	}
}

func TestMoveKeepsMaskedSecretsInTheSameSave(t *testing.T) {
	f := newServiceFixture(t, moveConfig)
	f.startSources()
	code, body := f.do(http.MethodPut, "/pw", map[string]any{
		"fleet":    "home",
		"settings": map[string]any{"url": "https://example.com/new", "token": unchangedMarker},
	})
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s, want 200", code, body)
	}
	if got, want := f.stored("pw"), `{"token":"decoy-secret-0a1b2c","url":"https://example.com/new"}`; got != want {
		t.Errorf("stored settings = %s, want %s", got, want)
	}
	if got := f.storedFleet("pw"); got != "home" {
		t.Errorf("fleet = %q, want home", got)
	}
}
