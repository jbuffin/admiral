package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// DefaultAutopilotMaxShips is the top-level cap when the file sets none.
const DefaultAutopilotMaxShips = 3

// PanelAutopilot is one panel's autopilot entry: which orders it launches and
// how (#114). Config can't check that the panel exists; panels come from the
// running plugin.
type PanelAutopilot struct {
	Enabled   bool
	Fleet     string
	Harness   string // claude, for now
	Skill     Skill
	Args      []string // extra harness args, after the fleet's own
	MaxShips  int      // most ships from this panel at once
	LowStakes string   // advisor or never
	// Resources names the locked resources an order of this panel needs; nil
	// when none. Each is checked against the top-level resources once the whole
	// file is parsed.
	Resources []string
}

// Skill is what a launch runs, in the kinds the launcher takes.
type Skill struct {
	Kind string // commodore, skill or none
	Name string // for kind skill
}

func parseAutopilot(raw json.RawMessage) (int, error) {
	ms, err := members("autopilot", raw)
	if err != nil {
		return 0, err
	}
	max := DefaultAutopilotMaxShips
	for _, m := range ms {
		p := "autopilot." + m.key
		switch m.key {
		case "max_ships":
			if max, err = positiveInt(p, m.raw); err != nil {
				return 0, err
			}
		default:
			return 0, fmt.Errorf("%s: unknown key", p)
		}
	}
	return max, nil
}

func parsePanelAutopilot(path string, raw json.RawMessage) (map[string]PanelAutopilot, error) {
	ms, err := members(path, raw)
	if err != nil {
		return nil, err
	}
	out := map[string]PanelAutopilot{}
	for _, m := range ms {
		if out[m.key], err = parseAutopilotEntry(path+"."+m.key, m.raw); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func parseAutopilotEntry(path string, raw json.RawMessage) (PanelAutopilot, error) {
	ms, err := members(path, raw)
	if err != nil {
		return PanelAutopilot{}, err
	}
	e := PanelAutopilot{Args: []string{}, MaxShips: 1, LowStakes: "advisor"}
	have := map[string]bool{}
	for _, m := range ms {
		p := path + "." + m.key
		have[m.key] = true
		switch m.key {
		case "enabled":
			if err := json.Unmarshal(m.raw, &e.Enabled); err != nil {
				return PanelAutopilot{}, fmt.Errorf("%s: must be true or false", p)
			}
		case "fleet":
			if e.Fleet, err = str(p, m.raw); err != nil {
				return PanelAutopilot{}, err
			}
		case "harness":
			if e.Harness, err = str(p, m.raw); err != nil {
				return PanelAutopilot{}, err
			}
			if e.Harness != "claude" {
				return PanelAutopilot{}, fmt.Errorf("%s: autopilot runs on Claude Code only for now, got %q", p, e.Harness)
			}
		case "skill":
			if e.Skill, err = parseSkill(p, m.raw); err != nil {
				return PanelAutopilot{}, err
			}
		case "args":
			if e.Args, err = stringList(p, m.raw); err != nil {
				return PanelAutopilot{}, err
			}
		case "max_ships":
			if e.MaxShips, err = positiveInt(p, m.raw); err != nil {
				return PanelAutopilot{}, err
			}
		case "resources":
			if e.Resources, err = stringList(p, m.raw); err != nil {
				return PanelAutopilot{}, err
			}
			if len(e.Resources) == 0 {
				e.Resources = nil
			}
		case "low_stakes":
			if e.LowStakes, err = str(p, m.raw); err != nil {
				return PanelAutopilot{}, err
			}
			if e.LowStakes != "advisor" && e.LowStakes != "never" {
				return PanelAutopilot{}, fmt.Errorf("%s: must be advisor or never, got %q", p, e.LowStakes)
			}
		default:
			return PanelAutopilot{}, fmt.Errorf("%s: unknown key", p)
		}
	}
	for _, k := range []string{"fleet", "harness", "skill"} {
		if !have[k] {
			return PanelAutopilot{}, fmt.Errorf("%s.%s: required", path, k)
		}
	}
	return e, nil
}

// parseSkill repeats the launcher's skill rules; config can't import it.
func parseSkill(path string, raw json.RawMessage) (Skill, error) {
	ms, err := members(path, raw)
	if err != nil {
		return Skill{}, err
	}
	var s Skill
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "kind":
			if s.Kind, err = str(p, m.raw); err != nil {
				return Skill{}, err
			}
		case "name":
			if s.Name, err = str(p, m.raw); err != nil {
				return Skill{}, err
			}
		default:
			return Skill{}, fmt.Errorf("%s: unknown key", p)
		}
	}
	switch s.Kind {
	case "commodore", "none":
	case "skill":
		name := strings.TrimPrefix(strings.TrimPrefix(s.Name, "/"), "$")
		if name == "" {
			return Skill{}, fmt.Errorf("%s.name: required for kind skill", path)
		}
		if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
			return Skill{}, fmt.Errorf("%s.name: must not contain whitespace, got %q", path, s.Name)
		}
	default:
		return Skill{}, fmt.Errorf("%s.kind: must be commodore, skill or none, got %q", path, s.Kind)
	}
	return s, nil
}

func positiveInt(path string, raw json.RawMessage) (int, error) {
	n, err := strconv.Atoi(string(bytes.TrimSpace(raw)))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s: must be a positive integer, got %s", path, raw)
	}
	return n, nil
}

// checkAutopilotFleets runs after the fleets are parsed: every entry must name
// a fleet that has a claude dock.
func checkAutopilotFleets(cfg *Config) error {
	for _, s := range cfg.SourceList {
		for panel, e := range s.Autopilot {
			path := fmt.Sprintf("sources.%s.autopilot.%s.fleet", s.Key, panel)
			var fleet *Fleet
			for i := range cfg.Fleets {
				if cfg.Fleets[i].Key == e.Fleet {
					fleet = &cfg.Fleets[i]
				}
			}
			if fleet == nil {
				return fmt.Errorf("%s: no fleet %q in fleets", path, e.Fleet)
			}
			hasClaude := false
			for _, h := range fleet.Harnesses {
				hasClaude = hasClaude || h.Name == "claude"
			}
			if !hasClaude {
				return fmt.Errorf("%s: fleet %q has no claude dock", path, e.Fleet)
			}
		}
	}
	return nil
}
