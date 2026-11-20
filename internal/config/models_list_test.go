package config_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

// Each fleet's docks serve the models their harness listed, or the built-in list until it has.

type lists map[string][]string

func (l lists) models(fleet, harness string) ([]string, bool) {
	m, ok := l[fleet+"/"+harness]
	return m, ok
}

type dockWire struct {
	Harness      string   `json:"harness"`
	Models       []string `json:"models"`
	ModelsSource string   `json:"models_source"`
}

func dockWireOf(t *testing.T, m *config.Manager, fleet, harness string) dockWire {
	t.Helper()
	for _, d := range fleetOf(m.View(), fleet).Docks {
		if d.Harness != harness {
			continue
		}
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		var out dockWire
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	t.Fatalf("fleet %s has no %s dock", fleet, harness)
	return dockWire{}
}

type chatModelsWire struct {
	Model          string   `json:"model"`
	Models         []string `json:"models"`
	HarnessOptions []struct {
		Harness string   `json:"harness"`
		Models  []string `json:"models"`
	} `json:"harness_options"`
}

func chatModelsOf(t *testing.T, m *config.Manager, fleet string) chatModelsWire {
	t.Helper()
	raw, err := json.Marshal(fleetOf(m.View(), fleet).Chat)
	if err != nil {
		t.Fatal(err)
	}
	var out chatModelsWire
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDockViewServesTheBuiltInListsWithFableForClaudeUntilAListingExists(t *testing.T) {
	m, _ := openView(t, joinFleets(chatFleet("w", "")))
	for _, c := range []struct {
		harness string
		models  []string
	}{
		{"claude", []string{"sonnet", "opus", "haiku", "fable"}},
		{"codex", []string{"gpt-6.1-sol", "gpt-6-luna"}},
		{"copilot", []string{"gpt-5.6-sol", "gpt-5.6-luna"}},
	} {
		d := dockWireOf(t, m, "w", c.harness)
		if !sameSet(d.Models, c.models) || d.ModelsSource != "built_in" {
			t.Errorf("%s dock = models %v source %q, want the built-in %v", c.harness, d.Models, d.ModelsSource, c.models)
		}
	}
}

func TestDockViewServesAListedListAndTheChatListsFollowIt(t *testing.T) {
	m, _ := openView(t, joinFleets(chatFleet("w", ""), chatFleet("v", "")))
	l := lists{
		"w/claude":  {"sonnet", "opus[1m]", "claude-fable-5-1"},
		"w/copilot": {"auto", "claude-opus-5.5", "gpt-6.2-sol", "gemini-4-carbon", "grok-4.7", "kimi-k3"},
	}
	m.SetHarnessModels(l.models)

	if d := dockWireOf(t, m, "w", "claude"); !slices.Equal(d.Models, l["w/claude"]) || d.ModelsSource != "listed" {
		t.Errorf("w claude dock = %v %q, want the listed list", d.Models, d.ModelsSource)
	}
	if d := dockWireOf(t, m, "w", "copilot"); !slices.Equal(d.Models, l["w/copilot"]) || d.ModelsSource != "listed" {
		t.Errorf("w copilot dock = %v %q, want all six listed models", d.Models, d.ModelsSource)
	}
	if d := dockWireOf(t, m, "w", "codex"); d.ModelsSource != "built_in" {
		t.Errorf("w codex dock source = %q, want built_in: nothing was listed for it", d.ModelsSource)
	}
	if d := dockWireOf(t, m, "v", "claude"); d.ModelsSource != "built_in" || !slices.Contains(d.Models, "fable") {
		t.Errorf("v claude dock = %v %q, want the built-in list: only fleet w was listed", d.Models, d.ModelsSource)
	}

	c := chatModelsOf(t, m, "w")
	if !slices.Equal(c.Models, l["w/claude"]) {
		t.Errorf("w chat.models = %v, want the claude dock's list %v", c.Models, l["w/claude"])
	}
	for _, o := range c.HarnessOptions {
		if o.Harness == "copilot" && !slices.Equal(o.Models, l["w/copilot"]) {
			t.Errorf("w copilot harness option models = %v, want %v", o.Models, l["w/copilot"])
		}
	}
	if c.Model != "sonnet" {
		t.Errorf("w chat model = %q, want the default sonnet whatever was listed", c.Model)
	}
	if v := chatModelsOf(t, m, "v"); !slices.Contains(v.Models, "fable") || slices.Contains(v.Models, "opus[1m]") {
		t.Errorf("v chat.models = %v, want its own built-in list", v.Models)
	}
}

func TestListedModelsSurviveAReloadAndTheChatDefaultStaysWhenTheListLacksIt(t *testing.T) {
	m, _ := openView(t, joinFleets(chatFleet("w", "")))
	m.SetHarnessModels(lists{"w/claude": {"claude-fable-5-1"}}.models)
	m.Reload()
	if d := dockWireOf(t, m, "w", "claude"); !slices.Equal(d.Models, []string{"claude-fable-5-1"}) || d.ModelsSource != "listed" {
		t.Errorf("after a reload the dock = %v %q, want the listed list", d.Models, d.ModelsSource)
	}
	if c := chatModelsOf(t, m, "w"); c.Model != "sonnet" {
		t.Errorf("chat model = %q, want sonnet though the list lacks it", c.Model)
	}
}

func TestChatLoadKeepsAnyWellFormedModelNameAndFallsBackOnAMalformedOne(t *testing.T) {
	m, _ := openView(t, joinFleets(
		chatFleet("a", `{"model":"claude-fable-5-1"}`), chatFleet("b", `{"model":"opus[1m]"}`),
		chatFleet("c", `{"model":"gpt-6.1-sol"}`), chatFleet("d", `{"model":"a b"}`),
		chatFleet("e", `{"model":"opus[a-b]"}`), chatFleet("f", `{"harness":"copilot","model":"claude-opus-5.5"}`)))
	for _, c := range []struct{ key, want string }{
		{"a", "claude-fable-5-1"}, {"b", "opus[1m]"}, {"c", "gpt-6.1-sol"}, {"d", "sonnet"}, {"e", "sonnet"}, {"f", "claude-opus-5.5"},
	} {
		if g := chatModelsOf(t, m, c.key); g.Model != c.want {
			t.Errorf("fleet %s chat model = %q, want %q (the list is not visible while loading, so the name pattern decides)", c.key, g.Model, c.want)
		}
	}
}

func TestChatSaveChecksTheModelAgainstTheFleetsCurrentList(t *testing.T) {
	p := probes{}
	p.pass("w", "copilot")
	m, _ := openWithProbes(t, joinFleets(chatFleet("w", ""), chatFleet("v", "")), p)
	m.SetHarnessModels(lists{
		"w/claude":  {"sonnet", "claude-fable-5-1"},
		"w/copilot": {"auto", "claude-opus-5.5", "grok-4.7"},
		"v/claude":  {"sonnet", "opus"},
	}.models)
	save := func(fleet string, chat map[string]string) error {
		raw := map[string]json.RawMessage{}
		for k, v := range chat {
			raw[k] = json.RawMessage(v)
		}
		return m.SetFleetFields(fleet, config.FleetFields{Chat: raw})
	}

	if err := save("w", map[string]string{"model": `"claude-fable-5-1"`}); err != nil {
		t.Errorf("a listed model on fleet w: %v", err)
	}
	if g := chatModelsOf(t, m, "w"); g.Model != "claude-fable-5-1" {
		t.Errorf("w chat model after the save = %q, want claude-fable-5-1", g.Model)
	}
	if err := save("v", map[string]string{"model": `"claude-fable-5-1"`}); err == nil {
		t.Error("the same model on fleet v, whose list lacks it, was accepted")
	}
	if err := save("w", map[string]string{"model": `"opus"`}); err == nil {
		t.Error("a well-formed model that fleet w's list lacks was accepted")
	}
	if err := save("w", map[string]string{"model": `"opus[1m]"`}); err == nil {
		t.Error("a well-formed model that fleet w's list lacks was accepted")
	}
	// A harness switch checks the model against the new harness's list for that fleet.
	if err := save("w", map[string]string{"harness": `"copilot"`, "model": `"grok-4.7"`, "effort": `""`}); err != nil {
		t.Errorf("a listed copilot model on a copilot switch: %v", err)
	}
	if err := save("w", map[string]string{"model": `"gpt-6.2-sol"`}); err == nil {
		t.Error("a copilot model fleet w's copilot list lacks was accepted")
	}
}

func TestChatSaveOfTheBuiltInFallbackModelWorksBeforeAnythingIsListed(t *testing.T) {
	m, _ := openView(t, joinFleets(chatFleet("w", "")))
	if err := m.SetFleetFields("w", config.FleetFields{Chat: map[string]json.RawMessage{"model": json.RawMessage(`"fable"`)}}); err != nil {
		t.Errorf("fable on a claude fleet with no list: %v", err)
	}
}

func TestNotifyModelsChangedFiresSubscribersAndTheViewShowsTheNewList(t *testing.T) {
	m, _ := openView(t, joinFleets(chatFleet("w", "")))
	current := []string{"sonnet"}
	m.SetHarnessModels(func(fleet, harness string) ([]string, bool) {
		if fleet == "w" && harness == "claude" {
			return current, true
		}
		return nil, false
	})
	changed, unsubscribe := m.Subscribe()
	defer unsubscribe()
	select { // a reload from the setup above may have queued one
	case <-changed:
	default:
	}
	current = []string{"sonnet", "fable"}
	m.NotifyModelsChanged()
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("a changed list fired no subscriber, so no config message would reach a browser")
	}
	if d := dockWireOf(t, m, "w", "claude"); !slices.Equal(d.Models, []string{"sonnet", "fable"}) {
		t.Errorf("view models = %v, want the new list", d.Models)
	}
}

func TestValidModelName(t *testing.T) {
	for _, ok := range []string{"opus[1m]", "claude-fable-5-2", "gpt-5.6-sol", "sonnet", "claude-opus-5.5", "o3", "a:b"} {
		if !config.ValidModelName(ok) {
			t.Errorf("%q is refused, want a valid model name", ok)
		}
	}
	for _, bad := range []string{"", "-x", "--model", "a b", "opus[1m", "opus[1m]x", "opus[a-b]", "$(x)", "opus[]", "opus[1m][2m]", "a;b", "a\nb"} {
		if config.ValidModelName(bad) {
			t.Errorf("%q is accepted, want it refused", bad)
		}
	}
}
