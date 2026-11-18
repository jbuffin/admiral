package main

import (
	"testing"

	"github.com/jbuffin/admiral/internal/chat"
	"github.com/jbuffin/admiral/internal/push"
)

func TestADigestReplyPushesOnceAsAChatEventWithItsOwnKeyAndNoShipCard(t *testing.T) {
	var got []push.Event
	on := digestReplyPush(func(string) string { return "Work" }, func(e push.Event) { got = append(got, e) })
	on("work", chat.DigestReply{TurnID: "digest:routine-1:rec-1", MessageID: "m-1", Text: "**Two ships** moored"})
	if len(got) != 1 {
		t.Fatalf("%d events, want 1", len(got))
	}
	e := got[0]
	if e.Kind != push.DigestReply || e.Fleet != "work" || e.FleetName != "Work" || e.Text != "Two ships moored" ||
		e.Target != push.TargetChat || e.Message != "m-1" || e.Key != "digest-reply-digest:routine-1:rec-1" {
		t.Errorf("event = %+v", e)
	}
	if e.Session != "" || e.Item != "" {
		t.Errorf("a digest reply is no Inbox item: %+v", e)
	}
	if push.Decide(e, false, true) != push.Push || push.Decide(e, true, true) != push.Alert || push.Decide(e, false, false) != push.Nothing {
		t.Error("the event does not push when away, alert when watched, or stay quiet with Notify off")
	}
}

func TestAReportReplyStillPushesBesideTheDigestOne(t *testing.T) {
	var got []push.Event
	on := reportReplyPush(cardsStub{"work/ship-1-moored": {"Calm Otter", "done"}}, func(string) string { return "Work" }, func(e push.Event) { got = append(got, e) })
	on("work", chat.ReportReply{CardID: "ship-1-moored", MessageID: "m-1", Text: "hi"})
	if len(got) != 1 || got[0].Kind != push.ReportReply || got[0].Key != "report-reply-ship-1-moored" {
		t.Errorf("events = %+v, want the one report reply", got)
	}
}
