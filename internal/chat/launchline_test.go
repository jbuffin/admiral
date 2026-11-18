package chat

import (
	"encoding/json"
	"testing"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/ships"
)

type launchLineView struct {
	Ship   string `json:"ship"`
	Name   string `json:"name"`
	Fleet  string `json:"fleet"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
	Text   string `json:"text"`
}

func launchLinesIn(t *testing.T, r *briefRig, fleet string) []launchLineView {
	t.Helper()
	got, err := r.history().Recent(fleet, 500)
	if err != nil {
		t.Fatal(err)
	}
	var out []launchLineView
	for _, e := range got {
		if e.Kind != chatlog.KindLaunchLine {
			continue
		}
		var v launchLineView
		if err := json.Unmarshal(e.Card, &v); err != nil {
			t.Fatalf("card %s: %v", e.Card, err)
		}
		out = append(out, v)
	}
	return out
}

func TestALaunchLineCarriesTheShipItsKindTheReasonAndTheLaunchText(t *testing.T) {
	r := newBriefRig(t)
	line := ships.LaunchLine{Ship: "calm-otter", Name: "Calm Otter", Kind: "quick_ship", Reason: "to read the AWS logs", Text: "Read the logs and fix the 500."}
	if err := r.PostLaunchLine("work", line); err != nil {
		t.Fatalf("PostLaunchLine = %v", err)
	}
	got := launchLinesIn(t, r, "work")
	if len(got) != 1 {
		t.Fatalf("%d launch lines in the work chat, want 1", len(got))
	}
	want := launchLineView{Ship: "calm-otter", Name: "Calm Otter", Fleet: "work", Kind: "quick_ship", Reason: "to read the AWS logs", Text: "Read the logs and fix the 500."}
	if got[0] != want {
		t.Errorf("launch line = %+v, want %+v", got[0], want)
	}
}

func TestALaunchLinePostsInItsOwnFleetAndNoOther(t *testing.T) {
	r := newBriefRig(t)
	if err := r.PostLaunchLine("work", ships.LaunchLine{Ship: "calm-otter", Kind: "scout", Reason: "to look", Text: "look"}); err != nil {
		t.Fatalf("PostLaunchLine = %v", err)
	}
	if got := launchLinesIn(t, r, "work"); len(got) != 1 {
		t.Errorf("the work chat holds %d launch lines, want the one it was given", len(got))
	}
	if got := launchLinesIn(t, r, "home"); len(got) != 0 {
		t.Errorf("the home chat holds %+v, want none", got)
	}
}
