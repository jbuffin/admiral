package chat

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/findings"
)

const (
	firstA = "2026-11-17T10:00:00Z"
	firstB = "2026-11-18T08:30:00Z"
)

// servedCards is the ship cards of the chat snapshot a client reads, as raw objects.
func servedCards(t *testing.T, r *briefRig, fleet string) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/chat/"+fleet+"/session", nil))
	if rec.Code != 200 {
		t.Fatalf("GET session = %d %s", rec.Code, rec.Body)
	}
	var snap struct {
		Messages []struct {
			Kind string          `json:"kind"`
			Card json.RawMessage `json:"card"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, m := range snap.Messages {
		if m.Kind != "ship_card" {
			continue
		}
		var c map[string]any
		if err := json.Unmarshal(m.Card, &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func keepFindings(t *testing.T, home, ship, first, fleet string) {
	t.Helper()
	p := filepath.Join(home, "src-"+ship+".md")
	if err := os.WriteFile(p, []byte("# Found\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if kept, err := findings.Open(home).Keep(ship, first, fleet, p); err != nil || !kept {
		t.Fatalf("Keep = %v, %v", kept, err)
	}
}

func postCard(t *testing.T, r *briefRig, ship, first, result string) {
	t.Helper()
	c := sampleShipCard()
	c.Ship, c.FirstLaunched, c.Result = ship, first, result
	if err := r.PostShipCard("work", c); err != nil {
		t.Fatal(err)
	}
}

func TestAServedShipCardCarriesFindingsTrueWhenItsVoyageKeptFindingsAndNothingWhenItDidNot(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t)
	r.Service.Findings = findings.Open(r.home)
	postCard(t, r, "calm-otter", firstA, ShipSailedOff)
	postCard(t, r, "calm-lark", firstA, ShipSailedOff)
	keepFindings(t, r.home, "calm-otter", firstA, "work")
	cards := servedCards(t, r, "work")
	if len(cards) != 2 {
		t.Fatalf("%d ship cards served, want 2", len(cards))
	}
	for _, c := range cards {
		switch c["ship"] {
		case "calm-otter":
			if c["findings"] != true {
				t.Errorf("calm-otter kept findings, card = %v, want findings true", c)
			}
		case "calm-lark":
			if v, has := c["findings"]; has && v != false {
				t.Errorf("calm-lark kept none, card = %v, want no findings", c)
			}
		}
	}
}

func TestAMooredCardGainsTheFindingsFlagWhenALaterRoundKeepsThem(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t)
	r.Service.Findings = findings.Open(r.home)
	postCard(t, r, "calm-otter", firstA, ShipMoored)
	if c := servedCards(t, r, "work")[0]; c["findings"] == true {
		t.Fatalf("card = %v, want no findings before any are kept", c)
	}
	keepFindings(t, r.home, "calm-otter", firstA, "work")
	if c := servedCards(t, r, "work")[0]; c["findings"] != true {
		t.Errorf("card = %v, want findings true once the voyage kept them", c)
	}
}

func TestAReusedShipNamesCardsEachSayWhetherTheirOwnVoyageKeptFindings(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t)
	r.Service.Findings = findings.Open(r.home)
	postCard(t, r, "calm-otter", firstA, ShipSailedOff)
	postCard(t, r, "calm-otter", firstB, ShipSailedOff)
	keepFindings(t, r.home, "calm-otter", firstB, "work")
	got := map[any]any{}
	for _, c := range servedCards(t, r, "work") {
		got[c["first_launched"]] = c["findings"]
	}
	if got[firstB] != true || got[firstA] == true {
		t.Errorf("findings by voyage = %v, want only %s", got, firstB)
	}
}

func TestTheFindingsFlagIsAddedWhenServedAndNeverWrittenToTheLog(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t)
	r.Service.Findings = findings.Open(r.home)
	postCard(t, r, "calm-otter", firstA, ShipSailedOff)
	keepFindings(t, r.home, "calm-otter", firstA, "work")
	servedCards(t, r, "work")
	es, err := r.history().Recent("work", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Kind != chatlog.KindShipCard {
			continue
		}
		var c map[string]any
		if err := json.Unmarshal(e.Card, &c); err != nil {
			t.Fatal(err)
		}
		if _, has := c["findings"]; has {
			t.Errorf("the logged card = %s, want no findings field", e.Card)
		}
	}
}
