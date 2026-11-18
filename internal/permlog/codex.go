package permlog

import (
	"encoding/json"
	"strings"
)

// CodexTracker turns a Codex ship's app-server frames into log events. A request for approval reaches the TUI only
// when the reviewer left it to a person, so its answer is a person's; the reviewer's own verdicts come as reviews.
// It is not safe for concurrent use.
type CodexTracker struct {
	pending map[string]string // item id -> the ask waiting on a person
}

func NewCodexTracker() *CodexTracker { return &CodexTracker{pending: map[string]string{}} }

const maxCodexPending = 64

// Frame reads one frame and gives the events it makes.
func (t *CodexTracker) Frame(raw []byte) []Event {
	var f struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(raw, &f) != nil || f.Method == "" {
		return nil
	}
	var p struct {
		ItemID string `json:"itemId"`
		Item   struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"item"`
		ReviewID string `json:"reviewId"`
		Review   struct {
			Status string `json:"status"`
		} `json:"review"`
		Action struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Program string   `json:"program"`
			Argv    []string `json:"argv"`
			Cwd     string   `json:"cwd"`
		} `json:"action"`
	}
	_ = json.Unmarshal(f.Params, &p)
	switch {
	case strings.HasSuffix(f.Method, "/requestApproval"):
		call, ok := CodexApproval(f.Method, f.Params)
		if !ok || p.ItemID == "" || len(t.pending) >= maxCodexPending {
			return nil
		}
		id := "codex-" + p.ItemID + "-" + strings.Trim(string(f.ID), `"`)
		t.pending[p.ItemID] = id
		return []Event{{Ev: "ask", At: Now(), ID: id, Call: &call}}
	case f.Method == "item/completed":
		id, ok := t.pending[p.Item.ID]
		if !ok {
			return nil
		}
		delete(t.pending, p.Item.ID)
		ans := Allow
		if p.Item.Status == "declined" {
			ans = Deny
		}
		return []Event{{Ev: "answer", At: Now(), ID: id, Answer: ans, By: Person}}
	case f.Method == "turn/completed":
		// A turn that ends with a prompt still open was stopped from the prompt.
		var out []Event
		for item, id := range t.pending {
			out = append(out, Event{Ev: "answer", At: Now(), ID: id, Answer: Deny, By: Person})
			delete(t.pending, item)
		}
		return out
	case f.Method == "item/autoApprovalReview/completed":
		var ans Answer
		by := Reviewer
		switch p.Review.Status {
		case "approved":
			ans = Allow
		case "denied":
			ans = Deny
		case "timedOut":
			ans, by = Deny, Timeout
		default:
			return nil
		}
		if p.ReviewID == "" {
			return nil
		}
		call := Call{Tool: "Bash", Kind: "shell", Command: p.Action.Command, Cwd: p.Action.Cwd}
		switch p.Action.Type {
		case "command":
		case "execve":
			call.Command = strings.Join(p.Action.Argv, " ")
			if call.Command == "" {
				call.Command = p.Action.Program
			}
		default:
			call = Call{Tool: p.Action.Type, Kind: "other", Cwd: p.Action.Cwd}
		}
		id := "codex-review-" + p.ReviewID
		now := Now()
		return []Event{{Ev: "ask", At: now, ID: id, Call: &call}, {Ev: "answer", At: now, ID: id, Answer: ans, By: by}}
	}
	return nil
}
