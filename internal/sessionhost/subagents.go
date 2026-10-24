package sessionhost

// SubagentsOption is the pane option `admiral hook` keeps the subagents of a
// ship's harness in: a JSON array of Subagent (ADR 0014).
const SubagentsOption = Prefix + "subagents"

// Subagent is one running subagent of a ship: the harness's id for it, its
// type, and when its start hook ran (RFC 3339).
type Subagent struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Start string `json:"start"`
}
