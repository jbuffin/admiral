package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

const newFleetBase = `{"version":1,"x_future":{"keep":[1,{"deep":true}]},"max_ships":3,
"fleets":{"a":{"name":"A","search_roots":["/srv/a"],"claude":{}}},
"sources":{"other":{"plugin":"jira","name":"Jira","fleet":"a","settings":{"token":"fake-token-work-2"}}%s}}`

const ghSource = `,"gha":{"plugin":"github","name":"Work GitHub","fleet":"a","panel_order":["assigned","reviews"],
"settings":{"views":[{"id":"mine","name":"Mine"}],"host":"ghe.example.com","account":"work","hotLabels":["urgent"],"refreshMinutes":7,"token":"fake-token-work-2"}}`

func fleetsWith(t *testing.T, dir string, add ...string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var top struct {
		Fleets map[string]json.RawMessage `json:"fleets"`
	}
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	for _, k := range add {
		top.Fleets[k] = json.RawMessage(`{"name":"` + k + `","search_roots":["/srv/` + k + `"],"claude":{}}`)
	}
	out, _ := json.Marshal(top.Fleets)
	return out
}

func writtenSources(t *testing.T, dir string) map[string]map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Sources map[string]map[string]any `json:"sources"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Sources
}

func TestNewFleetGetsGitHubSourceCopiedFromTheFirstGitHubSource(t *testing.T) {
	// The template key sorts after the other GitHub source's, so only config order picks it.
	first := strings.Replace(ghSource, `"gha"`, `"ghz"`, 1)
	dir := migrateDir(t, fmt.Sprintf(newFleetBase, first+`,"gha":{"plugin":"github","name":"Second","fleet":"a","settings":{"views":[],"account":"other"}}`))
	if err := config.SetFleets(dir, fleetsWith(t, dir, "b")); err != nil {
		t.Fatal(err)
	}
	srcs := writtenSources(t, dir)
	got, ok := srcs["github-b"]
	if !ok {
		t.Fatalf("sources = %v, want github-b", keysOfSources(srcs))
	}
	wantJSON(t, "github-b", got, `{"plugin":"github","name":"Work GitHub","fleet":"b","panel_order":["assigned","reviews"],
"settings":{"views":[{"id":"mine","name":"Mine"}],"host":"ghe.example.com","account":"work","hotLabels":["urgent"],"refreshMinutes":7}}`)
	wantJSON(t, "other source", srcs["other"], `{"plugin":"jira","name":"Jira","fleet":"a","settings":{"token":"fake-token-work-2"}}`)
	if len(srcs) != 4 {
		t.Errorf("sources = %v, want the three old ones and github-b", keysOfSources(srcs))
	}
}

func TestNewFleetWithNoGitHubSourceGetsTheDefaultViews(t *testing.T) {
	dir := migrateDir(t, fmt.Sprintf(newFleetBase, ``))
	if err := config.SetFleets(dir, fleetsWith(t, dir, "b")); err != nil {
		t.Fatal(err)
	}
	srcs := writtenSources(t, dir)
	wantJSON(t, "github-b", srcs["github-b"], `{"plugin":"github","fleet":"b","settings":{"views":[]}}`)
	wantJSON(t, "other source", srcs["other"], `{"plugin":"jira","name":"Jira","fleet":"a","settings":{"token":"fake-token-work-2"}}`)
}

func TestNewFleetSourceKeyAvoidsTakenKeys(t *testing.T) {
	taken := `,"github-b":{"plugin":"jira","name":"Jira","fleet":"a"},"github-b-2":{"plugin":"jira","name":"Jira 2","fleet":"a"}`
	dir := migrateDir(t, fmt.Sprintf(newFleetBase, taken))
	if err := config.SetFleets(dir, fleetsWith(t, dir, "b")); err != nil {
		t.Fatal(err)
	}
	srcs := writtenSources(t, dir)
	if srcs["github-b"]["plugin"] != "jira" || srcs["github-b-2"]["plugin"] != "jira" {
		t.Errorf("a taken key was overwritten: %v", srcs)
	}
	if s := srcs["github-b-3"]; s == nil || s["plugin"] != "github" || s["fleet"] != "b" {
		t.Errorf("sources = %v, want github-b-3 on fleet b", keysOfSources(srcs))
	}
}

func TestSeveralNewFleetsInOneWriteEachGetTheirOwnSource(t *testing.T) {
	for name, write := range map[string]func(dir string, fleets json.RawMessage) error{
		"fleets": config.SetFleets,
		"fleets and remote": func(dir string, f json.RawMessage) error {
			return config.SetFleetsAndRemote(dir, f, config.Remote{})
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := migrateDir(t, fmt.Sprintf(newFleetBase, ghSource))
			if err := write(dir, fleetsWith(t, dir, "b", "c")); err != nil {
				t.Fatal(err)
			}
			srcs := writtenSources(t, dir)
			for _, f := range []string{"b", "c"} {
				s := srcs["github-"+f]
				if s == nil || s["fleet"] != f || s["name"] != "Work GitHub" {
					t.Errorf("sources = %v, want github-%s copied from gha", keysOfSources(srcs), f)
				}
			}
			if len(srcs) != 4 {
				t.Errorf("sources = %v, want other, gha and two new", keysOfSources(srcs))
			}
		})
	}
}

func TestPreviewShowsTheSourceTheWriteProduces(t *testing.T) {
	dir := migrateDir(t, fmt.Sprintf(newFleetBase, ghSource))
	fleets := fleetsWith(t, dir, "b")
	_, after, err := config.PreviewFleetsAndRemote(dir, fleets, config.Remote{})
	if err != nil {
		t.Fatal(err)
	}
	if err := config.SetFleetsAndRemote(dir, fleets, config.Remote{}); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	var a any
	if err := json.Unmarshal(after, &a); err != nil {
		t.Fatal(err)
	}
	wantJSON(t, "preview", a, string(written))
	var top struct{ Sources map[string]any }
	json.Unmarshal(after, &top)
	if _, ok := top.Sources["github-b"]; !ok {
		t.Errorf("the preview has no github-b source:\n%s", after)
	}
}

func TestAnExistingFleetEditedGetsNoSource(t *testing.T) {
	for name, extra := range map[string]string{"without a GitHub source": ``, "with one": ghSource} {
		t.Run(name, func(t *testing.T) {
			dir := migrateDir(t, fmt.Sprintf(newFleetBase, extra))
			var fleets map[string]map[string]any
			json.Unmarshal(fleetsWith(t, dir), &fleets)
			fleets["a"]["color"] = "#336699"
			raw, _ := json.Marshal(fleets)
			if err := config.SetFleets(dir, raw); err != nil {
				t.Fatal(err)
			}
			want := 1
			if extra != "" {
				want = 2
			}
			if srcs := writtenSources(t, dir); len(srcs) != want {
				t.Errorf("sources = %v, want %d unchanged", keysOfSources(srcs), want)
			}
		})
	}
}
