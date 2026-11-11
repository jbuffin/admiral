package conversation

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// What a line can make, and how long each field of it can be. The input is model
// output and can be hostile, so every field is cut and every list is capped.
const (
	maxText        = 64 << 10 // a message's text, a question's answer
	maxToolName    = 128
	maxQuestionLen = 4 << 10
	maxLabel       = 256
	maxDescription = 2 << 10
	maxQuestions   = 8         // per AskUserQuestion call
	maxOptions     = 16        // per question
	maxLineItems   = 50        // items one line can make, and tool results it can fold
	maxPending     = 1000      // calls held while they wait for a result
	maxPendingSize = 128 << 10 // and the bytes of what is held for them
	// DefaultMaxOutput is how much of a tool's output an item carries.
	DefaultMaxOutput = 16 << 10
)

// noticePrefixes start the user lines the harness wrote, not the engineer.
var noticePrefixes = []string{"<task-notification>", "Another Claude session sent a message"}

// claudeLine is the part of a Claude Code transcript line Admiral reads. The
// format is undocumented (ADR 0019, ADR 0030): anything else in a line is ignored.
type claudeLine struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	UUID        string `json:"uuid"`
	Timestamp   string `json:"timestamp"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	// Claude Code's compaction summary is a user line that only the transcript shows.
	IsCompactSummary          bool  `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool  `json:"isVisibleInTranscriptOnly"`
	DurationMs                int64 `json:"durationMs"`
	Message                   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

// emission is an item the decoder hands on. An update carries only the item's id
// and the part a result fills in (Tool's output, a Question's answer); the holder
// merges it into the item it has.
type emission struct {
	Item   Item
	Update bool
}

// pendingCall is what the decoder keeps of a call until its result arrives: the
// ids of the items it made and, for questions, the question texts.
type pendingCall struct {
	ids       []string
	questions []string // parallel to ids; "" for a tool call
	size      int
}

// claudeDecoder turns transcript lines into items. It remembers the calls that
// have no result yet so a result on a later line folds into its call.
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

// remember holds a call, evicting the oldest while there are too many or too much.
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
	if len(d.order) > 2*maxPending+16 { // ids of folded calls linger; drop them
		live := d.order[:0]
		for _, id := range d.order {
			if _, ok := d.calls[id]; ok {
				live = append(live, id)
			}
		}
		d.order = live
	}
}

// lineItems collects the items of one line, giving each its id: the line's uuid
// for the first, uuid#1, uuid#2 and so on for the rest.
type lineItems struct {
	uuid, at string
	n        int // items made by add; updates folded in don't count
	out      []emission
}

// full is whether the line has made all the items one line may.
func (b *lineItems) full() bool { return b.n >= maxLineItems }

// add makes an item. The first of a line has the line's uuid; the others have
// uuid#1, uuid#2 and so on, a form a uuid can't take. It returns the item's id.
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

// line decodes one line. ok is false when the line is not a JSON object, which
// is what tells a file with nothing parseable from one with nothing to show.
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
	if l.IsMeta && l.Type != "user" { // a meta user line may be a notice; the rest are dropped
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

// blocks is a message's content as blocks; a plain string is one text block.
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

// textOf joins the text blocks by a blank line.
func textOf(bs []claudeBlock) string {
	var parts []string
	for _, b := range bs {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// assistant makes an item of the text blocks and one of each tool call. Thinking
// blocks are dropped.
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
			}})}
			p.questions = []string{""}
		}
		if blk.ID != "" {
			p.size += len(blk.ID) + 64*len(p.ids)
			d.remember(blk.ID, p)
		}
	}
}

// askedQuestions is the questions an AskUserQuestion call put, each field cut and
// the lists capped.
func askedQuestions(input json.RawMessage) []Question {
	var in struct {
		Questions []struct {
			Question string `json:"question"`
			Options  []struct {
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
		out := Question{Text: cutTo(q.Question, maxQuestionLen, "…")}
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

// summary is a short line on what a tool was asked to do, from the input fields
// tools commonly carry.
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

// user makes an item of a user line's text and folds its tool results into their
// calls. A line the harness wrote and the engineer did not is a notice, so it can't
// pass as the engineer's words; a meta line that is not a notice is dropped.
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
			// The chat shows a subagent's report today and has never shown the
			// meta line another session's message arrives in.
			b.add(Item{Kind: KindNotice, Text: cutTo(text, maxText, "…"), Meta: l.IsMeta})
			return
		}
	}
	if !l.IsMeta {
		b.add(Item{Kind: KindUser, Text: cutTo(text, maxText, "…")})
	}
}

// fold emits the result of a call as an update of the items the call made. A
// result whose call was not seen (a read that began mid-file) is dropped.
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

// resultText is a tool result's content: a string, or text blocks.
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

// cutTo ends a text at n bytes, on a character boundary, and adds marker when it cut.
func cutTo(text string, n int, marker string) string {
	if len(text) <= n {
		return text
	}
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n] + marker
}
