package sources

import (
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

func TestFleetGitHubIsTheFirstGitHubSourceOfTheFleet(t *testing.T) {
	cfg := &config.Config{
		Fleets: []config.Fleet{{Key: "a"}, {Key: "b"}},
		SourceList: []config.Source{
			{Key: "other", Plugin: "linear", Fleet: "a"},
			{Key: "elsewhere", Plugin: "github", Fleet: "b", Settings: []byte(`{"host":"b.example.com"}`)},
			{Key: "first", Plugin: "github", Fleet: "a", Settings: []byte(`{"host":"a.example.com"}`)},
			{Key: "second", Plugin: "github", Fleet: "a", Settings: []byte(`{"host":"later.example.com"}`)},
		},
	}
	if c := FleetGitHub(cfg, "a"); c == nil || c.Host() != "a.example.com" {
		t.Errorf("fleet a's client = %v, want the first GitHub source's host a.example.com", c)
	}
	if c := FleetGitHub(&config.Config{Fleets: []config.Fleet{{Key: "a"}}}, "a"); c != nil {
		t.Errorf("a fleet with no GitHub source got a client")
	}
}
