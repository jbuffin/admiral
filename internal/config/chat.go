package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strconv"
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

func defaultChat() ChatSettings {
	return ChatSettings{Harness: DefaultChatHarness, Model: DefaultChatModel, ContextThreshold: DefaultChatThreshold}
}

// ChatOffReason is empty when the fleet's chat is on.
func (f Fleet) ChatOffReason() string {
	if !slices.Contains(ChatHarnesses, f.Chat.Harness) {
		return fmt.Sprintf("Admiral cannot yet fence %s, so the chat does not run on it: choose Claude Code", f.Chat.Harness)
	}
	if _, ok := f.Harness(f.Chat.Harness); !ok {
		return "This fleet has no Claude Code dock, so the chat has nowhere to run: add one to the fleet"
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

// ChatFleet is the first fleet, in config order, whose chat is on.
func (c *Config) ChatFleet() (Fleet, bool) {
	for _, f := range c.Fleets {
		if f.ChatOn() {
			return f, true
		}
	}
	return Fleet{}, false
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
