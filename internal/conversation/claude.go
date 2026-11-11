package conversation

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The transcript is model output and can be hostile, so every field is cut and every list capped.
const (
	maxText        = 64 << 10 // a message's text, a question's answer
	maxToolName    = 128
	maxToolInput   = 4 << 10 // what a permission card shows of a call
	maxQuestionLen = 4 << 10
	maxLabel       = 256
	maxDescription = 2 << 10
	maxQuestions   = 8         // per AskUserQuestion call
	maxOptions     = 16        // per question
	maxLineItems   = 50        // items one line can make, and tool results it can fold
	maxPending     = 1000      // calls held while they wait for a result
	maxPendingSize = 128 << 10 // bytes of what is held for them

	// DefaultMaxOutput is how much of a tool's output an item carries.
	DefaultMaxOutput = 16 << 10
)

var noticePrefixes = []string{"<task-notification>", "Another Claude session sent a message"}

// claudeLine is the part of a transcript line Admiral reads; the format is undocumented, so the rest is ignored.
type claudeLine struct {
	Type                      string `json:"type"`
	Subtype                   string `json:"subtype"`
	UUID                      string `json:"uuid"`
	Timestamp                 string `json:"timestamp"`
	IsMeta                    bool   `json:"isMeta"`
	IsSidechain               bool   `json:"isSidechain"`
	IsCompactSummary          bool   `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool   `json:"isVisibleInTranscriptOnly"`
	DurationMs                int64  `json:"durationMs"`
	Message                   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

// emission is an item the decoder hands on; an update carries only the id and what a result fills in.
type emission struct {
	Item   Item
	Update bool
}

type pendingCall struct {
	ids       []string
	questions []string // parallel to ids; "" for a tool call
	size      int
}

type claudeDecoder struct {
	maxOutput int
	calls     map[string]pendingCall
	order     []string // tool_use ids, oldest first, to evict by age
	held      int      // bytes of calls
}

func newClaudeDecoder(maxOutput int) *claudeDecoder {
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutput
	}
	return &claudeDecoder{maxOutput: maxOutput, calls: map[string]pendingCall{}}
}

func (d *claudeDecoder) reset() {
	d.calls, d.order, d.held = map[string]pendingCall{}, nil, 0
}

func (d *claudeDecoder) remember(id string, p pendingCall) {
	if old, ok := d.calls[id]; ok {
		d.held -= old.size
	} else {
		d.order = append(d.order, id)
	}
	d.calls[id] = p
	d.held += p.size
	for len(d.order) > 0 && (len(d.calls) > maxPending || d.held > maxPendingSize) {
		oldest := d.order[0]
		d.order = d.order[1:]
		if p, ok := d.calls[oldest]; ok {
			d.held -= p.size
			delete(d.calls, oldest)
		}
	}
	if len(d.order) > 2*maxPending+16 { // ids of folded calls linger in order
		live := d.order[:0]
		for _, id := range d.order {
			if _, ok := d.calls[id]; ok {
				live = append(live, id)
			}
		}
		d.order = live
	}
}

type lineItems struct {
	uuid, at string
	n        int // items made by add; updates folded in don't count
	out      []emission
}

func (b *lineItems) full() bool { return b.n >= maxLineItems }

// add gives the first item the line's uuid and the rest a suffixed form, which a uuid can't collide with.
func (b *lineItems) add(it Item) string {
	it.At = b.at
	it.ID = b.uuid
	if b.n > 0 {
		it.ID = b.uuid + "#" + strconv.Itoa(b.n)
	}
	b.n++
	b.out = append(b.out, emission{Item: it})
	return it.ID
}

// line decodes one line; ok is false for a non-JSON line, which tells an unreadable file from an empty one.
func (d *claudeDecoder) line(raw []byte) (out []emission, ok bool) {
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil {
		return nil, false
	}
	var l claudeLine
	if json.Unmarshal(raw, &l) != nil {
		return nil, true // JSON, but a field is not the shape we know
	}
	if l.UUID == "" || l.IsSidechain || l.IsCompactSummary || l.IsVisibleInTranscriptOnly {
		return nil, true
	}
	if l.IsMeta && l.Type != "user" { // a meta user line may be a notice
		return nil, true
	}
	b := &lineItems{uuid: l.UUID, at: l.Timestamp}
	switch l.Type {
	case "system":
		switch l.Subtype {
		case "turn_duration":
			b.add(Item{Kind: KindTurnEnd, Duration: time.Duration(l.DurationMs) * time.Millisecond})
		case "compact_boundary":
			b.add(Item{Kind: KindCompaction})
		}
	case "assistant":
		d.assistant(l, b)
	case "user":
		d.user(l, b)
	}
	return b.out, true
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func blocks(raw json.RawMessage) []claudeBlock {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []claudeBlock{{Type: "text", Text: s}}
	}
	var bs []claudeBlock
	if json.Unmarshal(raw, &bs) != nil {
		return nil
	}
	return bs
}

func textOf(bs []claudeBlock) string {
	var parts []string
	for _, b := range bs {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (d *claudeDecoder) assistant(l claudeLine, b *lineItems) {
	bs := blocks(l.Message.Content)
	if text := strings.TrimSpace(textOf(bs)); text != "" && !b.full() {
		b.add(Item{Kind: KindAssistant, Text: cutTo(text, maxText, "…")})
	}
	for _, blk := range bs {
		if blk.Type != "tool_use" || blk.Name == "" || b.full() {
			continue
		}
		var p pendingCall
		if blk.Name == "AskUserQuestion" {
			for _, q := range askedQuestions(blk.Input) {
				if b.full() {
					break
				}
				q.Pending = true
				p.ids = append(p.ids, b.add(Item{Kind: KindQuestion, Question: &q}))
				p.questions = append(p.questions, q.Text)
				p.size += len(q.Text)
			}
		}
		if len(p.ids) == 0 {
			p.ids = []string{b.add(Item{Kind: KindToolCall, Tool: &ToolCall{
				Name:    cutTo(blk.Name, maxToolName, "…"),
				Summary: summary(blk.Input),
				Input:   approvingInput(blk.Name, blk.Input),
			}})}
			p.questions = []string{""}
		}
		if blk.ID != "" {
			p.size += len(blk.ID) + 64*len(p.ids)
			d.remember(blk.ID, p)
		}
	}
}

func askedQuestions(input json.RawMessage) []Question {
	var in struct {
		Questions []struct {
			Question    string `json:"question"`
			MultiSelect bool   `json:"multiSelect"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	var qs []Question
	for _, q := range in.Questions {
		if len(qs) == maxQuestions {
			break
		}
		out := Question{Text: cutTo(q.Question, maxQuestionLen, "…"), MultiSelect: q.MultiSelect}
		for _, o := range q.Options {
			if len(out.Options) == maxOptions {
				break
			}
			out.Options = append(out.Options, Option{
				Label:       cutTo(o.Label, maxLabel, "…"),
				Description: cutTo(o.Description, maxDescription, "…"),
			})
		}
		qs = append(qs, out)
	}
	return qs
}

