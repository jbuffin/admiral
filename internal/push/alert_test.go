package push_test

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

type sentLog struct {
	mu   sync.Mutex
	sent []map[string]any
}

// SendWatching is the part of the server's hub the alert sink needs.
func (l *sentLog) SendWatching(v any) {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	l.mu.Lock()
	l.sent = append(l.sent, m)
	l.mu.Unlock()
}

func TestAlertSinkSendsAnAlertToTheWatchedWindowsAndNothingForAPush(t *testing.T) {
	log := &sentLog{}
	sink := push.NewAlertSink(log)
	ev := push.Event{Kind: push.ShipQuestion, Fleet: "work", FleetName: "Work", Key: "s-1", Name: "Dory", Text: "needs your approval\nsecond line", Target: "inbox", Session: "s-1"}

	sink.Deliver(push.Decided{Event: ev, Outcome: push.Push})
	if len(log.sent) != 0 {
		t.Fatalf("a push outcome sent %d window messages, want none", len(log.sent))
	}

	sink.Deliver(push.Decided{Event: ev, Outcome: push.Alert})
	if len(log.sent) != 1 {
		t.Fatalf("an alert outcome sent %d window messages, want 1", len(log.sent))
	}
	got := log.sent[0]
	want := map[string]any{"type": "alert", "fleet": "Work", "name": "Dory", "line": "needs your approval", "tag": "s-1", "target": "inbox", "session": "s-1"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("alert[%q] = %v, want %v", k, got[k], v)
		}
	}
}

// The shell turns what the alert names into a Peek path, so each event says which entry it is about.
func TestAlertEventsNameTheEntryATapOpens(t *testing.T) {
	rec := &push.Recorder{}
	w := push.NewWatcher(func() bool { return true }, func(string) bool { return true }, rec)
	dory := func(status, reason string, crew ...ships.CrewBoat) ships.Ship {
		return ships.Ship{Session: "s-dory", Name: "Dory", Fleet: "work", FleetName: "Work", Status: status, Reason: reason, Crew: crew}
	}
	w.Ships([]ships.Ship{dory(ships.StatusUnderway, "")})
	w.Inbox(nil)

	w.Ships([]ships.Ship{dory(ships.StatusNeedsYou, "question")})
	w.Ships([]ships.Ship{dory(ships.StatusUnderway, "", ships.CrewBoat{Name: "scout", Status: sessionhost.CrewNeedsYou, Reason: "question"})})
	w.Ships([]ships.Ship{dory(ships.StatusAnchored, "")})
	w.Ships([]ships.Ship{func() ships.Ship { s := dory(ships.StatusMoored, ""); s.StackReady = true; return s }()})

	body, _ := json.Marshal(map[string]any{"version": 1, "id": "q-1", "kind": "question", "created": "2026-10-22T10:00:00Z", "title": "Which port?",
		"question": map[string]any{"questions": []any{map[string]any{"id": "1", "text": "Pick", "options": []any{map[string]any{"label": "A"}}}}}})
	it, err := inbox.ParseItem(body, "s-dory", "q-1")
	if err != nil {
		t.Fatal(err)
	}
	it.Fleet = "work"
	w.Inbox([]inbox.Item{it})

	type tap struct{ target, session, crew, item string }
	var got []tap
	for _, d := range rec.Events() {
		got = append(got, tap{d.Event.Target, d.Event.Session, d.Event.Crew, d.Event.Item})
	}
	want := []tap{
		{"inbox", "s-dory", "", ""},      // the ship's stop opens in Peek
		{"inbox", "s-dory", "scout", ""}, // a crew boat's stop
		{"ships", "s-dory", "", ""},      // an anchored ship is no Inbox entry: its own view
		{"merge", "s-dory", "", ""},      // ready to merge
		{"inbox", "s-dory", "", "q-1"},   // an Inbox item
	}
	if len(got) != len(want) {
		t.Fatalf("taps = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d taps %+v, want %+v", i, got[i], want[i])
		}
	}
}
