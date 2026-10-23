package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
)

// Resource is a named thing with a capacity that a panel's autopilot entry can
// need (#118). A ship holds one unit of each resource its entry named until it
// ends.
type Resource struct {
	Capacity int      // units; 1 or more, 1 when the file sets none
	Check    []string // argv, run with no shell; exit 0 means present. Nil when none
}

// resourceNameRe is what a resource name may hold.
var resourceNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func parseResources(raw json.RawMessage) (map[string]Resource, error) {
	ms, err := members("resources", raw)
	if err != nil {
		return nil, err
	}
	out := map[string]Resource{}
	for _, m := range ms {
		path := "resources." + m.key
		if !resourceNameRe.MatchString(m.key) {
			return nil, fmt.Errorf("%s: resource names may hold only letters, digits, . - and _, up to 64 characters", path)
		}
		if out[m.key], err = parseResource(path, m.raw); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func parseResource(path string, raw json.RawMessage) (Resource, error) {
	ms, err := members(path, raw)
	if err != nil {
		return Resource{}, err
	}
	r := Resource{Capacity: 1}
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "capacity":
			if r.Capacity, err = positiveInt(p, m.raw); err != nil {
				return Resource{}, err
			}
		case "check":
			if r.Check, err = stringList(p, m.raw); err != nil {
				return Resource{}, err
			}
			if len(r.Check) == 0 {
				return Resource{}, fmt.Errorf("%s: must not be empty", p)
			}
			for i, a := range r.Check {
				if a == "" {
					return Resource{}, fmt.Errorf("%s[%d]: must not be empty", p, i)
				}
			}
		default:
			return Resource{}, fmt.Errorf("%s: unknown key", p)
		}
	}
	return r, nil
}

// checkPanelResources runs after the whole file is parsed: every resource an
// autopilot entry names must be defined at the top level.
func checkPanelResources(cfg *Config) error {
	for _, s := range cfg.SourceList {
		panels := make([]string, 0, len(s.Autopilot))
		for panel := range s.Autopilot {
			panels = append(panels, panel)
		}
		sort.Strings(panels)
		for _, panel := range panels {
			for _, name := range s.Autopilot[panel].Resources {
				if _, ok := cfg.Resources[name]; !ok {
					return fmt.Errorf("sources.%s.autopilot.%s.resources: no resource %q in resources", s.Key, panel, name)
				}
			}
		}
	}
	return nil
}

func (c *Config) cloneResources() map[string]Resource {
	if c.Resources == nil {
		return nil
	}
	out := make(map[string]Resource, len(c.Resources))
	for k, r := range c.Resources {
		r.Check = slices.Clone(r.Check)
		out[k] = r
	}
	return out
}
