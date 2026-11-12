package server_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/server"
)

// The fleet view and a fleet save offer AdmiralBot only the harnesses whose probes the store shows passed.

const harnessFleetsFixture = `{
	"version": 1,
	"fleets": {
		"work": {"name": "Work", "search_roots": ["/srv/work"], "claude": {}, "codex": {}},
		"cop": {"name": "Cop", "search_roots": ["/srv/cop"], "claude": {}, "copilot": {}}
	}
}`

type probedFixture struct {
	*fleetFixture
	store *capabilities.Store
}

func newProbedFixture(t *testing.T) *probedFixture {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(harnessFleetsFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	store := capabilities.Open(home)
	m.SetChatProbe(capabilities.ChatProbe(store))
	f := &fleetFixture{t: t, home: home, h: (&server.Server{Config: m}).Handler()}
	return &probedFixture{fleetFixture: f, store: store}
}

// seed records a probe result under one version of the harness and makes that version the detected one.
func (f *probedFixture) seed(fleet, harness string, id capabilities.ID, passed bool, detail string) {
	f.t.Helper()
	const version = "9.9.9"
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := f.store.RecordVersion(fleet, harness, version, at); err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.Record(fleet, harness, version, id, capabilities.Result{Passed: passed, At: at, Detail: detail}); err != nil {
		f.t.Fatal(err)
	}
}

type harnessChat struct {
	Harness        string   `json:"harness"`
	Model          string   `json:"model"`
	On             bool     `json:"on"`
	OffReason      string   `json:"off_reason"`
	Harnesses      []string `json:"harnesses"`
	HarnessOptions []struct {
		Harness    string `json:"harness"`
		Offered    bool   `json:"offered"`
		Reason     string `json:"reason"`
		Capability string `json:"capability"`
	} `json:"harness_options"`
	Models  []string `json:"models"`
	Efforts []string `json:"efforts"`
}

func (f *probedFixture) view(key string) harnessChat {
	f.t.Helper()
	_, body := f.do(http.MethodGet, "/api/config", nil)
	var v struct {
		Fleets []struct {
			Key  string      `json:"key"`
			Chat harnessChat `json:"chat"`
		} `json:"fleets"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		f.t.Fatal(err)
	}
	for _, fl := range v.Fleets {
		if fl.Key == key {
			return fl.Chat
		}
	}
	f.t.Fatalf("GET /api/config has no fleet %s: %s", key, body)
	return harnessChat{}
}

func TestFleetView_ChatOptionsFollowTheStoreAndGainTheHarnessOnceItPasses(t *testing.T) {
	f := newProbedFixture(t)
	f.seed("work", "codex", capabilities.BotMCPFence, true, "")

	g := f.view("work")
	if len(g.HarnessOptions) != 2 || g.HarnessOptions[0].Harness != "claude" || g.HarnessOptions[1].Harness != "codex" {
		t.Fatalf("work's harness_options = %+v, want claude then codex", g.HarnessOptions)
	}
	if o := g.HarnessOptions[0]; !o.Offered || o.Reason != "" {
		t.Errorf("claude option = %+v, want offered with no reason", o)
	}
	o := g.HarnessOptions[1]
	if o.Offered || o.Capability != string(capabilities.BotConfigHome) || !strings.Contains(o.Reason, "Codex") || !strings.Contains(o.Reason, "Separate config home") || !strings.Contains(o.Reason, "has not been checked") {
		t.Errorf("codex option = %+v, want not offered, missing the config home, named in its reason", o)
	}
	if !slices.Equal(g.Harnesses, []string{"claude"}) {
		t.Errorf("harnesses = %v, want the offered list [claude]", g.Harnesses)
	}
	if !slices.Equal(g.Models, []string{"sonnet", "opus", "haiku"}) || !slices.Equal(g.Efforts, []string{"", "low", "medium", "high"}) {
		t.Errorf("claude's models = %v efforts = %v, want its own lists", g.Models, g.Efforts)
	}

	// A passing probe makes it selectable on the next read, with no restart.
	f.seed("work", "codex", capabilities.BotConfigHome, true, "")
	g = f.view("work")
	if !g.HarnessOptions[1].Offered || g.HarnessOptions[1].Reason != "" || !slices.Equal(g.Harnesses, []string{"claude", "codex"}) {
		t.Errorf("after both probes passed: options %+v harnesses %v, want codex offered", g.HarnessOptions, g.Harnesses)
	}

	// A failed fence is named with the probe's detail.
	f.seed("work", "codex", capabilities.BotMCPFence, false, "a built-in tool answered")
	if o := f.view("work").HarnessOptions[1]; o.Offered || !strings.Contains(o.Reason, "failed") || !strings.Contains(o.Reason, "a built-in tool answered") || o.Capability != string(capabilities.BotMCPFence) {
		t.Errorf("codex option after a failed fence = %+v, want the failure and its detail", o)
	}
}

func TestFleetSave_ChatHarnessMustBeOfferedAndKeepsToItsOwnModelsAndEfforts(t *testing.T) {
	f := newProbedFixture(t)
	refused := func(what string, chat map[string]any, wants ...string) {
		t.Helper()
		before := f.file()
		code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"chat": chat})
		if code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", what, code, body)
			return
		}
		for _, w := range wants {
			if !strings.Contains(body, w) {
				t.Errorf("%s: body %s lacks %q", what, body, w)
			}
		}
		if f.file() != before {
			t.Errorf("%s changed config.json", what)
		}
	}

	refused("codex before its probes", map[string]any{"harness": "codex"}, "Codex", "has not been checked")
	f.seed("work", "codex", capabilities.BotMCPFence, true, "")
	refused("codex with only the fence passed", map[string]any{"harness": "codex", "model": "gpt-6.1-sol"}, "Separate config home")
	f.seed("work", "codex", capabilities.BotConfigHome, true, "")
	// The merged settings still hold claude's sonnet.
	refused("codex alone", map[string]any{"harness": "codex"}, "model")
	refused("a claude model on codex", map[string]any{"harness": "codex", "model": "opus"}, "model")

	if code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"chat": map[string]any{"harness": "codex", "model": "gpt-6.1-sol"}}); code != http.StatusOK {
		t.Fatalf("codex with its own model = %d %s, want 200", code, body)
	}
	g := f.view("work")
	if g.Harness != "codex" || g.Model != "gpt-6.1-sol" || !g.On || !slices.Contains(g.Models, "gpt-6-luna") || slices.Contains(g.Models, "sonnet") {
		t.Errorf("work after the save = %+v, want codex on gpt-6.1-sol with codex's models", g)
	}
	if code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"chat": map[string]any{"model": "gpt-6-luna"}}); code != http.StatusOK {
		t.Errorf("a codex model alone on a codex fleet = %d %s, want 200", code, body)
	}
	refused("a claude model alone on a codex fleet", map[string]any{"model": "sonnet"}, "model")
	refused("an effort codex lacks", map[string]any{"effort": "max"}, "effort")
}

func TestFleetSave_CopilotTakesNoEffort(t *testing.T) {
	f := newProbedFixture(t)
	f.seed("cop", "copilot", capabilities.BotMCPFence, true, "")
	f.seed("cop", "copilot", capabilities.BotConfigHome, true, "")

	if code, body := f.do(http.MethodPut, "/api/fleets/cop", map[string]any{"chat": map[string]any{"harness": "copilot", "model": "gpt-5.6-sol", "effort": "low"}}); code != http.StatusBadRequest || !strings.Contains(body, "effort") {
		t.Errorf("copilot with an effort = %d %s, want 400 naming effort", code, body)
	}
	if code, body := f.do(http.MethodPut, "/api/fleets/cop", map[string]any{"chat": map[string]any{"harness": "copilot", "model": "gpt-5.6-sol"}}); code != http.StatusOK {
		t.Fatalf("copilot with no effort = %d %s, want 200", code, body)
	}
	if g := f.view("cop"); g.Harness != "copilot" || !slices.Equal(g.Efforts, []string{""}) {
		t.Errorf("cop after the save = %+v, want copilot with only the default effort", g)
	}
}
