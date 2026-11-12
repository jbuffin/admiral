package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
)

// ChatSettings are one fleet's AdmiralBot chat settings, resolved: every field holds
// a usable value. Effort is empty for the harness default, which config.json leaves out.
type ChatSettings struct {
	Harness string
	Model   string
	Effort  string
	// ContextThreshold is a token count from ChatThresholds, or ChatNever.
	ContextThreshold int
}

const (
	DefaultChatHarness   = "claude"
	DefaultChatThreshold = 150000
	// ChatNever is the threshold of a chat that is never refreshed; config.json spells it "never".
	ChatNever = 0
)

var ChatThresholds = []int{100000, 150000, 300000}

// modelPairs are each harness's small model for the Lookout and the stronger one for every
// other call. Codex's pair comes from `codex debug models`, Copilot's from the names
// `copilot --model` accepted on a real call.
var modelPairs = map[string][2]string{
	"claude":  {"haiku", "sonnet"},
	"codex":   {"gpt-6-luna", "gpt-6.1-sol"},
	"copilot": {"gpt-5.6-luna", "gpt-5.6-sol"},
}

// ModelPair returns a harness's small and strong model; ok is false for a harness with none.
func ModelPair(harness string) (small, strong string, ok bool) {
	pair, ok := modelPairs[harness]
	return pair[0], pair[1], ok
}

// ChatModelsFor is the models a harness's AdmiralBot chat may use; the first is its default.
func ChatModelsFor(harness string) []string {
	small, strong, ok := ModelPair(harness)
	switch {
	case harness == "claude":
		return []string{"sonnet", "opus", "haiku"}
	case ok:
		return []string{strong, small}
	}
	return nil
}

// ChatEffortsFor is the efforts a harness's AdmiralBot chat may use; "" is the harness default,
// and Copilot takes no other.
func ChatEffortsFor(harness string) []string {
	if harness == "copilot" {
		return []string{""}
	}
	return []string{"", "low", "medium", "high"}
}

func chatDefaultModel(harness string) string { return ChatModelsFor(harness)[0] }

// ChatProbe reports a capability's status ("passed", "failed" or "not_checked") for a fleet's harness,
// with the probe's detail; config cannot import capabilities, so the server injects it.
type ChatProbe func(fleet, harness, capability string) (status, detail string)

// SetChatProbe wires the capability store into the AdmiralBot gate; it survives a reload.
func (m *Manager) SetChatProbe(p ChatProbe) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.probe = p
	m.cur.ChatProbe = p
}

// ChatHarnessOption is a docked candidate harness: whether the fleet's AdmiralBot may run on it, and if not why.
type ChatHarnessOption struct {
	Harness    string `json:"harness"`
	Offered    bool   `json:"offered"`
	Reason     string `json:"reason"`
	Capability string `json:"capability"`
	// Models and Efforts are what the harness's chat may use, so Preferences can show them before the harness is chosen.
	Models  []string `json:"models"`
	Efforts []string `json:"efforts"`
}

// The capabilities of the capability table's "bot" feature, by the ids and labels it serves.
const (
	capMCPFence   = "bot.mcp_fence"
	capConfigHome = "bot.config_home"
)

var capLabels = map[string]string{capMCPFence: "MCP-only fence", capConfigHome: "Separate config home"}

// HarnessName is how the UI and its reasons name a harness.
func HarnessName(h string) string {
	switch h {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	case "copilot":
		return "Copilot"
	}
	return h
}

func defaultChat() ChatSettings {
	return ChatSettings{Harness: DefaultChatHarness, Model: chatDefaultModel(DefaultChatHarness), ContextThreshold: DefaultChatThreshold}
}

// chatHarnessOption decides whether the fleet's AdmiralBot may run on a harness: the one place a
// capability counts. Claude counts until its fence probe fails; Codex and Copilot need the fence
// and the separate config home both passed.
func (c *Config) chatHarnessOption(f Fleet, harness string) ChatHarnessOption {
	o := ChatHarnessOption{Harness: harness, Models: ChatModelsFor(harness), Efforts: ChatEffortsFor(harness)}
	if _, ok := f.Harness(harness); !ok {
		name := HarnessName(harness)
		if harness == "claude" {
			name = "Claude Code"
		}
		o.Reason = fmt.Sprintf("This fleet has no %s dock, so the chat has nowhere to run: add one to the fleet.", name)
		return o
	}
	needs := []string{capMCPFence}
	if harness != "claude" {
		needs = append(needs, capConfigHome)
	}
	for _, id := range needs {
		status, detail := "not_checked", ""
		if c.ChatProbe != nil {
			status, detail = c.ChatProbe(f.Key, harness, id)
		}
		if status == "passed" || (harness == "claude" && status != "failed") {
			continue
		}
		o.Capability = id
		if status == "failed" {
			o.Reason = fmt.Sprintf("%s's %s failed", HarnessName(harness), capLabels[id])
			if detail != "" {
				o.Reason += ": " + detail
			}
			o.Reason += "."
		} else {
			o.Reason = fmt.Sprintf("%s's %s has not been checked yet.", HarnessName(harness), capLabels[id])
		}
		return o
	}
	o.Offered = true
	return o
}

// chatHarnessOrder is the picker's order, which is not the docks': Claude, then the two newer harnesses by name.
var chatHarnessOrder = []string{"claude", "codex", "copilot"}

