package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// A fleet's AdmiralBot harness is offered from the capability store, per fleet and harness.

const (
	mcpFence   = "bot.mcp_fence"
	configHome = "bot.config_home"
)

// probes is a fake capability store keyed "fleet/harness/capability" to {status, detail}.
type probes map[string][2]string

func (p probes) probe(fleet, harness, capability string) (string, string) {
	if r, ok := p[fleet+"/"+harness+"/"+capability]; ok {
		return r[0], r[1]
	}
	return "not_checked", ""
}

func (p probes) set(fleet, harness, capability, status, detail string) {
	p[fleet+"/"+harness+"/"+capability] = [2]string{status, detail}
}

func (p probes) pass(fleet, harness string) {
	p.set(fleet, harness, mcpFence, "passed", "")
	if harness != "claude" {
		p.set(fleet, harness, configHome, "passed", "")
	}
}

const allDocks = `"claude":{},"codex":{},"copilot":{}`

func chatFleet(key, chat string) string {
	if chat != "" {
		chat = `,"chat":` + chat
	}
	return `"` + key + `":{"name":"` + strings.ToUpper(key) + `",` + allDocks + chat + `}`
}

func openWithProbes(t *testing.T, cfg string, p probes) (*config.Manager, string) {
	t.Helper()
	m, dir := openView(t, cfg)
	m.SetChatProbe(p.probe)
	return m, dir
}

type harnessOption struct {
	Harness    string `json:"harness"`
	Offered    bool   `json:"offered"`
	Reason     string `json:"reason"`
	Capability string `json:"capability"`
}

type chatWire struct {
	chatSettings
	HarnessOptions []harnessOption `json:"harness_options"`
	Models         []string        `json:"models"`
	Efforts        []string        `json:"efforts"`
}

func chatWireOf(t *testing.T, m *config.Manager, key string) chatWire {
	t.Helper()
	raw, err := json.Marshal(fleetOf(m.View(), key).Chat)
	if err != nil {
		t.Fatal(err)
	}
	var out chatWire
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func sameSet(got, want []string) bool {
	g, w := slices.Clone(got), slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	return slices.Equal(g, w)
}

func TestChatHarnessIsOfferedWhenItsBotCapabilitiesPass(t *testing.T) {
	rows := []struct {
		name         string
		harness      string
		mcp, home    string
		homeDetail   string
		offered      bool
		reasonHas    []string
		reasonHasNot []string
	}{
		{name: "claude passed", harness: "claude", mcp: "passed", offered: true},
		// The one row that flips if the engineer wants Claude off until its probe passes.
		{name: "claude not checked", harness: "claude", mcp: "not_checked", offered: true},
		{name: "claude failed", harness: "claude", mcp: "failed", offered: false, reasonHas: []string{"Claude", "MCP-only fence", "failed"}},
		{name: "claude needs no config home", harness: "claude", mcp: "passed", home: "failed", offered: true},
		{name: "codex both passed", harness: "codex", mcp: "passed", home: "passed", offered: true},
		{name: "codex fence not checked", harness: "codex", mcp: "not_checked", home: "passed", offered: false, reasonHas: []string{"Codex", "MCP-only fence", "has not been checked"}, reasonHasNot: []string{"Separate config home"}},
		{name: "codex fence failed", harness: "codex", mcp: "failed", home: "passed", offered: false, reasonHas: []string{"Codex", "MCP-only fence", "failed"}},
		{name: "codex home not checked", harness: "codex", mcp: "passed", home: "not_checked", offered: false, reasonHas: []string{"Codex", "Separate config home", "has not been checked"}, reasonHasNot: []string{"MCP-only fence"}},
		{name: "copilot home failed", harness: "copilot", mcp: "passed", home: "failed", homeDetail: "login lost in the new home", offered: false, reasonHas: []string{"Copilot", "Separate config home", "failed", "login lost in the new home"}},
		{name: "copilot nothing checked", harness: "copilot", mcp: "not_checked", home: "not_checked", offered: false, reasonHas: []string{"Copilot", "has not been checked"}},
		{name: "copilot both passed", harness: "copilot", mcp: "passed", home: "passed", offered: true},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			p := probes{}
			p.set("w", r.harness, mcpFence, r.mcp, "")
			if r.home != "" {
				p.set("w", r.harness, configHome, r.home, r.homeDetail)
			}
			m, _ := openWithProbes(t, withFleets(chatFleet("w", `{"harness":"`+r.harness+`"}`)), p)
			cfg := m.Current()
			f, _ := cfg.Fleet("w")
			reason := cfg.ChatOffReason(f)
			if (reason == "") != r.offered {
				t.Fatalf("ChatOffReason = %q, want offered=%v", reason, r.offered)
			}
			view := chatWireOf(t, m, "w")
			if view.On != r.offered || view.OffReason != reason {
				t.Errorf("view on=%v off_reason=%q, want on=%v off_reason=%q", view.On, view.OffReason, r.offered, reason)
			}
			if slices.Contains(view.Harnesses, r.harness) != r.offered {
				t.Errorf("harnesses = %v, want %s listed=%v", view.Harnesses, r.harness, r.offered)
			}
			for _, want := range r.reasonHas {
				if !strings.Contains(reason, want) {
					t.Errorf("reason %q lacks %q", reason, want)
				}
			}
			for _, not := range r.reasonHasNot {
				if strings.Contains(reason, not) {
					t.Errorf("reason %q names %q, which is not what is missing", reason, not)
				}
			}
			if strings.Contains(reason, "\n") {
				t.Errorf("reason %q is more than one line", reason)
			}
		})
	}
}

