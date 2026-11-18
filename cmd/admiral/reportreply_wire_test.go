package main

import (
	"testing"

	"github.com/jbuffin/admiral/internal/chat"
	"github.com/jbuffin/admiral/internal/push"
)

type cardsStub map[string][2]string

func (c cardsStub) ShipCardOf(fleet, id string) (string, string, bool) {
	v, ok := c[fleet+"/"+id]
	return v[0], v[1], ok
}

func TestAReportReplyPushesOnceAsAChatEventNamingTheShipAndItsResult(t *testing.T) {
	var got []push.Event
	on := reportReplyPush(cardsStub{"work/ship-1-moored": {"Calm Otter", "partial_done"}}, func(string) string { return "Work" }, func(e push.Event) { got = append(got, e) })
	on("work", chat.ReportReply{CardID: "ship-1-moored", MessageID: "m-1", Text: "**Calm Otter** is done"})
	if len(got) != 1 {
		t.Fatalf("%d events, want 1", len(got))
	}
	e := got[0]
	if e.Kind != push.ReportReply || e.Fleet != "work" || e.FleetName != "Work" || e.Name != "Calm Otter partial done" ||
		e.Text != "Calm Otter is done" || e.Target != push.TargetChat || e.Message != "m-1" || e.Key != "report-reply-ship-1-moored" {
		t.Errorf("event = %+v", e)
	}
	if push.Decide(e, false, true) != push.Push || push.Decide(e, true, true) != push.Alert || push.Decide(e, false, false) != push.Nothing {
		t.Error("the event does not push when away, alert when watched, or stay quiet with Notify off")
	}
}

func TestAReportReplyWithNoShipCardInTheFleetPushesNothing(t *testing.T) {
	var got []push.Event
	on := reportReplyPush(cardsStub{"home/ship-1-moored": {"Calm Otter", "done"}}, func(string) string { return "Work" }, func(e push.Event) { got = append(got, e) })
	on("work", chat.ReportReply{CardID: "ship-1-moored", MessageID: "m-1", Text: "hi"})
	if len(got) != 0 {
		t.Errorf("%d events for a ship card the fleet does not hold, want none", len(got))
	}
}
