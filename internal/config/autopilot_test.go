package config_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// The interface these tests assume (#114, rung 2 of autopilot):
//
//	Config.AutopilotMaxShips int                      // top-level cap, 3 when unset
//	Source.Autopilot map[string]config.PanelAutopilot // by panel id
//	config.PanelAutopilot{Enabled bool, Fleet, Harness string, Skill config.Skill,
//	    Args []string, MaxShips int, LowStakes string}
//	config.Skill{Kind, Name string}
//
// A source's autopilot object is also kept as it was read, so SourcesJSON can
// write it back untouched.

// apConfig is a config with a claude fleet "work", a codex-only fleet "cod"
// and one source whose autopilot object is sourceAP (top is the top-level one).
func apConfig(top, sourceAP string) string {
	c := `{"version":1,"fleets":{"work":{"name":"Work","claude":{}},"cod":{"name":"Cod","codex":{}}}`
	if top != "" {
		c += `,"autopilot":` + top
	}
	return c + `,"sources":{"s":{"plugin":"freeform","autopilot":` + sourceAP + `}}}`
}

func apEntry(extra string) string {
	e := `{"fleet":"work","harness":"claude","skill":{"kind":"commodore"}`
	if extra != "" {
		e += "," + extra
	}
	return e + "}"
}