func TestChatGateWithNoStoreWiredCountsClaudeOnlyAsOffered(t *testing.T) {
	m, _ := openView(t, joinFleets(chatFleet("w", ""), chatFleet("x", `{"harness":"codex"}`)))
	if got := chatWireOf(t, m, "w"); !got.On || !slices.Equal(got.Harnesses, []string{"claude"}) {
		t.Errorf("claude with no store: on=%v harnesses=%v, want on with claude only", got.On, got.Harnesses)
	}
	if got := chatWireOf(t, m, "x"); got.On || !strings.Contains(got.OffReason, "Codex") {
		t.Errorf("codex with no store: on=%v reason=%q, want off with a reason naming Codex", got.On, got.OffReason)
	}
}

func joinFleets(fleets ...string) string { return withFleets(strings.Join(fleets, ",")) }

func TestChatHarnessOptionsListEveryDockedCandidateWithItsReason(t *testing.T) {
	p := probes{}
	p.set("a", "codex", mcpFence, "passed", "")
	p.set("a", "codex", configHome, "failed", "no login in the new home")
	p.pass("b", "codex")
	m, _ := openWithProbes(t, withFleets(
		`"a":{"name":"A","claude":{},"codex":{}},"b":{"name":"B","claude":{},"codex":{}},"c":{"name":"C","codex":{}}`), p)

	a := chatWireOf(t, m, "a")
	if len(a.HarnessOptions) != 2 || a.HarnessOptions[0].Harness != "claude" || a.HarnessOptions[1].Harness != "codex" {
		t.Fatalf("a's options = %+v, want claude then codex, nothing for a harness it has no dock for", a.HarnessOptions)
	}
	if o := a.HarnessOptions[0]; !o.Offered || o.Reason != "" || o.Capability != "" {
		t.Errorf("a's claude option = %+v, want offered with no reason", o)
	}
	o := a.HarnessOptions[1]
	if o.Offered || o.Capability != configHome || !strings.Contains(o.Reason, "no login in the new home") {
		t.Errorf("a's codex option = %+v, want not offered, missing %s, with the probe's detail", o, configHome)
	}
	if !slices.Equal(a.Harnesses, []string{"claude"}) {
		t.Errorf("a's harnesses = %v, want the offered list [claude]", a.Harnesses)
	}

	// The store is read per fleet: b's codex passed.
	b := chatWireOf(t, m, "b")
	if len(b.HarnessOptions) != 2 || !b.HarnessOptions[1].Offered || !slices.Equal(b.Harnesses, []string{"claude", "codex"}) {
		t.Errorf("b's options = %+v harnesses = %v, want codex offered beside claude", b.HarnessOptions, b.Harnesses)
	}

	// A fleet with no claude dock still lists its codex dock and nothing else.
	c := chatWireOf(t, m, "c")
	if len(c.HarnessOptions) != 1 || c.HarnessOptions[0].Harness != "codex" || c.HarnessOptions[0].Offered {
		t.Errorf("c's options = %+v, want only a codex option, not offered", c.HarnessOptions)
	}
}

