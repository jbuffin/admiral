package sessionhost

import (
	"encoding/json"
	"sort"
	"time"
)

const SubagentsOption = Prefix + "subagents"

// MaxSubagents keeps a runaway harness from growing the option.
const MaxSubagents = 64

// Kinds an entry can have; an empty kind reads as KindSubagent, so lists written before kinds existed still decode.
const (
	KindSubagent = "subagent"
	KindTeammate = "teammate"
	KindShell    = "shell"
	KindMonitor  = "monitor"
)

// A Subagent entry is running until it has an End.
type Subagent struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Start string `json:"start"`
	Kind  string `json:"kind,omitempty"`
	End   string `json:"end,omitempty"`
}

func validKind(k string) bool {
	switch k {
	case "", KindSubagent, KindTeammate, KindShell, KindMonitor:
		return true
	}
	return false
}

// AddSubagent appends e, dropping the finished entry that ended first when the list is full; a list of 64 running entries refuses it.
func AddSubagent(l []Subagent, e Subagent) []Subagent {
	return TrimSubagents(append(append([]Subagent(nil), l...), e))
}

// TrimSubagents cuts a list to MaxSubagents in one pass: the earliest-ended finished entries go first, then running ones from the tail (the newest).
func TrimSubagents(l []Subagent) []Subagent {
	excess := len(l) - MaxSubagents
	if excess <= 0 {
		return l
	}
	var fin []int
	for i, a := range l {
		if a.End != "" {
			fin = append(fin, i)
		}
	}
	sort.SliceStable(fin, func(x, y int) bool { return l[fin[x]].End < l[fin[y]].End })
	if len(fin) > excess {
		fin = fin[:excess]
	}
	drop := make(map[int]bool, len(fin))
	for _, i := range fin {
		drop[i] = true
	}
	out := make([]Subagent, 0, MaxSubagents)
	for i, a := range l {
		if !drop[i] {
			out = append(out, a)
		}
	}
	return out[:min(len(out), MaxSubagents)]
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
		if !SafeToken.MatchString(a.ID) || seen[a.ID] || !validKind(a.Kind) {
			continue
		}
		if a.End != "" {
			if _, err := time.Parse(time.RFC3339, a.End); err != nil {
				continue
			}
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
	return TrimSubagents(out)
}
