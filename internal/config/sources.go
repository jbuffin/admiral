package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// Source is one order source: a plugin and the settings it runs with.
type Source struct {
	Key      string
	Plugin   string
	Name     string          // display name; empty means the plugin's
	Settings json.RawMessage // a JSON object; nil when the file has none
	// PanelOrder lists panel IDs in the order the sidebar shows them; panels
	// it does not name follow in the plugin's order.
	PanelOrder []string
	// Autopilot holds the per-panel autopilot entries by panel id.
	Autopilot map[string]PanelAutopilot
	// autopilotRaw is the autopilot object as read, compacted. SourcesJSON
	// writes it back as it is, so a UI save adds no defaults to the file.
	autopilotRaw json.RawMessage
}

func (s Source) clone() Source {
	s.Settings = slices.Clone(s.Settings)
	s.PanelOrder = slices.Clone(s.PanelOrder)
	s.autopilotRaw = slices.Clone(s.autopilotRaw)
	if s.Autopilot != nil {
		ap := make(map[string]PanelAutopilot, len(s.Autopilot))
		for k, e := range s.Autopilot {
			e.Args = slices.Clone(e.Args)
			ap[k] = e
		}
		s.Autopilot = ap
	}
	return s
}

// NameRe is what source keys and plugin names may hold. Both end up in paths.
var NameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Source returns the source with this key.
func (c *Config) Source(key string) (Source, bool) {
	for _, s := range c.SourceList {
		if s.Key == key {
			return s, true
		}
	}
	return Source{}, false
}

// SameRun reports whether two definitions start the same process: the same
// plugin with the same settings, whatever the key order of the settings.
func (s Source) SameRun(o Source) bool {
	if s.Plugin != o.Plugin {
		return false
	}
	var a, b any
	_ = json.Unmarshal(s.Settings, &a)
	_ = json.Unmarshal(o.Settings, &b)
	return reflect.DeepEqual(a, b)
}

func parseSources(raw json.RawMessage) ([]Source, error) {
	ms, err := members("sources", raw)
	if err != nil {
		return nil, err
	}
	out := []Source{}
	for _, m := range ms {
		path := "sources." + m.key
		if !NameRe.MatchString(m.key) {
			return nil, fmt.Errorf("%s: source keys may hold only letters, digits, - and _", path)
		}
		s, err := parseSource(path, m.key, m.raw)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func parseSource(path, key string, raw json.RawMessage) (Source, error) {
	ms, err := members(path, raw)
	if err != nil {
		return Source{}, err
	}
	s := Source{Key: key}
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "plugin":
			if s.Plugin, err = str(p, m.raw); err != nil {
				return Source{}, err
			}
			if !NameRe.MatchString(s.Plugin) {
				return Source{}, fmt.Errorf("%s: plugin names may hold only letters, digits, - and _, got %q", p, s.Plugin)
			}
		case "name":
			if s.Name, err = str(p, m.raw); err != nil {
				return Source{}, err
			}
		case "settings":
			if _, err := members(p, m.raw); err != nil {
				return Source{}, err
			}
			var buf bytes.Buffer
			if err := json.Compact(&buf, m.raw); err != nil {
				return Source{}, fmt.Errorf("%s: %v", p, err)
			}
			s.Settings = buf.Bytes()
		case "panel_order":
			if s.PanelOrder, err = stringList(p, m.raw); err != nil {
				return Source{}, err
			}
		case "autopilot":
			if s.Autopilot, err = parsePanelAutopilot(p, m.raw); err != nil {
				return Source{}, err
			}
			var buf bytes.Buffer
			if err := json.Compact(&buf, m.raw); err != nil {
				return Source{}, fmt.Errorf("%s: %v", p, err)
			}
			s.autopilotRaw = buf.Bytes()
		default:
			return Source{}, fmt.Errorf("%s: unknown key", p)
		}
	}
	if s.Plugin == "" {
		return Source{}, fmt.Errorf("%s.plugin: required", path)
	}
	return s, nil
}

// SourcesJSON is the `sources` object for these definitions, in order.
func SourcesJSON(list []Source) (json.RawMessage, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, s := range list {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(s.Key)
		b.Write(k)
		b.WriteString(`:{"plugin":`)
		p, _ := json.Marshal(s.Plugin)
		b.Write(p)
		if s.Name != "" {
			n, _ := json.Marshal(s.Name)
			b.WriteString(`,"name":`)
			b.Write(n)
		}
		if s.Settings != nil {
			b.WriteString(`,"settings":`)
			b.Write(s.Settings)
		}
		if s.autopilotRaw != nil {
			b.WriteString(`,"autopilot":`)
			b.Write(s.autopilotRaw)
		}
		if len(s.PanelOrder) > 0 {
			o, _ := json.Marshal(s.PanelOrder)
			b.WriteString(`,"panel_order":`)
			b.Write(o)
		}
		b.WriteByte('}')
	}
	b.WriteByte('}')
	return json.RawMessage(b.String()), nil
}
