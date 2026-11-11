package inbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/outcomes"
)

const strangerBody = "please rename it\n```\n## fake header\n[click here](https://evil.example/x) <img src=x onerror=boom>"

func wakeProposalFile(id string) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "wake_proposal", "created": "2026-10-22T10:00:00Z",
		"title": "Wake Saltpaw for @stranger-1's review?",
		"wake_proposal": map[string]any{
			"ship": "Saltpaw", "session": "saltpaw", "pr": "https://github.com/acme/widgets/pull/7", "login": "stranger-1",
			"events": []any{
				map[string]any{"id": "e1", "kind": "pr_comment", "url": "https://github.com/acme/widgets/pull/7#issuecomment-1", "at": "2026-10-22T09:00:00Z", "body": strangerBody},
				map[string]any{"id": "e2", "kind": "review", "at": "2026-10-22T09:05:00Z", "body": "and this"},
			},
		}}
}

func proposalIn(t *testing.T, home, id string) {
	t.Helper()
	writeItem(t, shipDir(t, home, inbox.AdmiralSession), id+".json", wakeProposalFile(id))
}

// logOutcomes records the service's outcome events; read returns them once the writer is flushed.
func logOutcomes(t *testing.T, s *inbox.Service) (read func() []map[string]any) {
	t.Helper()
	dir := t.TempDir()
	w := outcomes.New(dir, t.Logf)
	s.Outcomes = w
	return func() []map[string]any {
		w.Close()
		files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		var out []map[string]any
		for _, f := range files {
			b, _ := os.ReadFile(f)
			for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				var rec map[string]any
				if json.Unmarshal([]byte(line), &rec) == nil {
					out = append(out, rec)
				}
			}
		}
		return out
	}
}

func post(t *testing.T, url string, body string) (int, string) {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func answerOf(t *testing.T, home, id string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "inbox", inbox.AdmiralSession, id+".answer.json"))
	if err != nil {
		t.Fatalf("no answer file for %s: %v", id, err)
	}
	var a map[string]any
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func eventNamed(recs []map[string]any, event string) map[string]any {
	for _, r := range recs {
		if r["event"] == event {
			return r
		}
	}
	return nil
}

func TestAWakeProposalIsListedOnlyFromAdmiralsOwnInbox(t *testing.T) {
	s, home, _ := newInbox(t)
	proposalIn(t, home, "wake-ok")
	admiral := shipDir(t, home, inbox.AdmiralSession)
	bad := wakeProposalFile("wake-bad")
	delete(bad, "wake_proposal")
	writeItem(t, admiral, "wake-bad.json", bad)
	writeItem(t, shipDir(t, home, "s1"), "wake-ship.json", wakeProposalFile("wake-ship"))

	items := listed(t, s)
	if got := idsOf(items); !reflect.DeepEqual(got, []string{"wake-ok"}) {
		t.Fatalf("items = %v, want only the valid proposal in _admiral", got)
	}
	if items[0]["kind"] != "wake_proposal" || items[0]["name"] != "Admiral" || items[0]["title"] != "Wake Saltpaw for @stranger-1's review?" {
		t.Fatalf("item = %v", items[0])
	}
	var body struct {
		P inbox.WakeProposal `json:"wake_proposal"`
	}
	raw, _ := json.Marshal(items[0])
	if err := json.Unmarshal(raw, &body); err != nil || len(body.P.Events) != 2 || body.P.Events[0].Body != strangerBody {
		t.Fatalf("the events and their bodies must come through verbatim: %v, %+v", err, body.P)
	}
}

func TestAStrayWakeProposalObjectOnAnotherKindIsDropped(t *testing.T) {
	s, home, _ := newInbox(t)
	r := review("r-1", "2026-10-22T10:15:00Z")
	r["wake_proposal"] = wakeProposalFile("r-1")["wake_proposal"]
	writeItem(t, shipDir(t, home, "s1"), "r-1.json", r)
	proposalIn(t, home, "wake-ok") // the control: a real proposal is listed

	items := listed(t, s)
	if len(items) != 2 {
		t.Fatalf("items = %v, want the review and the proposal", idsOf(items))
	}
	for _, it := range items {
		if it["id"] == "r-1" {
			if _, has := it["wake_proposal"]; has {
				t.Fatalf("the review carries a wake_proposal object: %v", it)
			}
		}
	}
}