func TestChatNoDockReasonNamesTheChosenHarness(t *testing.T) {
	p := probes{}
	p.pass("w", "codex")
	m, _ := openWithProbes(t, withFleets(`"w":{"name":"W","claude":{},"chat":{"harness":"codex"}},"n":{"name":"N","codex":{}}`), p)
	if got := chatWireOf(t, m, "w"); got.On || !strings.Contains(got.OffReason, "Codex") || !strings.Contains(got.OffReason, "dock") {
		t.Errorf("a fleet whose chosen codex has no dock: on=%v reason=%q, want off naming Codex and its missing dock", got.On, got.OffReason)
	}
	if got := chatWireOf(t, m, "n"); got.On || !strings.Contains(got.OffReason, "Claude") || !strings.Contains(got.OffReason, "dock") {
		t.Errorf("a fleet whose chosen claude has no dock: on=%v reason=%q, want off naming Claude and its missing dock", got.On, got.OffReason)
	}
}

func TestChatSettingsFollowTheHarnessInTheFile(t *testing.T) {
	m, _ := openView(t, joinFleets(
		chatFleet("keeps", `{"model":"gpt-6.1-sol","harness":"codex"}`), chatFleet("strong", `{"harness":"codex"}`),
		chatFleet("claude", `{"model":"gpt-6.1-sol"}`), chatFleet("cop", `{"harness":"copilot","effort":"high"}`),
		chatFleet("small", `{"harness":"copilot","model":"gpt-5.6-luna"}`)))
	if g := chatWireOf(t, m, "keeps"); g.Harness != "codex" || g.Model != "gpt-6.1-sol" {
		t.Errorf("model before harness: %s %s, want codex gpt-6.1-sol (the harness is resolved before the model is checked)", g.Harness, g.Model)
	}
	if g := chatWireOf(t, m, "strong"); g.Model != "gpt-6.1-sol" {
		t.Errorf("codex with no model = %q, want its strong model gpt-6.1-sol", g.Model)
	}
	if g := chatWireOf(t, m, "claude"); g.Model != "sonnet" {
		t.Errorf("claude with a codex model = %q, want the fallback sonnet", g.Model)
	}
	if g := chatWireOf(t, m, "cop"); g.Model != "gpt-5.6-sol" || g.Effort != "" {
		t.Errorf("copilot = model %q effort %q, want gpt-5.6-sol and the default effort (copilot takes none)", g.Model, g.Effort)
	}
	if g := chatWireOf(t, m, "small"); g.Model != "gpt-5.6-luna" {
		t.Errorf("copilot's small model = %q, want it kept", g.Model)
	}
}

func TestChatViewServesTheModelAndEffortListsOfTheChosenHarness(t *testing.T) {
	m, _ := openView(t, joinFleets(
		chatFleet("cl", ""), chatFleet("cx", `{"harness":"codex"}`), chatFleet("cp", `{"harness":"copilot"}`)))
	cases := []struct {
		key     string
		models  []string
		efforts []string
	}{
		{"cl", []string{"sonnet", "opus", "haiku"}, []string{"", "low", "medium", "high"}},
		{"cx", []string{"gpt-6-luna", "gpt-6.1-sol"}, []string{"", "low", "medium", "high"}},
		{"cp", []string{"gpt-5.6-luna", "gpt-5.6-sol"}, []string{""}},
	}
	for _, c := range cases {
		g := chatWireOf(t, m, c.key)
		if !sameSet(g.Models, c.models) {
			t.Errorf("%s models = %v, want %v", c.key, g.Models, c.models)
		}
		if !sameSet(g.Efforts, c.efforts) {
			t.Errorf("%s efforts = %v, want %v", c.key, g.Efforts, c.efforts)
		}
	}
}

