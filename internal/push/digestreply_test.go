package push_test

import (
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/push"
)

// AdmiralBot's reply to a fleet digest turn: its own key per run, push or alert, the chat at that message, no Inbox item.

const digestTurn = "digest:routine-1:rec-1"

func digest(text string) push.Digest {
	return push.Digest{Fleet: "work", FleetName: "Work", Turn: digestTurn, Message: "msg-9", Text: text}
}

func TestADigestReplyPushesWhenAwayAndAlertsWhenWatching(t *testing.T) {
	ev := push.DigestReplyEvent(digest("Two ships moored this week."))
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
			var outs []push.Outcome
			for _, d := range emitted(c.watching, c.notify, ev) {
				outs = append(outs, d.Outcome)
			}
			if len(outs) != len(c.want) || (len(outs) == 1 && outs[0] != c.want[0]) {
				t.Errorf("outcomes = %v, want %v", outs, c.want)
			}
		})
	}
}

func TestADigestReplyTapOpensTheChatAtItsMessageAndIsNoInboxItem(t *testing.T) {
	ev := push.DigestReplyEvent(digest("x"))
	p := push.PayloadOf(ev)
	if p.Target != "chat" || p.Fleet != "work" || p.Message != "msg-9" {
		t.Errorf("target, fleet, message = %q, %q, %q, want chat, work, msg-9", p.Target, p.Fleet, p.Message)
	}
	if p.Session != "" || p.Item != "" || p.Crew != "" || p.Merge || p.Routine != "" {
		t.Errorf("a chat target carries no ship, item, crew, merge or routine: %+v", p)
	}
	if ev.Kind == push.ReportReply {
		t.Error("a digest reply is its own kind, not a report reply")
	}
}

func TestADigestReplyIsTaggedPerRunSoTwoRunsDoNotReplaceEachOther(t *testing.T) {
	a := push.PayloadOf(push.DigestReplyEvent(digest("x"))).Tag
	other := digest("x")
	other.Turn = "digest:routine-1:rec-2"
	b := push.PayloadOf(push.DigestReplyEvent(other)).Tag
	if !strings.Contains(a, digestTurn) {
		t.Errorf("tag = %q, want it to name the turn %s", a, digestTurn)
	}
	if a == b {
		t.Errorf("two runs share the tag %q", a)
	}
	if strings.HasPrefix(a, "report-reply-") {
		t.Errorf("tag = %q shares a report reply's key space", a)
	}
}

func TestADigestReplyLineIsItsFirstLineAsSafePlainText(t *testing.T) {
	for _, c := range []struct{ name, text, want string }{
		{"a link keeps its text", "Two ships: [PR 7](https://github.com/acme/api/pull/7)", "Two ships: PR 7"},
		{"only the first line", "First line\n\nSecond line", "First line"},
		{"control, bidi and zero-width characters drop", "Done‮\x07 now​\x1b", "Done now"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := push.PayloadOf(push.DigestReplyEvent(digest(c.text))).Line; got != c.want {
				t.Errorf("line = %q, want %q", got, c.want)
			}
		})
	}
	if n := len([]rune(push.PayloadOf(push.DigestReplyEvent(digest(strings.Repeat("é", 200)))).Line)); n != 120 {
		t.Errorf("a long line is %d runes, want 120", n)
	}
}
