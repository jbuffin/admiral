package conversation

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Ordinal   *int64          `json:"ordinal"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexPayload struct {
	Type       string          `json:"type"`
	TurnID     string          `json:"turn_id"`
	DurationMs int64           `json:"duration_ms"`
	Item       codexItem       `json:"item"`
	CallID     string          `json:"call_id"`
	Name       string          `json:"name"`
	Input      string          `json:"input"`
	Arguments  string          `json:"arguments"`
	Output     json.RawMessage `json:"output"`
}

type codexItem struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// codexDecoder reads a rollout's event stream for the words and its response stream for tool calls:
// the response stream also carries the context Codex injects, which is not the engineer's.
type codexDecoder struct {
	*claudeDecoder
}

func newCodexDecoder(maxOutput int) *codexDecoder {
	return &codexDecoder{newClaudeDecoder(maxOutput)}
}

func (d *codexDecoder) line(raw []byte) (out []emission, ok bool) {
	var l codexLine
	if json.Unmarshal(raw, &l) != nil {
		return nil, false
	}
	var p codexPayload
	if json.Unmarshal(l.Payload, &p) != nil {
		return nil, true // JSON, but not a shape we know
	}
	b := &lineItems{at: l.Timestamp}
	ident := func(id string) string {
		if id != "" {
			return id
		}
		if l.Ordinal != nil {
			return "o" + strconv.FormatInt(*l.Ordinal, 10)
		}
		return "t" + l.Timestamp
	}
	switch l.Type {
	case "compacted":
		b.uuid = "compacted-" + ident("")
		b.add(Item{Kind: KindCompaction})
	case "event_msg":
		switch p.Type {
		case "task_complete":
			b.uuid = ident(p.TurnID) + "#end"
			b.add(Item{Kind: KindTurnEnd, Duration: time.Duration(p.DurationMs) * time.Millisecond})
		case "item_completed":
			kind := Kind("")
			switch p.Item.Type {
			case "UserMessage":
				kind = KindUser
			case "AgentMessage":
				kind = KindAssistant
			}
			var parts []string
			for _, c := range p.Item.Content {
				if c.Text != "" {
					parts = append(parts, c.Text)
				}
			}
			if text := strings.TrimSpace(strings.Join(parts, "\n\n")); kind != "" && text != "" {
				b.uuid = ident(p.Item.ID)
				b.add(Item{Kind: kind, Text: cutTo(text, maxText, "…")})
			}
		}
	case "response_item":
		switch p.Type {
		case "custom_tool_call", "function_call":
			d.call(p, ident(p.CallID), b)
		case "custom_tool_call_output", "function_call_output":
			d.result(p, b)
		}
	}
	return b.out, true
}

func (d *codexDecoder) call(p codexPayload, id string, b *lineItems) {
	if p.Name == "" || p.CallID == "" {
		return
	}
	input := p.Input
	if input == "" {
		input = p.Arguments
	}
	name, summary, approves := describeCodexCall(p.Name, input)
	b.uuid = id
	b.add(Item{Kind: KindToolCall, Tool: &ToolCall{
		Name:    cutTo(name, maxToolName, "…"),
		Summary: cutTo(summary, 200, "…"),
		Input:   cutTo(approves, maxToolInput, "\n… cut"),
	}})
	d.remember(p.CallID, pendingCall{ids: []string{id}, questions: []string{""}, size: len(p.CallID) + len(id) + 64})
}

// result drops an output whose call was not seen, as when a read began mid-file.
func (d *codexDecoder) result(p codexPayload, b *lineItems) {
	c, ok := d.calls[p.CallID]
	if !ok {
		return
	}
	delete(d.calls, p.CallID)
	d.held -= c.size
	b.out = append(b.out, emission{Update: true, Item: Item{ID: c.ids[0], Tool: &ToolCall{
		Output: cutTo(strings.TrimSpace(codexOutput(p.Output)), d.maxOutput, "… [output cut]"), Done: true,
	}}})
}

// codexOutput is a call's output as text; a script's own "Script completed" header is not part of it.
func codexOutput(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for i, p := range parts {
		if i == 0 && len(parts) > 1 && strings.HasPrefix(p.Text, "Script completed") {
			continue
		}
		texts = append(texts, p.Text)
	}
	return strings.Join(texts, "")
}

var (
	innerCall   = regexp.MustCompile(`tools\.(\w+)\(`)
	innerCmd    = regexp.MustCompile(`^cmd:\s*("(?:[^"\\]|\\.)*")`)
	patchedFile = regexp.MustCompile(`\*\*\* (Add|Update|Delete) File: ([^\n\\]+)`)
)

// describeCodexCall names the tool the model reached through Codex's exec script or function call, and gives
// the short summary and what allowing it approves: a command in full, or the files a patch touches. What it
// approves comes only from the call it names, so a script with several tool calls approves nothing.
func describeCodexCall(name, input string) (tool, summary, approves string) {
	tool = name
	if name == "exec" {
		calls := innerCall.FindAllStringSubmatchIndex(input, -1)
		if len(calls) == 0 {
			return tool, firstLine(input), ""
		}
		tool = input[calls[0][2]:calls[0][3]]
		if len(calls) > 1 {
			return tool, firstLine(input), ""
		}
		input = callArgument(input[calls[0][1]:])
		if cmd, ok := commandOutsideStrings(input); ok {
			return tool, firstLine(cmd), cmd
		}
	}
	var args struct {
		Cmd     string `json:"cmd"`
		Command any    `json:"command"`
	}
	if json.Unmarshal([]byte(input), &args) == nil {
		switch c := args.Command.(type) {
		case string:
			args.Cmd = c
		case []any:
			var words []string
			for _, w := range c {
				if s, ok := w.(string); ok {
					words = append(words, s)
				}
			}
			args.Cmd = strings.Join(words, " ")
		}
		if args.Cmd != "" {
			return tool, firstLine(args.Cmd), args.Cmd
		}
	}
	if files := patchedFile.FindAllStringSubmatch(unescape(input), -1); len(files) > 0 {
		var paths []string
		for _, f := range files {
			paths = append(paths, strings.TrimSpace(f[2]))
		}
		return tool, strings.TrimSpace(files[0][1]) + " " + strings.Join(paths, ", "), strings.Join(paths, "\n")
	}
	return tool, firstLine(input), ""
}

// commandOutsideStrings finds `cmd: "…"` in a call's argument where it is code, not inside a string literal:
// a patch's text can quote one, and the patch approves its files.
func commandOutsideStrings(s string) (string, bool) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\'', '`':
			for i++; i < len(s) && s[i] != c; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case 'c':
			if i > 0 && (s[i-1] == '_' || s[i-1] >= '0' && s[i-1] <= '9' || s[i-1] >= 'a' && s[i-1] <= 'z' || s[i-1] >= 'A' && s[i-1] <= 'Z') {
				continue
			}
			if m := innerCmd.FindStringSubmatch(s[i:]); m != nil {
				var cmd string
				if json.Unmarshal([]byte(m[1]), &cmd) == nil {
					return cmd, true
				}
			}
		}
	}
	return "", false
}

// callArgument is the text up to the parenthesis that closes a call whose "(" was just read, skipping JS string
// literals, so a comment or later statement in the script is not part of the call.
func callArgument(s string) string {
	depth := 1
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\'', '`':
			for i++; i < len(s) && s[i] != c; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return s[:i]
			}
		}
	}
	return s
}

// unescape reads a JS string literal's \n so a patch inside an exec script can be scanned line by line.
func unescape(s string) string { return strings.ReplaceAll(s, `\n`, "\n") }

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
