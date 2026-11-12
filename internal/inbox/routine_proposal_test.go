package inbox_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
)

func routineProposal(trigger string, expires time.Time) inbox.RoutineProposal {
	return inbox.RoutineProposal{Fleet: "work", Routine: "r1", RoutineName: "Nightly sweep", Trigger: trigger,
		Action: "launch claude in /tmp/repo: sweep the board", ExpiresAt: expires}
}

type routineLaunches struct {
	mu    sync.Mutex
	got   []inbox.RoutineProposal
	reply func() (any, int, string)
}

func (l *routineLaunches) launch(_ context.Context, p inbox.RoutineProposal) (any, int, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, p)
	if l.reply != nil {
		return l.reply()
	}
	return map[string]any{"session": "ship-1"}, 0, ""
}

func (l *routineLaunches) calls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.got)
}

func routineInbox(t *testing.T) (*inbox.Service, string, string, *routineLaunches) {
	t.Helper()
	s, home, srv := newInbox(t)
	s.Fleet = func(key string) (inbox.FleetInfo, bool) {
		return inbox.FleetInfo{Key: key, Name: "Work", Color: "#38bdf8"}, key == "work"
	}
	l := &routineLaunches{}
	s.LaunchRoutine = l.launch
	return s, home, srv.URL, l
}

func publish(t *testing.T, s *inbox.Service, p inbox.RoutineProposal) string {
	t.Helper()
	id, err := s.PublishRoutineProposal(p)
	if err != nil {
		t.Fatalf("PublishRoutineProposal: %v", err)
	}
	return id
}

func TestARoutineProposalIsListedWithItsFleetNameAndWhatItWouldDoAndOnlyFromAdmiralsOwnInbox(t *testing.T) {
	s, home, _, _ := routineInbox(t)
	id := publish(t, s, routineProposal("t1", time.Now().Add(24*time.Hour)))
	if id != "routine-work-t1" {
		t.Fatalf("item id = %q, want routine-<fleet>-<trigger>", id)
	}
	publish(t, s, routineProposal("t1", time.Now().Add(24*time.Hour))) // written once
	writeItem(t, shipDir(t, home, "s1"), "routine-work-t9.json", map[string]any{"version": 1, "id": "routine-work-t9", "kind": "routine_proposal",
		"created": "2026-10-22T10:00:00Z", "title": "Run Nightly sweep?", "routine_proposal": routineProposal("t9", time.Now().Add(time.Hour))})

	items := listed(t, s)
	if got := idsOf(items); !reflect.DeepEqual(got, []string{"routine-work-t1"}) {
		t.Fatalf("items = %v, want the one proposal, from _admiral only", got)
	}
	it := items[0]
	if it["kind"] != "routine_proposal" || it["fleet"] != "work" || it["fleet_name"] != "Work" || !strings.Contains(it["title"].(string), "Nightly sweep") {
		t.Errorf("item = %v, want kind routine_proposal in fleet work titled with the routine's name", it)
	}
	var body struct {
		P inbox.RoutineProposal `json:"routine_proposal"`
	}
	raw, _ := json.Marshal(it)
	if err := json.Unmarshal(raw, &body); err != nil || body.P.Trigger != "t1" || body.P.Routine != "r1" || body.P.Action == "" || body.P.ExpiresAt.IsZero() {
		t.Errorf("routine_proposal = %+v (%v), want the routine, the trigger, the action and the expiry", body.P, err)
	}
}

func TestAnActorSeesARoutineProposalOnlyInItsOwnFleet(t *testing.T) {
	s, _, mux := actorInbox(t)
	s.Fleet = func(key string) (inbox.FleetInfo, bool) { return inbox.FleetInfo{Key: key, Name: key}, true }
	publish(t, s, routineProposal("t1", time.Now().Add(time.Hour)))
	s.Refresh()
	for fleet, want := range map[string]int{"work": 1, "personal": 0} {
		rec := asActor(mux, fleet, http.MethodGet, "/api/inbox", "")
		var out struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Items) != want {
			t.Errorf("fleet %s lists %d items (%v), want %d", fleet, len(out.Items), err, want)
		}
	}
}

func TestLaunchingARoutineProposalRunsItOnceAndASecondPressDoesNothing(t *testing.T) {
	s, home, url, l := routineInbox(t)
	id := publish(t, s, routineProposal("t1", time.Now().Add(time.Hour)))
	code, body := post(t, url+"/api/inbox/_admiral/"+id+"/launch", `{}`)
	if code != http.StatusCreated || !strings.Contains(body, "ship-1") {
		t.Fatalf("launch = %d %s, want 201 with the ship", code, body)
	}
	if l.calls() != 1 || l.got[0].Routine != "r1" || l.got[0].Trigger != "t1" || l.got[0].Fleet != "work" {
		t.Fatalf("launch hook got %+v, want one call for routine r1's trigger t1", l.got)
	}
	if a := answerOf(t, home, id); a["launched"] != true {
		t.Errorf("answer = %v, want launched", a)
	}
	if code, _ := post(t, url+"/api/inbox/_admiral/"+id+"/launch", `{}`); code != http.StatusConflict || l.calls() != 1 {
		t.Errorf("second launch = %d with %d hook calls, want 409 and still one", code, l.calls())
	}
	if got := idsOf(listed(t, s)); len(got) != 0 {
		t.Errorf("an answered proposal is still listed: %v", got)
	}
}

