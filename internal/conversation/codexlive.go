package conversation

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Live is a Codex ship's conversation as its app-server tells it: the thread/resume answer, then notifications
// and the server's requests. Like the rollout it only reads: a request is learned from, never answered.
// It is not safe for concurrent use.
type Live struct {
	h   *held
	dec *liveDecoder
}

// NewLive follows one thread; anything the server says about another is not this conversation.
func NewLive(o Options, thread string) *Live {
	l := &Live{h: newHeld(o, true), dec: &liveDecoder{thread: thread, asked: map[string][]string{}}}
	l.h.dec = l.dec
	return l
}

// Apply reads one frame from the server and returns the held items it made or filled in, oldest first.
func (l *Live) Apply(frame []byte) []Item {
	l.h.line(frame)
	return l.h.changed()
}

func (l *Live) Items() []Item { return append([]Item(nil), l.h.items...) }

// Pending is the questions the server is waiting on, oldest first.
func (l *Live) Pending() []Item {
	var out []Item
	for _, it := range l.h.items {
		if it.Kind == KindQuestion && it.Question != nil && it.Question.Pending {
			out = append(out, it)
		}
	}
	return out
}

type liveDecoder struct {
	thread string
	asked  map[string][]string // request id -> the ids of the question items it made, until it is settled
}

func (d *liveDecoder) reset() { d.asked = map[string][]string{} }

type liveFrame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

type liveThreadItem struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Text    string `json:"text"`
	Command string `json:"command"`
	Status  string `json:"status"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Changes []struct {
		Path string `json:"path"`
	} `json:"changes"`
	Output      *string `json:"aggregatedOutput"`
	StartedAtMs int64   `json:"startedAtMs"`
	CompletedMs int64   `json:"completedAtMs"`
}

type liveTurn struct {
	ID          string           `json:"id"`
	Items       []liveThreadItem `json:"items"`
	Status      string           `json:"status"`
	CompletedAt int64            `json:"completedAt"`
	DurationMs  int64            `json:"durationMs"`
}

type liveParams struct {
	ThreadID  string          `json:"threadId"`
	Item      liveThreadItem  `json:"item"`
	Turn      liveTurn        `json:"turn"`
	RequestID json.RawMessage `json:"requestId"`
	ItemID    string          `json:"itemId"`
	Questions []struct {
		Question string `json:"question"`
		IsSecret bool   `json:"isSecret"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

func (d *liveDecoder) line(raw []byte) (out []emission, ok bool) {
	var f liveFrame
	if json.Unmarshal(raw, &f) != nil {
		return nil, false
	}
	if f.Method == "" {
		// The thread/resume answer: the history the server holds.
		var res struct {
			Thread struct {
				ID    string     `json:"id"`
				Turns []liveTurn `json:"turns"`
			} `json:"thread"`
		}
		if json.Unmarshal(f.Result, &res) != nil || res.Thread.ID != d.thread {
			return nil, true
		}
		for _, t := range res.Thread.Turns {
			for _, it := range t.Items {
				out = append(out, d.item(it, true)...)
			}
			if t.Status != "" && t.Status != "inProgress" {
				out = append(out, turnEnd(t))
			}
		}
		return out, true
	}
	var p liveParams
	if json.Unmarshal(f.Params, &p) != nil {
		return nil, true
	}
	switch f.Method {
	case "item/started":
		if p.ThreadID == d.thread {
			out = d.item(p.Item, false)
		}
	case "item/completed":
		if p.ThreadID == d.thread {
			out = d.item(p.Item, true)
		}
	case "turn/completed":
		if p.ThreadID == d.thread {
			out = append(d.settleAll(), turnEnd(p.Turn))
		}
	case "item/tool/requestUserInput":
		if p.ThreadID == d.thread {
			out = d.ask(f.ID, p)
		}
	case "serverRequest/resolved":
		if p.ThreadID == d.thread {
			out = d.settle(string(p.RequestID))
		}
	}
	return out, true
}

