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
	DefaultChatModel     = "sonnet"
	DefaultChatHarness   = "claude"
	DefaultChatThreshold = 150000
	// ChatNever is the threshold of a chat that is never refreshed; config.json spells it "never".
	ChatNever = 0
)

// An empty effort is the harness default.
var (
	ChatModels     = []string{"sonnet", "opus", "haiku"}
	ChatEfforts    = []string{"", "low", "medium", "high"}
	ChatThresholds = []int{100000, 150000, 300000}
	// ChatHarnesses are the harnesses whose fence is proven, a recorded assumption until the
	// MCP fence lands; a fleet's chat can run on no other.
	ChatHarnesses = []string{"claude"}
)

// ChatProbe reports a capability's status ("passed", "failed" or "not_checked") for a fleet's harness,
// with the probe's detail; config cannot import capabilities, so the server injects it.
type ChatProbe func(fleet, harness, capability string) (status, detail string)

// SetChatProbe wires the capability store into the AdmiralBot gate; it survives a reload.
func (m *Manager) SetChatProbe(p ChatProbe) {}

// ChatModelsFor is the models a harness's AdmiralBot chat may use; the first is its default.
func ChatModelsFor(harness string) []string { return nil }

// ChatEffortsFor is the efforts a harness's AdmiralBot chat may use; "" is the harness default.
func ChatEffortsFor(harness string) []string { return nil }

// ChatHarnessOption is a docked candidate harness: whether the fleet's AdmiralBot may run on it, and if not why.
type ChatHarnessOption struct {
	Harness    string `json:"harness"`
	Offered    bool   `json:"offered"`
	Reason     string `json:"reason"`
	Capability string `json:"capability"`
}

func defaultChat() ChatSettings {
	return ChatSettings{Harness: DefaultChatHarness, Model: DefaultChatModel, ContextThreshold: DefaultChatThreshold}
}

// ChatOffReason is empty when the fleet's chat is on.
func (f Fleet) ChatOffReason() string {
	if !slices.Contains(ChatHarnesses, f.Chat.Harness) {
		return fmt.Sprintf("Admiral cannot yet fence %s, so the chat does not run on it: choose Claude Code.", f.Chat.Harness)
	}
	if _, ok := f.Harness(f.Chat.Harness); !ok {
		return "This fleet has no Claude Code dock, so the chat has nowhere to run: add one to the fleet."
	}
	return ""
}

func (f Fleet) ChatOn() bool { return f.ChatOffReason() == "" }

// ChatHarnessesOffered are the proven harnesses the fleet has a dock for.
func (f Fleet) ChatHarnessesOffered() []string {
	out := []string{}
	for _, name := range ChatHarnesses {
		if _, ok := f.Harness(name); ok {
			out = append(out, name)
		}
	}
	return out
}

// ChatOffReason is why the fleet's chat is off, with the case clash only a config can see: the
// chat lives in chat/<key>/, and the Mac's disk usually ignores case. The earlier key keeps its chat.
func (c *Config) ChatOffReason(f Fleet) string {
	for _, o := range c.Fleets {
		if o.Key == f.Key {
			break
		}
		if strings.EqualFold(o.Key, f.Key) {
			return fmt.Sprintf("This fleet's key differs only in case from fleet %s's (%s), and their chats would share one directory on a disk that ignores case: rename one key.", o.Name, o.Key)
		}
	}
	return f.ChatOffReason()
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
func parseFleetChat(path string, raw json.RawMessage, strict bool) (ChatSettings, error) {
	ms, err := members(path, raw)
	if err != nil {
		return ChatSettings{}, err
	}
	c := defaultChat()
	bad := func(p, msg string, fallback any) error {
		if strict {
			return fmt.Errorf("%s: %s", p, msg)
		}
		log.Printf("config: %s: %s, using %v", p, msg, fallback)
		return nil
	}
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "harness":
			s, err := str(p, m.raw)
			if err != nil {
				return ChatSettings{}, err
			}
			ok := slices.Contains(Harnesses(), s)
			if strict {
				ok = slices.Contains(ChatHarnesses, s)
			}
			if !ok {
				if err := bad(p, fmt.Sprintf("%q is not one of %v", s, ChatHarnesses), DefaultChatHarness); err != nil {
					return ChatSettings{}, err
				}
				continue
			}
			c.Harness = s
		case "model":
			s, err := str(p, m.raw)
			if err != nil {
				return ChatSettings{}, err
			}
			if !slices.Contains(ChatModels, s) {
				if err := bad(p, fmt.Sprintf("%q is not one of %v", s, ChatModels), DefaultChatModel); err != nil {
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
			if !slices.Contains(ChatEfforts, s) {
				if err := bad(p, fmt.Sprintf("%q is not one of %q", s, ChatEfforts), "the harness default"); err != nil {
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