// ChatHarnessOptions has one entry for each candidate harness the fleet has a dock for.
func (c *Config) ChatHarnessOptions(f Fleet) []ChatHarnessOption {
	out := []ChatHarnessOption{}
	for _, name := range chatHarnessOrder {
		if _, ok := f.Harness(name); ok {
			out = append(out, c.chatHarnessOption(f, name))
		}
	}
	return out
}

// ChatHarnessesOffered are the harnesses the fleet's AdmiralBot may run on.
func (c *Config) ChatHarnessesOffered(f Fleet) []string { return offered(c.ChatHarnessOptions(f)) }

func offered(opts []ChatHarnessOption) []string {
	out := []string{}
	for _, o := range opts {
		if o.Offered {
			out = append(out, o.Harness)
		}
	}
	return out
}

// KeyClash is why the fleet's key can't have a chat, "" when it can: the chat lives in
// chat/<key>/, and the Mac's disk usually ignores case. The earlier key keeps its chat.
func (c *Config) KeyClash(f Fleet) string {
	for _, o := range c.Fleets {
		if o.Key == f.Key {
			break
		}
		if strings.EqualFold(o.Key, f.Key) {
			return fmt.Sprintf("This fleet's key differs only in case from fleet %s's (%s), and their chats would share one directory on a disk that ignores case: rename one key.", o.Name, o.Key)
		}
	}
	return ""
}

// ChatOffReason is why the fleet's chat is off, with the case clash only a config can see.
func (c *Config) ChatOffReason(f Fleet) string { return c.chatOffReason(f, nil) }

// chatOffReason takes the fleet's options when the caller already has them, so the store is not read again.
func (c *Config) chatOffReason(f Fleet, opts []ChatHarnessOption) string {
	if r := c.KeyClash(f); r != "" {
		return r
	}
	for _, o := range opts {
		if o.Harness == f.Chat.Harness {
			return o.Reason
		}
	}
	return c.chatHarnessOption(f, f.Chat.Harness).Reason
}

// ChatFleet is the fleet with the key, and why its chat is off: "" when it is on.
func (c *Config) ChatFleet(key string) (f Fleet, off string, ok bool) {
	f, ok = c.Fleet(key)
	if !ok {
		return Fleet{}, "", false
	}
	return f, c.ChatOffReason(f), true
}

// parseFleetChat reads a fleet's chat object. Leniently (the file) a bad model, effort,
// threshold or harness name falls back to the default with a warning and never fails the
// load; strictly (a save) it is an error. An unknown key or a wrong type is always one.
// The harness is resolved first, since the model and effort are checked against it; a
// save that names none is checked against base, the fleet's harness today.
func parseFleetChat(path string, raw json.RawMessage, strict bool, base string) (ChatSettings, error) {
	ms, err := members(path, raw)
	if err != nil {
		return ChatSettings{}, err
	}
	c := defaultChat()
	c.Harness = base
	bad := func(p, msg string, fallback any) error {
		if strict {
			return fmt.Errorf("%s: %s", p, msg)
		}
		log.Printf("config: %s: %s, using %v", p, msg, fallback)
		return nil
	}
	for _, m := range ms {
		if m.key != "harness" {
			continue
		}
		s, err := str(path+".harness", m.raw)
		if err != nil {
			return ChatSettings{}, err
		}
		if !slices.Contains(Harnesses(), s) {
			if err := bad(path+".harness", fmt.Sprintf("%q is not one of %v", s, Harnesses()), base); err != nil {
				return ChatSettings{}, err
			}
			continue
		}
		c.Harness = s
	}
	c.Model = chatDefaultModel(c.Harness)
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "harness":
		case "model":
			s, err := str(p, m.raw)
			if err != nil {
				return ChatSettings{}, err
			}
			if models := ChatModelsFor(c.Harness); !slices.Contains(models, s) {
				if err := bad(p, fmt.Sprintf("%q is not one of %v for %s", s, models, c.Harness), c.Model); err != nil {
					return ChatSettings{}, err
				}
				continue
			}
			c.Model = s
		case "effort":
			s, err := str(p, m.raw)
			if err != nil {
				return ChatSettings{}, err
			}
			if efforts := ChatEffortsFor(c.Harness); !slices.Contains(efforts, s) {
				if err := bad(p, fmt.Sprintf("%q is not one of %q for %s", s, efforts, c.Harness), "the harness default"); err != nil {
					return ChatSettings{}, err
				}
				continue
			}
			c.Effort = s
		case "context_threshold":
			n, ok := parseThreshold(m.raw)
			if !ok {
				if err := bad(p, fmt.Sprintf("%s is not one of %v or \"never\"", bytes.TrimSpace(m.raw), ChatThresholds), DefaultChatThreshold); err != nil {
					return ChatSettings{}, err
				}
				continue
			}
			c.ContextThreshold = n
		default:
			return ChatSettings{}, fmt.Errorf("%s: unknown key", p)
		}
	}
	return c, nil
}

func parseThreshold(raw json.RawMessage) (int, bool) {
	raw = bytes.TrimSpace(raw)
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return ChatNever, s == "never"
	}
	n, err := strconv.Atoi(string(raw))
	return n, err == nil && slices.Contains(ChatThresholds, n)
}

// ThresholdJSON is the threshold as config.json and the config view spell it.
func (c ChatSettings) ThresholdJSON() any {
	if c.ContextThreshold == ChatNever {
		return "never"
	}
	return c.ContextThreshold
}
