package config_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

const twoFleets = `"fleets":{
  "zulu":{"name":"Zulu","search_roots":["/srv/z"],"claude":{}},
  "alpha":{"name":"Alpha","search_roots":["/srv/a"],"codex":{}}}`

func withSources(sources string) []byte {
	return []byte(`{"version":1,` + twoFleets + `,"sources":` + sources + `}`)
}

func TestSourceNamesAnExistingFleet(t *testing.T) {
	cases := []struct {
		name, sources string
		want          []string // an error naming each, or none for a valid file
	}{
		{"a fleet that exists", `{"tracker":{"plugin":"jira","fleet":"alpha"}}`, nil},
		{"no fleet", `{"tracker":{"plugin":"jira"}}`, []string{"tracker", "fleet"}},
		{"an unknown fleet", `{"tracker":{"plugin":"jira","fleet":"ghost"}}`, []string{"tracker", "ghost"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := config.Parse(withSources(c.sources))
			if c.want == nil {
				if err != nil {
					t.Fatalf("Parse: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Parse accepted %s, want an error naming %v", c.sources, c.want)
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not name %q", err, w)
				}
			}
		})
	}
}

func TestFleetOwnersAndCloneRootRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, string(withSources(`{}`)))
	fleets := json.RawMessage(`{
  "zulu":{"name":"Zulu","search_roots":["/srv/z"],"owners":["acme","jbuffin"],"clone_root":"~/code/zulu","claude":{}},
  "alpha":{"name":"Alpha","search_roots":["/srv/a"],"codex":{}}}`)
	if err := config.SetFleets(dir, fleets); err != nil {
		t.Fatalf("SetFleets with owners and clone_root: %v", err)
	}
	if got := decode(t, path)["fleets"]; !jsonEqual(t, got, fleets) {
		t.Errorf("fleets after the write = %s, want %s", got, fleets)
	}
	if _, err := config.Parse(mustRead(t, path)); err != nil {
		t.Errorf("the written file does not parse: %v", err)
	}
}

func TestRemovingAFleetASourceNamesIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, string(withSources(`{"tracker":{"plugin":"jira","fleet":"zulu"}}`)))

	// A change that keeps zulu goes through.
	keep := json.RawMessage(`{
  "zulu":{"name":"Zulu","search_roots":["/srv/z"],"claude":{}},
  "alpha":{"name":"Alpha again","search_roots":["/srv/a"],"codex":{}}}`)
	if err := config.SetFleets(dir, keep); err != nil {
		t.Fatalf("SetFleets that keeps every named fleet: %v", err)
	}

	before := string(mustRead(t, path))
	gone := json.RawMessage(`{"alpha":{"name":"Alpha","search_roots":["/srv/a"],"codex":{}}}`)
	err := config.SetFleets(dir, gone)
	if err == nil {
		t.Fatal("SetFleets removed fleet zulu while source tracker names it")
	}
	for _, w := range []string{"tracker", "zulu"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not name %q", err, w)
		}
	}
	if got := string(mustRead(t, path)); got != before {
		t.Errorf("a refused write changed the file:\n%s", got)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSourceKeyCampaignIsReservedForCampaignShips(t *testing.T) {
	_, err := config.Parse(withSources(`{"campaign":{"plugin":"jira","fleet":"alpha"}}`))
	if err == nil || !strings.Contains(err.Error(), "campaign") {
		t.Fatalf("Parse accepted a source keyed campaign: err = %v, want one naming the reserved key", err)
	}
}
