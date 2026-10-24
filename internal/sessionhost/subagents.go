package sessionhost

import "encoding/json"

// SubagentsOption is the pane option `admiral hook` keeps the subagents of a
// ship's harness in: a JSON array of Subagent (ADR 0014).
const SubagentsOption = Prefix + "subagents"

// MaxSubagents caps the list, so a runaway harness can't grow the option.
const MaxSubagents = 64

// Subagent is one running subagent of a ship: the harness's id for it, its
// type, and when its start hook ran (RFC 3339).
type Subagent struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Start string `json:"start"`
}

// EncodeSubagents is the value of SubagentsOption: a JSON array, or "" for an
// empty list, which `admiral hook` unsets the option for.
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

// DecodeSubagents reads the option's value. An unset or unreadable value is no
// subagents, and an entry without an id is dropped. A reader treats the list of
// a dead pane as empty: the harness is gone, so its subagents are too.
func DecodeSubagents(s string) []Subagent {
	var l []Subagent
	if s == "" || json.Unmarshal([]byte(s), &l) != nil {
		return nil
	}
	out := l[:0]
	for _, a := range l {
		if a.ID != "" {
			out = append(out, a)
		}
	}
	if len(out) > MaxSubagents {
		out = out[:MaxSubagents]
	}
	return out
}
