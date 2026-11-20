package server_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/server"
)

// GET /api/config shows each dock's models and where they came from.

type dockModelsView struct {
	Fleets []struct {
		Key   string `json:"key"`
		Docks []struct {
			Harness      string   `json:"harness"`
			Models       []string `json:"models"`
			ModelsSource string   `json:"models_source"`
		} `json:"docks"`
		Chat struct {
			Models []string `json:"models"`
		} `json:"chat"`
	} `json:"fleets"`
}

func TestConfigShowsEachDocksModelsAndTheirSource(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(harnessFleetsFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	m.SetHarnessModels(func(fleet, harness string) ([]string, bool) {
		if fleet == "work" && harness == "claude" {
			return []string{"sonnet", "opus[1m]", "claude-fable-5-1"}, true
		}
		return nil, false
	})
	f := &fleetFixture{t: t, home: home, h: (&server.Server{Config: m}).Handler()}
	code, body := f.do(http.MethodGet, "/api/config", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/config = %d: %s", code, body)
	}
	var v dockModelsView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	for _, fl := range v.Fleets {
		for _, d := range fl.Docks {
			listed := fl.Key == "work" && d.Harness == "claude"
			switch {
			case listed && (d.ModelsSource != "listed" || !slices.Equal(d.Models, []string{"sonnet", "opus[1m]", "claude-fable-5-1"})):
				t.Errorf("work claude dock = %v %q, want the listed models", d.Models, d.ModelsSource)
			case !listed && (d.ModelsSource != "built_in" || len(d.Models) == 0):
				t.Errorf("%s %s dock = %v %q, want a built-in list", fl.Key, d.Harness, d.Models, d.ModelsSource)
			}
		}
		if fl.Key == "work" && !slices.Equal(fl.Chat.Models, []string{"sonnet", "opus[1m]", "claude-fable-5-1"}) {
			t.Errorf("work chat models = %v, want the claude dock's listed models", fl.Chat.Models)
		}
	}
}