func TestAutopilotParse(t *testing.T) {
	cfg, err := config.Parse([]byte(apConfig(`{"max_ships":5}`, `{"prs-needs-review":`+apEntry(`"enabled":true,"args":["--permission-mode","auto"],"max_ships":2,"low_stakes":"never"`)+`}`)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutopilotMaxShips != 5 {
		t.Errorf("AutopilotMaxShips = %d, want 5", cfg.AutopilotMaxShips)
	}
	src, _ := cfg.Source("s")
	want := config.PanelAutopilot{Enabled: true, Fleet: "work", Harness: "claude", Skill: config.Skill{Kind: "commodore"},
		Args: []string{"--permission-mode", "auto"}, MaxShips: 2, LowStakes: "never"}
	if got := src.Autopilot["prs-needs-review"]; !reflect.DeepEqual(got, want) {
		t.Errorf("entry = %+v, want %+v", got, want)
	}

	t.Run("defaults", func(t *testing.T) {
		cfg, err := config.Parse([]byte(apConfig(``, `{"p":{"fleet":"work","harness":"claude","skill":{"kind":"skill","name":"review"}}}`)))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AutopilotMaxShips != 3 {
			t.Errorf("AutopilotMaxShips = %d, want 3 with no autopilot object", cfg.AutopilotMaxShips)
		}
		src, _ := cfg.Source("s")
		want := config.PanelAutopilot{Enabled: false, Fleet: "work", Harness: "claude", Skill: config.Skill{Kind: "skill", Name: "review"},
			Args: []string{}, MaxShips: 1, LowStakes: "advisor"}
		if got := src.Autopilot["p"]; !reflect.DeepEqual(got, want) {
			t.Errorf("entry = %+v, want %+v", got, want)
		}
		cfg, err = config.Parse([]byte(apConfig(`{}`, `{}`)))
		if err != nil || cfg.AutopilotMaxShips != 3 {
			t.Errorf("an empty autopilot object: max ships %d, err %v, want 3 and none", cfg.AutopilotMaxShips, err)
		}
	})
}

func TestAutopilotParseErrors(t *testing.T) {
	cases := []struct{ name, config, want string }{
		{"harness other than claude", apConfig(``, `{"p":{"fleet":"work","harness":"codex","skill":{"kind":"none"}}}`),
			"autopilot runs on Claude Code only for now"},
		{"unknown key at the top", apConfig(`{"max_ship":2}`, `{}`), "autopilot.max_ship"},
		{"unknown key in an entry", apConfig(``, `{"p":`+apEntry(`"colour":"red"`)+`}`), "sources.s.autopilot.p.colour"},
		{"fleet missing", apConfig(``, `{"p":{"harness":"claude","skill":{"kind":"none"}}}`), "sources.s.autopilot.p.fleet"},
		{"harness missing", apConfig(``, `{"p":{"fleet":"work","skill":{"kind":"none"}}}`), "sources.s.autopilot.p.harness"},
		{"skill missing", apConfig(``, `{"p":{"fleet":"work","harness":"claude"}}`), "sources.s.autopilot.p.skill"},
		{"fleet does not exist", apConfig(``, `{"p":{"fleet":"nope","harness":"claude","skill":{"kind":"none"}}}`), "sources.s.autopilot.p.fleet"},
		{"fleet has no claude dock", apConfig(``, `{"p":{"fleet":"cod","harness":"claude","skill":{"kind":"none"}}}`), "sources.s.autopilot.p.fleet"},
		{"low_stakes", apConfig(``, `{"p":`+apEntry(`"low_stakes":"sometimes"`)+`}`), "sources.s.autopilot.p.low_stakes"},
		{"entry max_ships 0", apConfig(``, `{"p":`+apEntry(`"max_ships":0`)+`}`), "sources.s.autopilot.p.max_ships"},
		{"top max_ships 0", apConfig(`{"max_ships":0}`, `{}`), "autopilot.max_ships"},
		{"top max_ships not an integer", apConfig(`{"max_ships":1.5}`, `{}`), "autopilot.max_ships"},
		{"skill kind", apConfig(``, `{"p":{"fleet":"work","harness":"claude","skill":{"kind":"mystery"}}}`), "sources.s.autopilot.p.skill"},
		{"named skill without a name", apConfig(``, `{"p":{"fleet":"work","harness":"claude","skill":{"kind":"skill"}}}`), "sources.s.autopilot.p.skill"},
		{"skill name with a space", apConfig(``, `{"p":{"fleet":"work","harness":"claude","skill":{"kind":"skill","name":"a b"}}}`), "sources.s.autopilot.p.skill"},
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

// The five UI writes rebuild the whole sources object through SourcesJSON. The
// autopilot object must come back as the engineer wrote it: no defaults added,
// key order kept.
func TestSourcesJSONKeepsAutopilotAsRead(t *testing.T) {
	ap := `{ "prs": {"skill": {"kind":"none"}, "harness":"claude", "fleet":"work"} }`
	cfg, err := config.Parse([]byte(apConfig(``, ap)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := config.SourcesJSON(cfg.SourceList)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("SourcesJSON is not JSON: %v\n%s", err, out)
	}
	var want bytes.Buffer
	if err := json.Compact(&want, []byte(ap)); err != nil {
		t.Fatal(err)
	}
	if string(got["s"]["autopilot"]) != want.String() {
		t.Errorf("autopilot after SourcesJSON = %s, want %s", got["s"]["autopilot"], want.String())
	}
}

// SetSources promises a bad write never leaves the file invalid, so it must
// run the fleet cross-check that parsing the sources alone can't.
func TestSetSourcesRefusesAnAutopilotFleetThatIsGone(t *testing.T) {
	dir := t.TempDir()
	good := `{"version":1,"fleets":{"work":{"name":"Work","claude":{}}}}`
	p := writeFile(t, dir, good)
	err := config.SetSources(dir, json.RawMessage(`{"s":{"plugin":"freeform","autopilot":{"p":{"fleet":"nope","harness":"claude","skill":{"kind":"none"}}}}}`))
	if err == nil {
		t.Fatal("SetSources accepted an autopilot entry naming a fleet that doesn't exist")
	}
	if b, _ := os.ReadFile(p); string(b) != good {
		t.Errorf("a refused write changed config.json:\n%s", b)
	}

	if err := config.SetSources(dir, json.RawMessage(`{"s":{"plugin":"freeform","autopilot":{"p":{"fleet":"work","harness":"claude","skill":{"kind":"none"}}}}}`)); err != nil {
		t.Fatalf("SetSources with a valid entry: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "config.json")); !strings.Contains(string(b), `"autopilot"`) {
		t.Errorf("config.json lost the autopilot object:\n%s", b)
	}
}

// autopilot.resume_source (#116 u5) names the source and panel that get a ship's
// resume order. Parse checks only the shape; whether the source and panel exist
// is checked when an order is added, so a source can still be deleted.
func TestAutopilotResumeSourceShape(t *testing.T) {
	good := []string{
		`{"resume_source":{"source":"orders","panel":"orders"}}`,
		`{"max_ships":2,"resume_source":{"source":"not-a-source-yet","panel":"nowhere"}}`,
	}
	for _, top := range good {
		if _, err := config.Parse([]byte(apConfig(top, `{}`))); err != nil {
			t.Errorf("autopilot %s: %v, want it to parse", top, err)
		}
	}
	bad := []struct{ name, top, want string }{
		{"a string", `{"resume_source":"orders"}`, "autopilot.resume_source"},
		{"a missing source", `{"resume_source":{"panel":"orders"}}`, "autopilot.resume_source.source"},
		{"a missing panel", `{"resume_source":{"source":"orders"}}`, "autopilot.resume_source.panel"},
		{"an empty source", `{"resume_source":{"source":"","panel":"orders"}}`, "autopilot.resume_source.source"},
		{"a number for the panel", `{"resume_source":{"source":"orders","panel":3}}`, "autopilot.resume_source.panel"},
		{"an unknown key", `{"resume_source":{"source":"orders","panel":"orders","x":1}}`, "autopilot.resume_source.x"},
	}
	for _, c := range bad {
		_, err := config.Parse([]byte(apConfig(c.top, `{}`)))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one naming %s", c.name, err, c.want)
		}
	}
}
