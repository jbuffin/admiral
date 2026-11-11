package sessionhost

import (
	"encoding/json"
	"time"
)

const SubagentsOption = Prefix + "subagents"

// MaxSubagents keeps a runaway harness from growing the option.
const MaxSubagents = 64

type Subagent struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Start string `json:"start"`
}

// EncodeSubagents gives "" for an empty list, which `admiral hook` unsets the option for.
func EncodeSubagents(l []Subagent) string {
	if len(l) == 0 {
		return ""
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return ""
	}
	return string(raw)
}

// DecodeSubagents re-checks every entry, whatever wrote it, since any process in the pane can set the option.
func DecodeSubagents(s string) []Subagent {
	var l []Subagent
	if s == "" || json.Unmarshal([]byte(s), &l) != nil {
		return nil
	}
	out := make([]Subagent, 0, len(l))
	seen := map[string]bool{}
	for _, a := range l {
		if !SafeToken.MatchString(a.ID) || seen[a.ID] {
			continue
		}
		if _, err := time.Parse(time.RFC3339, a.Start); err != nil {
			continue
		}
		if !SafeToken.MatchString(a.Type) {
			a.Type = ""
		}
		seen[a.ID] = true
		out = append(out, a)
	}
	if len(out) > MaxSubagents {
		out = out[:MaxSubagents]
	}
	return out
}