func TestChatSaveChecksTheMergedSettings(t *testing.T) {
	p := probes{}
	p.pass("w", "codex")
	p.pass("w", "copilot")
	m, dir := openWithProbes(t, joinFleets(chatFleet("w", ""), `"v":{"name":"V","claude":{},"codex":{}}`), p)
	save := func(chat map[string]string) error {
		raw := map[string]json.RawMessage{}
		for k, v := range chat {
			raw[k] = json.RawMessage(v)
		}
		return m.SetFleetFields("w", config.FleetFields{Chat: raw})
	}
	onDisk := func() string {
		b, err := os.ReadFile(filepath.Join(dir, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	refused := func(what string, err error, wants ...string) {
		t.Helper()
		if err == nil {
			t.Errorf("%s was accepted", what)
			return
		}
		for _, w := range wants {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: err %q lacks %q", what, err, w)
			}
		}
	}

	before := onDisk()
	// Not offered: a save of the harness alone is refused with the gate's reason.
	if err := m.SetFleetFields("v", config.FleetFields{Chat: map[string]json.RawMessage{"harness": json.RawMessage(`"codex"`)}}); err == nil || !strings.Contains(err.Error(), "MCP-only fence") {
		t.Errorf("codex on a fleet whose probes are not seeded: err = %v, want a refusal naming the MCP-only fence", err)
	}
	// Offered, but the merged object still carries claude's sonnet.
	refused("codex with the merged model sonnet", save(map[string]string{"harness": `"codex"`}), "model", "sonnet")
	refused("a claude model on codex", save(map[string]string{"harness": `"codex"`, "model": `"opus"`}), "model")
	if onDisk() != before {
		t.Error("a refused save changed config.json")
	}

	if err := save(map[string]string{"harness": `"codex"`, "model": `"gpt-6.1-sol"`}); err != nil {
		t.Fatalf("codex with its own model: %v", err)
	}
	if g := chatWireOf(t, m, "w"); g.Harness != "codex" || g.Model != "gpt-6.1-sol" || !g.On {
		t.Errorf("after the save: %+v, want codex on gpt-6.1-sol", g)
	}
	// A model alone is checked against the harness already on the fleet.
	if err := save(map[string]string{"model": `"gpt-6-luna"`}); err != nil {
		t.Errorf("a codex model alone on a codex fleet: %v", err)
	}
	refused("sonnet on a codex fleet", save(map[string]string{"model": `"sonnet"`}), "model")
	if err := save(map[string]string{"effort": `"high"`}); err != nil {
		t.Errorf("codex effort high: %v", err)
	}
	refused("effort max on codex", save(map[string]string{"effort": `"max"`}), "effort")

	// Copilot takes no effort.
	if err := save(map[string]string{"harness": `"copilot"`, "model": `"gpt-5.6-sol"`, "effort": `""`}); err != nil {
		t.Fatalf("copilot with its own model: %v", err)
	}
	refused("effort low on copilot", save(map[string]string{"effort": `"low"`}), "effort")
}

func TestChatProbeSurvivesAReloadAndFollowsTheStoreItIsGiven(t *testing.T) {
	p := probes{}
	p.pass("w", "codex")
	m, _ := openWithProbes(t, withFleets(chatFleet("w", `{"harness":"codex","model":"gpt-6.1-sol"}`)), p)
	if got := chatWireOf(t, m, "w"); !got.On {
		t.Fatalf("codex with both probes passed: %+v, want on", got)
	}
	m.Reload()
	if got := chatWireOf(t, m, "w"); !got.On {
		t.Errorf("after a reload: %+v, want the probe still applied", got)
	}
	// A store that has lost its results turns it off without restarting anything.
	q := probes{}
	m.SetChatProbe(q.probe)
	if got := chatWireOf(t, m, "w"); got.On {
		t.Errorf("after the store lost its results: %+v, want off", got)
	}
}

func TestChatSaveThatNamesNoHarnessGoesThroughWhenTheStoredHarnessLapsed(t *testing.T) {
	m, _ := openWithProbes(t, withFleets(chatFleet("w", `{"harness":"codex","model":"gpt-6.1-sol"}`)), probes{})
	set := func(k, v string) error {
		return m.SetFleetFields("w", config.FleetFields{Chat: map[string]json.RawMessage{k: json.RawMessage(v)}})
	}
	if err := set("context_threshold", `300000`); err != nil {
		t.Errorf("a threshold on a fleet whose codex is no longer offered: %v", err)
	}
	if err := set("model", `"gpt-6-luna"`); err != nil {
		t.Errorf("a codex model on that fleet: %v", err)
	}
	if err := set("model", `"sonnet"`); err == nil {
		t.Error("a claude model on that fleet was accepted: it is checked against the stored harness")
	}
	if err := set("effort", `"max"`); err == nil {
		t.Error("an effort codex lacks was accepted")
	}
}
