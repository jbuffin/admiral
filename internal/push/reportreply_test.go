package push_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/push"
)

// AdmiralBot's reply to a report turn: when it pushes, what it says and where a tap lands.

const replyCard = "ship-calm-otter-2026-11-17T10:00:00Z-moored"

func reply(text string) push.Reply {
	return push.Reply{Fleet: "work", FleetName: "Work", Ship: "Calm Otter", Result: "sailed_off", Card: replyCard, Message: "msg-7", Text: text}
}

func emitted(watching, notify bool, ev push.Event) []push.Decided {
	rec := &push.Recorder{}
	w := push.NewWatcher(func() bool { return watching }, func(string) bool { return notify }, rec)
	w.Emit(ev)
	return rec.Events()
}

func TestAReplyPushesWhenTheEngineerIsAwayAndAlertsWhenWatching(t *testing.T) {
	ev := push.ReportReplyEvent(reply("Calm Otter is done"))
	for _, c := range []struct {
		name             string
		watching, notify bool
		want             []push.Outcome
	}{
		{"away", false, true, []push.Outcome{push.Push}},
		{"watching", true, true, []push.Outcome{push.Alert}},
		{"notify off, away", false, false, nil},
		{"notify off, watching", true, false, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := emitted(c.watching, c.notify, ev)
			var outs []push.Outcome
			for _, d := range got {
				outs = append(outs, d.Outcome)
			}
			if len(outs) != len(c.want) || (len(outs) == 1 && outs[0] != c.want[0]) {
				t.Errorf("outcomes = %v, want %v", outs, c.want)
			}
		})
	}
}

func TestAReplyTapOpensTheChatAtItsMessage(t *testing.T) {
	p := push.PayloadOf(push.ReportReplyEvent(reply("Calm Otter is done")))
	if p.Target != "chat" {
		t.Errorf("target = %q, want chat", p.Target)
	}
	if p.Fleet != "work" || p.Message != "msg-7" {
		t.Errorf("fleet, message = %q, %q, want work, msg-7", p.Fleet, p.Message)
	}
	if p.Session != "" || p.Item != "" {
		t.Errorf("a chat target carries no session or item, got %q, %q", p.Session, p.Item)
	}
	raw, _ := json.Marshal(p)
	if !strings.Contains(string(raw), `"message":"msg-7"`) {
		t.Errorf("the payload JSON lacks the message id: %s", raw)
	}
}

func TestAReplyNamesTheShipAndItsResultInWords(t *testing.T) {
	p := push.PayloadOf(push.ReportReplyEvent(reply("x")))
	if !strings.Contains(p.Name, "Calm Otter") || !strings.Contains(p.Name, "sailed off") || strings.Contains(p.Name, "_") {
		t.Errorf("name = %q, want the ship and %q", p.Name, "sailed off")
	}
}

func TestAReplyLineIsItsFirstLineAsPlainText(t *testing.T) {
	for _, c := range []struct{ name, text, want string }{
		{"a link keeps its text", "Calm Otter is done: [PR 7](https://github.com/acme/api/pull/7)", "Calm Otter is done: PR 7"},
		{"emphasis and code marks drop", "**Done**: _two_ PRs, `make check` green", "Done: two PRs, make check green"},
		{"only the first line", "First line\n\nSecond line with [a](https://x.test)", "First line"},
		{"control and bidi characters drop", "Done‮\x07 now\x1b", "Done now"},
		{"a heading mark drops", "## Calm Otter finished", "Calm Otter finished"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := push.PayloadOf(push.ReportReplyEvent(reply(c.text))).Line; got != c.want {
				t.Errorf("line = %q, want %q", got, c.want)
			}
		})
	}
	long := strings.Repeat("é", 200)
	if n := len([]rune(push.PayloadOf(push.ReportReplyEvent(reply(long))).Line)); n != 120 {
		t.Errorf("a long line is %d runes, want 120", n)
	}
}

func TestAReplyIsTaggedByItsShipCardSoARepeatReplacesIt(t *testing.T) {
	if tag := push.PayloadOf(push.ReportReplyEvent(reply("x"))).Tag; !strings.Contains(tag, replyCard) {
		t.Errorf("tag = %q, want it to name the ship card %s", tag, replyCard)
	}
}