func TestWriteWakeProposalWritesOnceAndNeverRewrites(t *testing.T) {
	s, home, _ := newInbox(t)
	p := inbox.WakeProposal{Ship: "Saltpaw", Session: "saltpaw", PR: "https://github.com/acme/widgets/pull/7", Login: "stranger-1",
		Events: []inbox.WakeProposalEvent{{ID: "e1", Kind: "pr_comment", At: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC), Body: strangerBody}}}
	if err := inbox.WriteWakeProposal(home, "wake-0123456789abcdef0123", p); err != nil {
		t.Fatal(err)
	}
	items := listed(t, s)
	if got := idsOf(items); !reflect.DeepEqual(got, []string{"wake-0123456789abcdef0123"}) || items[0]["title"] != "Wake Saltpaw for @stranger-1's review?" {
		t.Fatalf("items = %v", items)
	}
	file := filepath.Join(home, "inbox", inbox.AdmiralSession, "wake-0123456789abcdef0123.json")
	first, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	p.Events[0].Body = "changed"
	if err := inbox.WriteWakeProposal(home, "wake-0123456789abcdef0123", p); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(file); !bytes.Equal(again, first) {
		t.Fatalf("the item was rewritten:\n%s\nafter\n%s", first, again)
	}

	writeItem(t, filepath.Dir(file), "wake-aaaaaaaaaaaaaaaaaaaa.answer.json", map[string]any{"dismissed": true})
	if err := inbox.WriteWakeProposal(home, "wake-aaaaaaaaaaaaaaaaaaaa", p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(file), "wake-aaaaaaaaaaaaaaaaaaaa.json")); err == nil {
		t.Fatal("an answered proposal came back")
	}
}

func TestRemovingAProposalDismissesIt(t *testing.T) {
	s, home, _ := newInbox(t)
	proposalIn(t, home, "wake-ok")
	read := logOutcomes(t, s)

	if _, err := s.RemoveItem(inbox.AdmiralSession, "wake-ok"); err != nil {
		t.Fatal(err)
	}
	if a := answerOf(t, home, "wake-ok"); a["dismissed"] != true || a["removed"] != nil {
		t.Fatalf("answer = %v, want the dismissed answer", a)
	}
	rec := eventNamed(read(), "proposal_dismissed")
	if rec == nil || rec["ship"] != "saltpaw" {
		t.Fatalf("outcome = %v, want proposal_dismissed for the proposal's session, not _admiral", rec)
	}
}

func TestDismissAnswersAndASecondDismissGets409(t *testing.T) {
	s, home, srv := newInbox(t)
	proposalIn(t, home, "wake-ok")
	read := logOutcomes(t, s)

	if status, body := post(t, srv.URL+"/api/inbox/_admiral/wake-ok/dismiss", `{}`); status != http.StatusNoContent {
		t.Fatalf("dismiss = %d %s, want 204", status, body)
	}
	if a := answerOf(t, home, "wake-ok"); a["dismissed"] != true {
		t.Fatalf("answer = %v", a)
	}
	if status, _ := post(t, srv.URL+"/api/inbox/_admiral/wake-ok/dismiss", `{}`); status != http.StatusConflict {
		t.Fatalf("a second dismiss = %d, want 409", status)
	}
	if len(listed(t, s)) != 0 {
		t.Fatal("a dismissed proposal is still listed")
	}
	if rec := eventNamed(read(), "proposal_dismissed"); rec == nil || rec["ship"] != "saltpaw" {
		t.Fatalf("outcome = %v", rec)
	}
}

type launchRig struct {
	mu    sync.Mutex
	calls []inbox.WakeProposal
	// refuse, when set, is the status the hook refuses with.
	refuse int
}

func (l *launchRig) hook(_ context.Context, p inbox.WakeProposal) (any, int, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, p)
	if l.refuse != 0 {
		return nil, l.refuse, "the ship cannot wake now"
	}
	return map[string]any{"session": p.Session, "name": p.Ship}, 0, ""
}