// item is a thread item as ours; a started one is the call still open, which is what a card names while its approval waits.
func (d *liveDecoder) item(it liveThreadItem, finished bool) []emission {
	at := millis(it.StartedAtMs)
	if at == "" {
		at = millis(it.CompletedMs)
	}
	one := func(i Item) []emission {
		i.ID, i.At = it.ID, at
		return []emission{{Item: i}}
	}
	if it.ID == "" {
		return nil
	}
	switch it.Type {
	case "userMessage":
		var parts []string
		for _, c := range it.Content {
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
		}
		if text := strings.TrimSpace(strings.Join(parts, "\n\n")); text != "" {
			return one(Item{Kind: KindUser, Text: cutTo(text, maxText, "…")})
		}
	case "agentMessage":
		// Its started item holds no text yet.
		if text := strings.TrimSpace(it.Text); finished && text != "" {
			return one(Item{Kind: KindAssistant, Text: cutTo(text, maxText, "…")})
		}
	case "commandExecution":
		call := &ToolCall{Name: "Bash", Summary: cutTo(firstLine(it.Command), 200, "…"), Input: cutTo(it.Command, maxToolInput, "\n… cut"),
			Done: it.Status != "" && it.Status != "inProgress"}
		if it.Output != nil {
			call.Output = cutTo(strings.TrimSpace(*it.Output), DefaultMaxOutput, "… [output cut]")
		}
		return one(Item{Kind: KindToolCall, Tool: call})
	case "fileChange":
		var paths []string
		for _, c := range it.Changes {
			paths = append(paths, c.Path)
		}
		first := ""
		if len(paths) > 0 {
			first = paths[0]
		}
		return one(Item{Kind: KindToolCall, Tool: &ToolCall{Name: "apply_patch", Summary: cutTo(first, 200, "…"),
			Input: cutTo(strings.Join(paths, "\n"), maxToolInput, "\n… cut"), Done: it.Status != "" && it.Status != "inProgress"}})
	case "contextCompaction":
		return one(Item{Kind: KindCompaction})
	}
	return nil
}

func turnEnd(t liveTurn) emission {
	at := ""
	if t.CompletedAt > 0 {
		at = millis(t.CompletedAt * 1000)
	}
	return emission{Item: Item{ID: t.ID + "#end", Kind: KindTurnEnd, At: at, Duration: time.Duration(t.DurationMs) * time.Millisecond}}
}

// ask makes an item per question, ids shaped like one call's items, so the answer route can tell a call that holds several.
// A secret question or one without options is shown without them, which sends it to the terminal.
func (d *liveDecoder) ask(id json.RawMessage, p liveParams) []emission {
	if p.ItemID == "" || len(p.Questions) == 0 || len(d.asked) >= maxPending {
		return nil
	}
	var out []emission
	var ids []string
	for i, q := range p.Questions {
		if i == maxQuestions {
			break
		}
		qi := Question{Text: cutTo(q.Question, maxQuestionLen, "…"), Pending: true}
		for _, o := range q.Options {
			if q.IsSecret || len(qi.Options) == maxOptions {
				break
			}
			qi.Options = append(qi.Options, Option{Label: cutTo(o.Label, maxLabel, "…"), Description: cutTo(o.Description, maxDescription, "…")})
		}
		itemID := p.ItemID + "#" + strconv.Itoa(i)
		ids = append(ids, itemID)
		out = append(out, emission{Item: Item{ID: itemID, Kind: KindQuestion, Question: &qi}})
	}
	d.asked[string(id)] = ids
	return out
}

func (d *liveDecoder) settle(request string) []emission {
	var out []emission
	for _, id := range d.asked[request] {
		out = append(out, emission{Update: true, Item: Item{ID: id, Question: &Question{}}})
	}
	delete(d.asked, request)
	return out
}

// settleAll is for a turn that ended: whatever it asked is no longer waiting, answered or not.
func (d *liveDecoder) settleAll() []emission {
	var out []emission
	for r := range d.asked {
		out = append(out, d.settle(r)...)
	}
	return out
}

func millis(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}