// approvingInput is what the engineer approves by allowing the call; other tools have none.
func approvingInput(name string, input json.RawMessage) string {
	key := "file_path"
	switch name {
	case "Bash":
		key = "command"
	case "NotebookEdit":
		key = "notebook_path"
	case "Write", "Edit", "MultiEdit", "Read":
	default:
		return ""
	}
	var in map[string]json.RawMessage
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(in[key], &s) != nil {
		return ""
	}
	return cutTo(s, maxToolInput, "\n… cut")
}

func summary(input json.RawMessage) string {
	var in map[string]json.RawMessage
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	for _, key := range []string{"description", "command", "file_path", "path", "pattern", "url", "query", "prompt"} {
		var s string
		if json.Unmarshal(in[key], &s) == nil && s != "" {
			return cutTo(s, 200, "…")
		}
	}
	return ""
}

// user keeps a line the harness wrote as a notice so it can't pass as the engineer's words.
func (d *claudeDecoder) user(l claudeLine, b *lineItems) {
	bs := blocks(l.Message.Content)
	folded := 0
	for _, blk := range bs {
		if blk.Type == "tool_result" && folded < maxLineItems && !l.IsMeta {
			folded++
			d.fold(blk, l.ToolUseResult, b)
		}
	}
	text := textOf(bs)
	head := strings.TrimLeft(text, " \t\r\n")
	for _, p := range []string{"<command-name>", "<local-command-"} {
		if strings.HasPrefix(head, p) {
			return
		}
	}
	text = strings.TrimSpace(text)
	if text == "" || b.full() {
		return
	}
	for _, p := range noticePrefixes {
		if strings.HasPrefix(head, p) {
			// The chat has always shown a subagent's report and never another session's message.
			b.add(Item{Kind: KindNotice, Text: cutTo(text, maxText, "…"), Meta: l.IsMeta})
			return
		}
	}
	if !l.IsMeta {
		b.add(Item{Kind: KindUser, Text: cutTo(text, maxText, "…")})
	}
}

// fold drops a result whose call was not seen, as when a read began mid-file.
func (d *claudeDecoder) fold(res claudeBlock, extra json.RawMessage, b *lineItems) {
	p, ok := d.calls[res.ToolUseID]
	if !ok {
		return
	}
	delete(d.calls, res.ToolUseID)
	d.held -= p.size
	output := strings.TrimSpace(resultText(res.Content))
	var answers struct {
		Answers map[string]string `json:"answers"`
	}
	_ = json.Unmarshal(extra, &answers)
	for i, id := range p.ids {
		if p.questions[i] == "" {
			b.out = append(b.out, emission{Update: true, Item: Item{ID: id, Tool: &ToolCall{
				Output: cutTo(output, d.maxOutput, "… [output cut]"), Error: res.IsError, Done: true,
			}}})
			continue
		}
		answer := output
		for text, a := range answers.Answers {
			if cutTo(text, maxQuestionLen, "…") == p.questions[i] {
				answer = a
			}
		}
		b.out = append(b.out, emission{Update: true, Item: Item{ID: id, Question: &Question{
			Answer: cutTo(answer, maxText, "…"),
		}}})
	}
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var bs []claudeBlock
	if json.Unmarshal(raw, &bs) != nil {
		return ""
	}
	return textOf(bs)
}

// cutTo cuts at n bytes on a character boundary and adds marker.
func cutTo(text string, n int, marker string) string {
	if len(text) <= n {
		return text
	}
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n] + marker
}
