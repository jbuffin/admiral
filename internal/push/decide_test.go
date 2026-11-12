package push_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/push"
)

func TestDecide(t *testing.T) {
	pushing := []push.Kind{
		push.ChatQuestion, push.ShipQuestion, push.InboxQuestion, push.WakeProposal,
		push.CodeApproval, push.Findings, push.ReadyToMerge, push.Anchored, push.RoutineNotify,
	}
	// Count-only: they reach the Inbox and the attention count, never a device or a chime.
	countOnly := []push.Kind{"stop", "idle", "unknown", "notice", "review", "crew_notes", "crew_member"}

	cases := []struct {
		name     string
		kinds    []push.Kind
		watching bool
		notify   bool
		want     push.Outcome
	}{
		{"pushing kind, away, notify on", pushing, false, true, push.Push},
		{"pushing kind, watched, notify on", pushing, true, true, push.Alert},
		{"pushing kind, away, notify off", pushing, false, false, push.Nothing},
		{"pushing kind, watched, notify off", pushing, true, false, push.Nothing},
		{"count-only kind, away", countOnly, false, true, push.Nothing},
		{"count-only kind, watched", countOnly, true, true, push.Nothing},
	}
	for _, c := range cases {
		for _, k := range c.kinds {
			t.Run(c.name+"/"+string(k), func(t *testing.T) {
				ev := push.Event{Kind: k, Fleet: "work", Key: "k1", Name: "Dory", Text: "waiting"}
				if got := push.Decide(ev, c.watching, c.notify); got != c.want {
					t.Errorf("Decide(%s, watching=%v, notify=%v) = %v, want %v", k, c.watching, c.notify, got, c.want)
				}
			})
		}
	}
}

func TestDecideEventWithNoFleetTreatsNotifyAsOn(t *testing.T) {
	ev := push.Event{Kind: push.RoutineNotify, Key: "routine-1", Name: "Nightly", Text: "done"}
	if got := push.Decide(ev, false, false); got != push.Push {
		t.Errorf("no fleet, away = %v, want Push whatever the fleet flag says", got)
	}
	if got := push.Decide(ev, true, false); got != push.Alert {
		t.Errorf("no fleet, watched = %v, want Alert", got)
	}
}

func TestPayloadIsOneShortLineWithTheEventsTag(t *testing.T) {
	ev := push.Event{Kind: push.ShipQuestion, Fleet: "work", Key: "dory:question", Name: "Dory",
		Text: "Which database should I use?\nHere is a diff:\n- old\n+ new"}
	p := push.PayloadOf(ev)
	if p.Line != "Which database should I use?" {
		t.Errorf("Line = %q, want the first line only", p.Line)
	}
	if p.Name != "Dory" || p.Tag != "dory:question" {
		t.Errorf("payload = %+v, want name Dory and tag dory:question", p)
	}

	long := push.PayloadOf(push.Event{Kind: push.Findings, Key: "f", Text: strings.Repeat("é", 500)})
	if n := len([]rune(long.Line)); n == 0 || n > 120 {
		t.Errorf("Line has %d characters, want 1 to 120", n)
	}
}

func TestPayloadCarriesWhereATapLands(t *testing.T) {
	p := push.PayloadOf(push.Event{Kind: push.InboxQuestion, Fleet: "work", Key: "s/i", Name: "Dory", Text: "x", Target: push.TargetPeek, Session: "s", Item: "i"})
	if p.Fleet != "work" || p.Target != push.TargetPeek || p.Session != "s" || p.Item != "i" {
		t.Errorf("payload = %+v, want fleet work and a peek of item i in session s", p)
	}
}

func TestPayloadCarriesTheRoutineATapOpens(t *testing.T) {
	p := push.PayloadOf(push.Event{Kind: push.RoutineNotify, Fleet: "work", Key: "routine:r1:t3", Name: "Nightly sweep", Text: "failed to run", Target: push.TargetRoutine, Routine: "r1"})
	if p.Fleet != "work" || p.Target != push.TargetRoutine || p.Routine != "r1" || p.Session != "" {
		t.Errorf("payload = %+v, want fleet work and routine r1 with no session", p)
	}
	b, err := json.Marshal(p)
	if err != nil || !strings.Contains(string(b), `"routine":"r1"`) {
		t.Errorf("payload JSON = %s (%v), want a routine field", b, err)
	}
}