func TestARefusedRoutineLaunchLeavesTheProposalOpenToLaunchLater(t *testing.T) {
	s, _, url, l := routineInbox(t)
	id := publish(t, s, routineProposal("t1", time.Now().Add(time.Hour)))
	l.reply = func() (any, int, string) { return nil, http.StatusConflict, "no free ship slot" }
	if code, body := post(t, url+"/api/inbox/_admiral/"+id+"/launch", `{}`); code != http.StatusConflict || !strings.Contains(body, "no free ship slot") {
		t.Fatalf("refused launch = %d %s, want 409 with the refusal", code, body)
	}
	if got := idsOf(listed(t, s)); !reflect.DeepEqual(got, []string{id}) {
		t.Fatalf("items = %v, want the proposal still open", got)
	}
	l.reply = nil
	if code, body := post(t, url+"/api/inbox/_admiral/"+id+"/launch", `{}`); code != http.StatusCreated {
		t.Fatalf("launch once there is room = %d %s", code, body)
	}
}

func TestAnExpiredRoutineProposalIsNotListedAndCannotBeLaunched(t *testing.T) {
	s, home, url, l := routineInbox(t)
	live := publish(t, s, routineProposal("live", time.Now().Add(time.Hour)))
	old := map[string]any{"version": 1, "id": "routine-work-old", "kind": "routine_proposal", "created": "2000-01-01T00:00:00Z",
		"title": "Run Nightly sweep?", "routine_proposal": routineProposal("old", time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC))}
	writeItem(t, shipDir(t, home, inbox.AdmiralSession), "routine-work-old.json", old)
	if got := idsOf(listed(t, s)); !reflect.DeepEqual(got, []string{live}) {
		t.Fatalf("items = %v, want only the proposal that has not expired", got)
	}
	code, _ := post(t, url+"/api/inbox/_admiral/routine-work-old/launch", `{}`)
	if code != http.StatusGone && code != http.StatusConflict {
		t.Errorf("launch after expiry = %d, want 410 or 409", code)
	}
	if l.calls() != 0 {
		t.Errorf("an expired proposal ran the routine %d times", l.calls())
	}
}

func TestDismissingARoutineProposalAnswersItAndRunsNothing(t *testing.T) {
	s, home, url, l := routineInbox(t)
	id := publish(t, s, routineProposal("t1", time.Now().Add(time.Hour)))
	if code, body := post(t, url+"/api/inbox/_admiral/"+id+"/dismiss", `{}`); code != http.StatusNoContent {
		t.Fatalf("dismiss = %d %s, want 204", code, body)
	}
	if a := answerOf(t, home, id); a["dismissed"] != true {
		t.Errorf("answer = %v, want dismissed", a)
	}
	if code, _ := post(t, url+"/api/inbox/_admiral/"+id+"/launch", `{}`); code != http.StatusConflict || l.calls() != 0 {
		t.Errorf("launch after dismiss = %d with %d hook calls, want 409 and none", code, l.calls())
	}
}

func TestANewProposalForARoutineClosesItsPreviousOpenOne(t *testing.T) {
	s, home, url, l := routineInbox(t)
	first := publish(t, s, routineProposal("t1", time.Now().Add(time.Hour)))
	second := publish(t, s, routineProposal("t2", time.Now().Add(2*time.Hour)))
	if got := idsOf(listed(t, s)); !reflect.DeepEqual(got, []string{second}) {
		t.Fatalf("items = %v, want only the newest proposal", got)
	}
	if a := answerOf(t, home, first); a["dismissed"] != true && a["expired"] != true {
		t.Errorf("the replaced proposal's answer = %v, want it closed", a)
	}
	if code, _ := post(t, url+"/api/inbox/_admiral/"+first+"/launch", `{}`); code != http.StatusConflict || l.calls() != 0 {
		t.Errorf("launching the replaced proposal = %d with %d hook calls, want 409 and none", code, l.calls())
	}
	other := routineProposal("t3", time.Now().Add(time.Hour))
	other.Routine = "r2"
	third := publish(t, s, other)
	if got := idsOf(listed(t, s)); !reflect.DeepEqual(got, []string{second, third}) {
		t.Errorf("items = %v, want a different routine's proposal to leave the first routine's open", got)
	}
}