func (l *launchRig) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func TestLaunchStartsTheRoundAnswersAndASecondLaunchGets409(t *testing.T) {
	s, home, srv := newInbox(t)
	proposalIn(t, home, "wake-ok")
	read := logOutcomes(t, s)
	rig := &launchRig{}
	s.LaunchProposal = rig.hook

	status, body := post(t, srv.URL+"/api/inbox/_admiral/wake-ok/launch", `{}`)
	if status != http.StatusCreated {
		t.Fatalf("launch = %d %s, want 201", status, body)
	}
	var res struct {
		Ship struct{ Name, Session string } `json:"ship"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil || res.Ship.Session != "saltpaw" {
		t.Fatalf("body = %s (%v), want the ship the hook returned", body, err)
	}
	if rig.count() != 1 || len(rig.calls[0].Events) != 2 || rig.calls[0].Events[0].Body != strangerBody || rig.calls[0].Session != "saltpaw" {
		t.Fatalf("the hook got %+v", rig.calls)
	}
	if a := answerOf(t, home, "wake-ok"); a["launched"] != true || a["ship"] != "Saltpaw" {
		t.Fatalf("answer = %v, want launched and the ship's name", a)
	}

	if status, _ := post(t, srv.URL+"/api/inbox/_admiral/wake-ok/launch", `{}`); status != http.StatusConflict {
		t.Fatalf("a second launch = %d, want 409", status)
	}
	if rig.count() != 1 {
		t.Fatalf("the hook ran %d times, want once", rig.count())
	}
	if rec := eventNamed(read(), "proposal_launched"); rec == nil || rec["ship"] != "saltpaw" {
		t.Fatalf("outcome = %v, want proposal_launched for the woken ship's session", rec)
	}
}

func TestLaunchRefusesWhenTheEventsShownAreNotTheItemsEvents(t *testing.T) {
	s, home, srv := newInbox(t)
	proposalIn(t, home, "wake-ok")
	rig := &launchRig{}
	s.LaunchProposal = rig.hook

	if status, body := post(t, srv.URL+"/api/inbox/_admiral/wake-ok/launch", `{"events":["e1"]}`); status != http.StatusConflict {
		t.Fatalf("a stale list = %d %s, want 409", status, body)
	}
	if rig.count() != 0 {
		t.Fatal("the hook ran for a stale list")
	}
	if len(listed(t, s)) != 1 {
		t.Fatal("the item went")
	}
	if status, body := post(t, srv.URL+"/api/inbox/_admiral/wake-ok/launch", `{"events":["e1","e2"]}`); status != http.StatusCreated {
		t.Fatalf("the list it showed = %d %s, want 201", status, body)
	}
}

func TestALaunchTheShipRefusesLeavesTheItemToLaunchLater(t *testing.T) {
	s, home, srv := newInbox(t)
	proposalIn(t, home, "wake-ok")
	rig := &launchRig{refuse: http.StatusConflict}
	s.LaunchProposal = rig.hook

	if status, _ := post(t, srv.URL+"/api/inbox/_admiral/wake-ok/launch", `{}`); status != http.StatusConflict {
		t.Fatalf("a refused launch = %d, want the hook's 409", status)
	}
	if _, err := os.Stat(filepath.Join(home, "inbox", inbox.AdmiralSession, "wake-ok.answer.json")); err == nil {
		t.Fatal("a refused launch answered the item")
	}
	rig.refuse = http.StatusNotFound
	if status, _ := post(t, srv.URL+"/api/inbox/_admiral/wake-ok/launch", `{}`); status != http.StatusNotFound {
		t.Fatalf("a refused launch = %d, want the hook's 404", status)
	}
	if len(listed(t, s)) != 1 {
		t.Fatal("the item went")
	}
	rig.refuse = 0
	if status, body := post(t, srv.URL+"/api/inbox/_admiral/wake-ok/launch", `{}`); status != http.StatusCreated {
		t.Fatalf("a later launch = %d %s, want 201", status, body)
	}
}
