package conversation

import (
	"cmp"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// decoder turns one transcript line into items.
type decoder interface {
	line(raw []byte) (out []emission, ok bool)
}

func newDecoder(o Options) decoder {
	if o.Harness == "copilot" {
		return newCopilotDecoder(o.MaxOutput)
	}
	return newClaudeDecoder(o.MaxOutput)
}

type copilotLine struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type copilotData struct {
	Content          string          `json:"content"`
	Source           string          `json:"source"`
	AgentTaskID      string          `json:"agentTaskId"`
	ParentToolCallID string          `json:"parentToolCallId"`
	ToolCallID       string          `json:"toolCallId"`
	MessageID        string          `json:"messageId"`
	DeltaContent     string          `json:"deltaContent"`
	ToolName         string          `json:"toolName"`
	Arguments        json.RawMessage `json:"arguments"`
	ToolRequests     []struct{}      `json:"toolRequests"`
	Success          *bool           `json:"success"`
	Result           struct {
		Content string `json:"content"`
	} `json:"result"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

type copilotCall struct {
	id       string
	question bool
	summary  string
}

// copilotDecoder reads a Copilot session's events.jsonl. It allowlists the types it maps, so the
// model.* lines that repeat assistant text, hook lines and usage lines never show.
type copilotDecoder struct {
	maxOutput int
	calls     map[string]copilotCall
	turnStart time.Time // of the typed prompt the open turn answers; zero when no turn is open
	lastTools bool      // the latest top-level model call asked for tools, so the turn goes on
}

func newCopilotDecoder(maxOutput int) *copilotDecoder {
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutput
	}
	return &copilotDecoder{maxOutput: maxOutput, calls: map[string]copilotCall{}}
}

func (d *copilotDecoder) line(raw []byte) ([]emission, bool) {
	var l copilotLine
	if json.Unmarshal(raw, &l) != nil {
		return nil, false
	}
	if l.ID == "" || l.Type == "" {
		return nil, true
	}
	var data copilotData
	if json.Unmarshal(l.Data, &data) != nil || data.ParentToolCallID != "" {
		return nil, true // a subagent's work folds into its parent's tool call
	}
	at, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
	item := func(it Item) []emission {
		it.ID, it.At = l.ID, l.Timestamp
		if it.Kind == KindAssistant && data.MessageID != "" {
			it.ID = data.MessageID // the server's deltas carry only this id, so live text and the file's item are one item
		}
		return []emission{{Item: it}}
	}
	switch l.Type {
	case "user.message":
		text := strings.TrimSpace(data.Content)
		if data.Source != "" || data.AgentTaskID != "" || text == "" {
			return nil, true // the memory and agent lines are not the engineer's words
		}
		d.turnStart, d.lastTools = at, true
		return item(Item{Kind: KindUser, Text: cutTo(text, maxText, "…")}), true
	case "system.notification":
		if text := strings.TrimSpace(data.Content); text != "" {
			return item(Item{Kind: KindNotice, Text: cutTo(text, maxText, "…"), Meta: true}), true
		}
	case "session.compaction_complete":
		return item(Item{Kind: KindCompaction}), true
	case "assistant.message":
		d.lastTools = len(data.ToolRequests) > 0
		if text := strings.TrimSpace(data.Content); text != "" {
			return item(Item{Kind: KindAssistant, Text: cutTo(text, maxText, "…")}), true
		}
	case "assistant.turn_end":
		if !d.lastTools {
			return d.endTurn(l, at), true
		}
	case "session.shutdown":
		return d.endTurn(l, at), true
	case "abort", "session.error":
		return d.abort(l, at), true
	case "tool.execution_start":
		return d.toolStart(l, data, item), true
	case "tool.execution_complete":
		return d.toolComplete(data), true
	}
	return nil, true
}

func (d *copilotDecoder) endTurn(l copilotLine, at time.Time) []emission {
	if d.turnStart.IsZero() {
		return nil
	}
	var dur time.Duration
	if !at.IsZero() && at.After(d.turnStart) {
		dur = at.Sub(d.turnStart)
	}
	d.turnStart = time.Time{}
	return []emission{{Item: Item{ID: l.ID, At: l.Timestamp, Kind: KindTurnEnd, Duration: dur}}}
}

// abort ends the open turn, which Copilot records no turn end for, and settles the calls it left running.
func (d *copilotDecoder) abort(l copilotLine, at time.Time) []emission {
	out := d.endTurn(l, at)
	var open []copilotCall
	for id, c := range d.calls {
		if !c.question {
			open = append(open, c)
			delete(d.calls, id)
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].id < open[j].id })
	for _, c := range open {
		out = append(out, emission{Update: true, Item: Item{ID: c.id, Tool: &ToolCall{Done: true}}})
	}
	return out
}

func (d *copilotDecoder) toolStart(l copilotLine, data copilotData, item func(Item) []emission) []emission {
	if data.ToolCallID == "" || data.ToolName == "" {
		return nil
	}
	if len(d.calls) >= maxPending {
		d.calls = map[string]copilotCall{}
	}
	if data.ToolName == "ask_user" {
		if q, ok := copilotQuestion(data.Arguments); ok {
			q.Pending = true
			d.calls[data.ToolCallID] = copilotCall{id: l.ID, question: true}
			return item(Item{Kind: KindQuestion, Question: &q})
		}
	}
	d.calls[data.ToolCallID] = copilotCall{id: l.ID}
	return item(Item{Kind: KindToolCall, Tool: &ToolCall{
		Name:    cutTo(data.ToolName, maxToolName, "…"),
		Summary: summary(data.Arguments),
		Input:   copilotInput(data.ToolName, data.Arguments),
	}})
}

// toolComplete drops a result whose start was not seen, as when a read began mid-file.
func (d *copilotDecoder) toolComplete(data copilotData) []emission {
	c, ok := d.calls[data.ToolCallID]
	if !ok {
		return nil
	}
	delete(d.calls, data.ToolCallID)
	out := strings.TrimSpace(data.Result.Content)
	if out == "" {
		out = strings.TrimSpace(data.Error.Message)
	}
	failed := data.Success != nil && !*data.Success
	if c.question && failed {
		// Copilot refused the call and showed no form, so it is a failed tool call, not an answered question.
		return []emission{{Item: Item{ID: c.id, Kind: KindToolCall, Tool: &ToolCall{
			Name: "ask_user", Summary: c.summary, Output: cutTo(out, d.maxOutput, "… [output cut]"), Error: true, Done: true,
		}}}}
	}
	if c.question {
		answer := strings.TrimSpace(strings.TrimPrefix(out, "User responded:"))
		return []emission{{Update: true, Item: Item{ID: c.id, Question: &Question{Answer: cutTo(answer, maxText, "…")}}}}
	}
	return []emission{{Update: true, Item: Item{ID: c.id, Tool: &ToolCall{
		Output: cutTo(out, d.maxOutput, "… [output cut]"), Error: data.Success != nil && !*data.Success, Done: true,
	}}}}
}

// copilotQuestion reads ask_user's arguments in both shapes: {message, requestedSchema} and {question, choices}.
func copilotQuestion(args json.RawMessage) (Question, bool) {
	var in struct {
		Message         string   `json:"message"`
		Question        string   `json:"question"`
		Choices         []string `json:"choices"`
		RequestedSchema struct {
			Properties map[string]struct {
				Enum  []string `json:"enum"`
				OneOf []struct {
					Const string `json:"const"`
					Title string `json:"title"`
				} `json:"oneOf"`
			} `json:"properties"`
		} `json:"requestedSchema"`
	}
	if json.Unmarshal(args, &in) != nil {
		return Question{}, false
	}
	text := in.Message
	if text == "" {
		text = in.Question
	}
	if text == "" {
		return Question{}, false
	}
	q := Question{Text: cutTo(text, maxQuestionLen, "…")}
	labels := in.Choices
	// A form of several fields is not one choice, so it has no options and is answered in the terminal.
	if len(in.RequestedSchema.Properties) == 1 {
		for _, p := range in.RequestedSchema.Properties {
			labels = p.Enum
			if len(p.OneOf) > 0 {
				labels = nil
			}
			for _, o := range p.OneOf {
				labels = append(labels, cmp.Or(o.Title, o.Const))
			}
		}
	}
	for _, label := range labels {
		if len(q.Options) == maxOptions {
			break
		}
		q.Options = append(q.Options, Option{Label: cutTo(label, maxLabel, "…")})
	}
	return q, true
}

// copilotInput is what allowing the call approves: a shell command in full, or a file's path.
func copilotInput(name string, args json.RawMessage) string {
	key := "path"
	switch name {
	case "bash":
		key = "command"
	case "edit", "create", "view":
	default:
		return ""
	}
	var in map[string]json.RawMessage
	if json.Unmarshal(args, &in) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(in[key], &s) != nil {
		return ""
	}
	return cutTo(s, maxToolInput, "\n… cut")
}
