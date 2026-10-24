package sessionhost

import (
	"encoding/json"
	"regexp"
	"time"
)

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

// SafeToken is what an id, a type or a reason must match to be kept: short, and
// made of characters tmux and a format leave alone. Any process in a pane can
// set its options, so a reader re-checks what a writer already checked.
var SafeToken = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

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
// subagents. Every entry is re-checked, whatever wrote it: an id that is not a
// safe token, a start that is not RFC 3339, or an id seen before drops the entry
// (the first one wins), and a type that is not a safe token reads as "". A
// reader treats the list of a dead pane as empty: the harness is gone, so its
// subagents are too.
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
