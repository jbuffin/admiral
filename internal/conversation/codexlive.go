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
	l := &Live{h: newHeld(o, true), dec: newLiveDecoder(thread)}
	l.h.dec = l.dec
	return l
}

// Apply reads one frame from the server and returns the held items it made or filled in, oldest first.
func (l *Live) Apply(frame []byte) []Item {
	l.h.line(frame)
	return l.h.changed()
}

func (l *Live) Items() []Item { return append([]Item(nil), l.h.items...) }

// Approving is whether the server is waiting on an approval, whatever it is for.
func (l *Live) Approving() bool { return len(l.dec.approvals) > 0 }

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
	thread    string
	asked     map[string][]string  // request id -> the ids of the question items it made, until it is settled
	approvals map[string]*approval // request id -> an approval the server waits on, until it is settled
	marked    map[string]bool      // item ids shown as awaiting approval
}

// approval is a server request for a yes or no. Only a plain command or file change, alone, is one a card answers.
type approval struct {
	item, input, summary string
	plain                bool
}

func newLiveDecoder(thread string) *liveDecoder {
	d := &liveDecoder{thread: thread}
	d.reset()
	return d
}

func (d *liveDecoder) reset() {
	d.asked, d.approvals, d.marked = map[string][]string{}, map[string]*approval{}, map[string]bool{}
}

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
	ThreadID string `json:"threadId"`
	Thread   struct {
		ID        string          `json:"id"`
		Ephemeral bool            `json:"ephemeral"`
		Source    json.RawMessage `json:"source"`
	} `json:"thread"`
	Item      liveThreadItem  `json:"item"`
	Turn      liveTurn        `json:"turn"`
	RequestID json.RawMessage `json:"requestId"`
	ItemID    string          `json:"itemId"`
	Kind      json.RawMessage `json:"kind"`
	Command   *string         `json:"command"`
	Cwd       *string         `json:"cwd"`
	// approvalId is set for a subcommand of a script and grantRoot for write access to a root: their menus differ.
	ApprovalID *string `json:"approvalId"`
	GrantRoot  *string `json:"grantRoot"`
	Questions  []struct {
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
			out = append(append(d.settleAll(), d.unapproveAll()...), turnEnd(p.Turn))
		}
	case "item/tool/requestUserInput":
		if p.ThreadID == d.thread {
			out = d.ask(f.ID, p)
		}
	case "serverRequest/resolved":
		if p.ThreadID == d.thread {
			out = append(d.settle(string(p.RequestID)), d.unapprove(string(p.RequestID))...)
		}
	default:
		if p.ThreadID == d.thread && strings.HasSuffix(strings.ToLower(f.Method), "requestapproval") {
			out = d.approve(string(f.ID), f.Method, p)
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
	delete(d.marked, it.ID) // a whole item replaces the one held, mark and all
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
		call := &ToolCall{Live: true, Name: "Bash", Summary: cutTo(firstLine(it.Command), 200, "…"), Input: cutTo(it.Command, maxToolInput, "\n… cut"),
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
		return one(Item{Kind: KindToolCall, Tool: &ToolCall{Live: true, Name: "apply_patch", Summary: cutTo(first, 200, "…"),
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

// approve records a request for approval and shows the call it names as the one awaiting it. Two waiting at once,
// or one for something other than a plain command or file change, name no call: the terminal shows which is which.
func (d *liveDecoder) approve(request, method string, p liveParams) []emission {
	if len(d.approvals) >= maxPending {
		return nil
	}
	a := &approval{item: p.ItemID}
	switch method {
	case "item/commandExecution/requestApproval":
		kind := strings.Trim(string(p.Kind), `"`)
		a.plain = p.ItemID != "" && (kind == "" || kind == "null" || kind == "command") && (p.ApprovalID == nil || *p.ApprovalID == "")
		if p.Command != nil {
			a.input = cutTo(*p.Command, maxToolInput, "\n… cut")
		}
		if p.Cwd != nil && *p.Cwd != "" {
			a.summary = "in " + cutTo(*p.Cwd, 200, "…")
		}
	case "item/fileChange/requestApproval":
		a.plain = p.ItemID != "" && (p.GrantRoot == nil || *p.GrantRoot == "")
	}
	d.approvals[request] = a
	return d.marks()
}

func (d *liveDecoder) unapprove(request string) []emission {
	if _, ok := d.approvals[request]; !ok {
		return nil
	}
	delete(d.approvals, request)
	return d.marks()
}

// unapproveAll is for a turn that ended: whatever it asked is no longer waiting, answered or not.
func (d *liveDecoder) unapproveAll() []emission {
	d.approvals = map[string]*approval{}
	return d.marks()
}

// marks brings the shown marks to what the waiting approvals say: exactly one, and a plain one, marks its call.
func (d *liveDecoder) marks() []emission {
	want := ""
	var a *approval
	if len(d.approvals) == 1 {
		for _, only := range d.approvals {
			if only.plain {
				want, a = only.item, only
			}
		}
	}
	var out []emission
	for id := range d.marked {
		if id != want {
			delete(d.marked, id)
			out = append(out, emission{Update: true, Item: Item{ID: id, Tool: &ToolCall{Live: true}}})
		}
	}
	if want != "" && !d.marked[want] {
		d.marked[want] = true
		out = append(out, emission{Update: true, Item: Item{ID: want, Tool: &ToolCall{Live: true, Approving: true, Input: a.input, Summary: a.summary}}})
	}
	return out
}
