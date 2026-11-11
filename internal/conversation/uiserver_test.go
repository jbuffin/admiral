package conversation_test

// Decoding a Copilot --ui-server stream into items.

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

	"github.com/jbuffin/admiral/internal/conversation"
)

func serverStream(t *testing.T) [][]byte {
	t.Helper()
	f, err := os.Open("testdata/copilot-server-stream.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		out = append(out, append([]byte(nil), sc.Bytes()...))
	}
	return out
}

func feedServer(r *conversation.ServerReader, msgs [][]byte) {
	for _, m := range msgs {
		r.Feed(m)
	}
}

func fileItems(t *testing.T) []conversation.Item {
	t.Helper()
	data, err := os.ReadFile("testdata/copilot-server-stream.events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return readCop(t, string(data))
}

func byID(items []conversation.Item) map[string]conversation.Item {
	m := map[string]conversation.Item{}
	for _, it := range items {
		m[it.ID] = it
	}
	return m
}

// In the recorded stream, messages 15-18 (0-based) are the reply's message_start, two deltas and the final message; 17 is the last delta.
const lastDeltaOfSecondReply = 17

func TestServerStreamGivesTheItemsTheFileGives(t *testing.T) {
	r := conversation.NewServerReader(conversation.Options{Harness: "copilot"})
	feedServer(r, serverStream(t))
	got := byID(r.Items())
	want := fileItems(t)
	if len(want) == 0 {
		t.Fatal("no file items")
	}
	for _, w := range want {
		g, ok := got[w.ID]
		if !ok {
			t.Errorf("stream has no item with the file's id %q (%s %q)", w.ID, w.Kind, w.Text)
			continue
		}
		if g.Kind != w.Kind || g.Text != w.Text {
			t.Errorf("item %q: stream %s %q, file %s %q", w.ID, g.Kind, g.Text, w.Kind, w.Text)
		}
	}
	if len(r.Items()) != len(want) {
		t.Errorf("stream gave %d items, file %d", len(r.Items()), len(want))
	}
}

func TestServerStreamShowsAssistantTextBeforeTheTurnEnds(t *testing.T) {
	msgs := serverStream(t)
	r := conversation.NewServerReader(conversation.Options{Harness: "copilot"})
	feedServer(r, msgs[:lastDeltaOfSecondReply+1])
	reply := func() (it conversation.Item, n int) {
		for _, x := range r.Items() {
			if x.Kind == conversation.KindAssistant && x.Text == "Done." {
				it, n = x, n+1
			}
		}
		return
	}
	mid, n := reply()
	if n != 1 {
		t.Fatalf("after the last delta the reply is shown %d times, want once; items = %v", n, kinds(r.Items()))
	}

	feedServer(r, msgs[lastDeltaOfSecondReply+1:])
	done, n := reply()
	if n != 1 {
		t.Fatalf("after the final message the reply is shown %d times, want once", n)
	}
	// One id from the first delta to the file, so a client replaces the item in place.
	if done.ID != mid.ID {
		t.Errorf("mid-turn id %q, finished id %q", mid.ID, done.ID)
	}
	for _, f := range fileItems(t) {
		if f.Kind == conversation.KindAssistant && f.Text == "Done." && f.ID != done.ID {
			t.Errorf("the file's id for the reply is %q, the stream's %q", f.ID, done.ID)
		}
	}

	// A turn's text comes before its end.
	lastText := -1
	for i, it := range r.Items() {
		switch it.Kind {
		case conversation.KindAssistant:
			lastText = i
		case conversation.KindTurnEnd:
			if lastText < 0 {
				t.Errorf("turn end at %d has no assistant text before it", i)
			}
			lastText = -1
		}
	}
}

func TestServerStreamIgnoresWhatIsNotAnEvent(t *testing.T) {
	r := conversation.NewServerReader(conversation.Options{Harness: "copilot"})
	for _, m := range []string{
		`{"jsonrpc":"2.0","method":"session.lifecycle","params":{"sessionId":"s","type":"session.background"}}`,
		`{"jsonrpc":"2.0","method":"host.event","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s"}}`,
		`not json`,
	} {
		r.Feed([]byte(m))
	}
	if n := len(r.Items()); n != 0 {
		t.Errorf("%d items from messages that carry no session event", n)
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session.event", "params": map[string]any{
		"sessionId": "s", "event": map[string]any{"type": "user.message", "id": "u1", "timestamp": "2026-11-11T21:00:00.000Z", "parentId": nil, "data": map[string]any{"content": "hi"}}}})
	r.Feed(b)
	if items := r.Items(); len(items) != 1 || items[0].ID != "u1" || items[0].Text != "hi" {
		t.Errorf("a user.message event gave %+v", items)
	}
}
