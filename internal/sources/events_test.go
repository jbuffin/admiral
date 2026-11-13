package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type catalogConn struct {
	res json.RawMessage
	err error
}

func (c *catalogConn) call(_ context.Context, method string, _ any, _ time.Duration) (json.RawMessage, error) {
	if method != "events.catalog" {
		return nil, fmt.Errorf("unexpected %s", method)
	}
	return c.res, c.err
}
func (c *catalogConn) stop()                   {}
func (c *catalogConn) kill()                   {}
func (c *catalogConn) exited() <-chan struct{} { return nil }
func (c *catalogConn) exitReason() string      { return "" }

const catalogJSON = `[{"kind":"review_requested","title":"Review requested"},{"kind":"new_in_view","title":"New in a view","panel":"mine"}]`

func catalogRunner(t *testing.T, res string, err error) *runner {
	t.Helper()
	r := &runner{key: "k", sid: 1, notify: func() {}}
	r.pullCatalog(t.Context(), &catalogConn{res: json.RawMessage(res), err: err})
	return r
}

type sink struct {
	mu  sync.Mutex
	got []Event
}

func (s *sink) add(ev Event) { s.mu.Lock(); s.got = append(s.got, ev); s.mu.Unlock() }
func (s *sink) n() int       { s.mu.Lock(); defer s.mu.Unlock(); return len(s.got) }

func withCatalog(t *testing.T) (*runner, *sink) {
	r := catalogRunner(t, catalogJSON, nil)
	s := &sink{}
	r.onEvent = s.add
	return r, s
}

func send(r *runner, kind, ref string, extra string) {
	r.note(1, "event", json.RawMessage(fmt.Sprintf(`{"kind":%q,"ref":%q%s}`, kind, ref, extra)))
}

func TestACatalogIsKeptWhenThePluginAnswersEventsCatalog(t *testing.T) {
	r := catalogRunner(t, catalogJSON, nil)
	kinds, ok := r.Events()
	if !ok || len(kinds) != 2 || kinds[0].Kind != "review_requested" || kinds[1].Panel != "mine" {
		t.Errorf("Events = %+v, %v, want the two kinds the plugin listed", kinds, ok)
	}
}

func TestAPluginWithoutAUsableCatalogHasNone(t *testing.T) {
	notFound := &rpcError{Code: codeMethodNotFound, Message: "no such method"}
	for name, r := range map[string]*runner{
		"method not found": catalogRunner(t, "", notFound),
		"not a list":       catalogRunner(t, `{}`, nil),
		"a timeout":        catalogRunner(t, "", errTimeout),
		"an error":         catalogRunner(t, "", errors.New("boom")),
	} {
		if _, ok := r.Events(); ok {
			t.Errorf("%s: the source has a catalog, want none", name)
		}
	}
	good := catalogRunner(t, catalogJSON, nil)
	if _, ok := good.Events(); !ok {
		t.Fatal("a good catalog was not kept")
	}
}

func TestAnEventInTheCatalogIsDeliveredWithItsFieldsAndNeverVerified(t *testing.T) {
	r, s := withCatalog(t)
	send(r, "review_requested", "https://example.com/pr/1", `,"actor":"bob","author":"amy","tier":"T1","verified":true,"viewer":"eng"`)
	if s.n() != 1 {
		t.Fatalf("%d events delivered, want 1", s.n())
	}
	ev := s.got[0]
	if ev.Kind != "review_requested" || ev.Ref != "https://example.com/pr/1" || ev.Actor != "bob" || ev.Author != "amy" {
		t.Errorf("event = %+v, want the fields the plugin sent", ev)
	}
	if ev.Verified || ev.Viewer != "" {
		t.Errorf("event = %+v, want a plugin's event unverified with no viewer whatever it claims", ev)
	}
}

func TestAnEventTheCatalogCannotVouchForIsDropped(t *testing.T) {
	long := strings.Repeat("a", maxEventField+1)
	r, s := withCatalog(t)
	send(r, "starred", "https://example.com/1", "")
	send(r, "review_requested", "not a url", "")
	send(r, "review_requested", "", "")
	r.note(1, "event", json.RawMessage(`{"kind":"review_requested","ref":"https://example.com/\u0007bell"}`))
	send(r, "review_requested", "https://example.com/"+long, "")
	send(r, "review_requested", "https://example.com/2", `,"actor":"b\u0000ob"`)
	send(r, "review_requested", "https://example.com/3", `,"author":"`+long+`"`)
	r.note(1, "event", json.RawMessage(`not json`))
	if s.n() != 0 {
		t.Errorf("%d events delivered, want every bad one dropped: %+v", s.n(), s.got)
	}
	send(r, "review_requested", "https://example.com/ok", "") // proof the sink works
	if s.n() != 1 {
		t.Errorf("a good event after the bad ones: %d delivered, want 1", s.n())
	}
}

func TestAPanelKindNeedsItsPanelToExist(t *testing.T) {
	r, s := withCatalog(t)
	send(r, "new_in_view", "https://example.com/1", `,"panel":"other"`)
	send(r, "new_in_view", "https://example.com/2", "")
	send(r, "new_in_view", "https://example.com/3", `,"panel":"mine"`)
	if s.n() != 1 || s.got[0].Panel != "mine" {
		t.Errorf("delivered %+v, want only the event for panel mine", s.got)
	}
}

func TestAPluginWithoutACatalogCannotRaiseEvents(t *testing.T) {
	r := catalogRunner(t, "", &rpcError{Code: codeMethodNotFound, Message: "no"})
	s := &sink{}
	r.onEvent = s.add
	send(r, "review_requested", "https://example.com/1", "")
	if s.n() != 0 {
		t.Errorf("%d events delivered by a source with no catalog, want 0", s.n())
	}
	good, gs := withCatalog(t)
	send(good, "review_requested", "https://example.com/1", "")
	if gs.n() != 1 {
		t.Error("the control source with a catalog delivered nothing")
	}
}

func TestAnEventFromAStoppedSessionIsIgnored(t *testing.T) {
	r, s := withCatalog(t)
	r.note(0, "event", json.RawMessage(`{"kind":"review_requested","ref":"https://example.com/1"}`))
	if s.n() != 0 {
		t.Errorf("a stale session delivered %d events, want 0", s.n())
	}
	send(r, "review_requested", "https://example.com/1", "")
	if s.n() != 1 {
		t.Error("the current session delivered nothing")
	}
}

func TestAFloodOfEventsDeliversOnlyTheBoundInAMinute(t *testing.T) {
	r, s := withCatalog(t)
	for i := range maxEventsPerMinute + 25 {
		send(r, "review_requested", fmt.Sprintf("https://example.com/%d", i), "")
	}
	if s.n() != maxEventsPerMinute {
		t.Errorf("%d events delivered from a flood of %d, want exactly %d", s.n(), maxEventsPerMinute+25, maxEventsPerMinute)
	}
}

func TestBadEventsDoNotSpendTheFloodBound(t *testing.T) {
	r, s := withCatalog(t)
	for range maxEventsPerMinute * 2 {
		send(r, "starred", "https://example.com/x", "")
	}
	send(r, "review_requested", "https://example.com/1", "")
	if s.n() != 1 {
		t.Errorf("%d delivered, want the one good event after a run of bad ones", s.n())
	}
}

func TestValidEventsAroundAnInvalidOneAreBothDelivered(t *testing.T) {
	r, s := withCatalog(t)
	send(r, "review_requested", "https://example.com/1", "")
	send(r, "starred", "https://example.com/bad", "")
	send(r, "review_requested", "https://example.com/2", "")
	if s.n() != 2 {
		t.Errorf("%d delivered, want 2: valid, then valid again", s.n())
	}
}

func TestTheBuiltInGitHubSourceEmitsOnlyItemsNewToAViewAndFillsTheViewer(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	sources := `"gh":{"plugin":"github","fleet":"alpha","settings":{"views":[{"id":"mine","title":"Mine","query":"is:open Q1"}]}}`
	f := newServiceFixture(t, ghConfig(`"alpha":{"name":"Alpha","claude":{}}`, sources))
	s := &sink{}
	f.Svc.Sup.OnEvent = func(_ string, ev Event) { s.add(ev) }
	g.searchBy("mine", "Q1", "acme/api", 1, "one", "amy")
	g.write("viewer", "eng\n")
	f.startSources()
	f.waitView("gh", "item 1", func(v SourceView) bool { return len(v.orders("mine")) == 1 })
	if s.n() != 0 {
		t.Fatalf("%d events from the baseline poll, want 0: %+v", s.n(), s.got)
	}
	g.searchBy("mine", "Q1", "acme/api", 1, "one", "amy")
	g.searchMore("mine", "acme/api", 2, "two", "bob")
	f.do(http.MethodPost, "/gh/refresh", map[string]any{})
	f.waitView("gh", "item 2", func(v SourceView) bool { return len(v.orders("mine")) == 2 })
	deadline := time.Now().Add(5 * time.Second)
	for s.n() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.n() != 1 {
		t.Fatalf("%d events after item 2 appeared, want exactly 1: %+v", s.n(), s.got)
	}
	ev := s.got[0]
	if ev.Kind != "new_in_view" || ev.Panel != "mine" || !strings.HasSuffix(ev.Ref, "/issues/2") || ev.Author != "bob" || ev.Viewer != "eng" || !ev.Verified {
		t.Errorf("event = %+v, want new_in_view on mine for issue 2, author bob, viewer eng, verified", ev)
	}
	// leaving the view and coming back is new to it again
	g.searchBy("mine", "Q1", "acme/api", 1, "one", "amy")
	f.do(http.MethodPost, "/gh/refresh", map[string]any{})
	f.waitView("gh", "item 2 gone", func(v SourceView) bool { return len(v.orders("mine")) == 1 })
	g.searchMore("mine", "acme/api", 2, "two", "bob")
	f.do(http.MethodPost, "/gh/refresh", map[string]any{})
	f.waitView("gh", "item 2 back", func(v SourceView) bool { return len(v.orders("mine")) == 2 })
	deadline = time.Now().Add(5 * time.Second)
	for s.n() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.n() != 2 {
		t.Errorf("%d events after item 2 left and came back, want 2 in all", s.n())
	}
}

// searchBy answers the match with one item whose author is login.
func (g *fakeGH) searchBy(key, match, repo string, n int, title, author string) {
	g.t.Helper()
	g.write("search/"+key+".match", match)
	g.write("search/"+key+".json", `{"data":{"search":{"nodes":[`+authored(repo, n, title, author)+`]}}}`)
}

// searchMore adds a second item, by author, to the answer searchBy wrote for key.
func (g *fakeGH) searchMore(key, repo string, n int, title, author string) {
	g.t.Helper()
	b, err := os.ReadFile(filepath.Join(g.dir, "search", key+".json"))
	if err != nil {
		g.t.Fatal(err)
	}
	s := strings.Replace(string(b), `]}}}`, `,`+authored(repo, n, title, author)+`]}}}`, 1)
	g.write("search/"+key+".json", s)
}

func authored(repo string, n int, title, author string) string {
	return fmt.Sprintf(`{"number":%d,"title":%q,"url":"https://github.com/%s/issues/%d","author":{"login":%q},"repository":{"nameWithOwner":%q,"url":"https://github.com/%s"},"labels":{"nodes":[]}}`,
		n, title, repo, n, author, repo, repo)
}

func TestAReviewRequestedEventNamesTheRequesterAsActorAndTheOpenerAsAuthor(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	sources := `"gh":{"plugin":"github","fleet":"alpha","settings":{"views":[{"id":"mine","title":"Mine","query":"is:open Q1"}]}}`
	f := newServiceFixture(t, ghConfig(`"alpha":{"name":"Alpha","claude":{}}`, sources))
	s := &sink{}
	f.Svc.Sup.OnEvent = func(_ string, ev Event) { s.add(ev) }
	g.searchBy("mine", "Q1", "acme/api", 1, "one", "amy")
	reviewing := func(withSecond bool) {
		nodes := reviewRequested("acme/api", 5, "five", "amy", "eng", "eng", "carol")
		if withSecond {
			nodes += "," + reviewRequested("acme/api", 6, "six", "amy", "bob", "eng", "carol")
		}
		g.write("search/rr.match", "review-requested:@me")
		g.write("search/rr.json", `{"data":{"search":{"nodes":[`+nodes+`]}}}`)
	}
	g.write("viewer", "eng\n")
	reviewing(false)
	f.startSources()
	f.waitView("gh", "item 1", func(v SourceView) bool { return len(v.orders("mine")) == 1 })
	time.Sleep(200 * time.Millisecond)
	if s.n() != 0 {
		t.Fatalf("%d events from the baseline poll, want 0: %+v", s.n(), s.got)
	}
	reviewing(true)
	f.do(http.MethodPost, "/gh/refresh", map[string]any{})
	// the built-in Review requested view shares the search, so it also sees pull 6 as new to it
	of := func(kind string) (out []Event) {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, ev := range s.got {
			if ev.Kind == kind {
				out = append(out, ev)
			}
		}
		return out
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(of("review_requested")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := of("review_requested")
	if len(got) != 1 {
		t.Fatalf("%d review_requested events, want exactly the one for pull 6: %+v", len(got), s.got)
	}
	ev := got[0]
	if ev.Kind != "review_requested" || !strings.HasSuffix(ev.Ref, "/issues/6") || ev.Actor != "bob" || ev.Author != "amy" || ev.Viewer != "eng" || !ev.Verified {
		t.Errorf("event = %+v, want review_requested for 6, requested by bob on amy's item, viewer eng, verified", ev)
	}
}

// reviewRequested is an item whose timeline has one review request by requester, aimed at reviewer; other is a
// request for someone else, made by eng, that the actor must not be read from.
func reviewRequested(repo string, n int, title, author, requester, reviewer, other string) string {
	item := authored(repo, n, title, author)
	return strings.TrimSuffix(item, "}") + fmt.Sprintf(`,"timelineItems":{"nodes":[`+
		`{"__typename":"ReviewRequestedEvent","actor":{"login":%q},"requestedReviewer":{"login":%q}},`+
		`{"__typename":"ReviewRequestedEvent","actor":{"login":"eng"},"requestedReviewer":{"login":%q}}]}}`, requester, reviewer, other)
}

func TestAMentionOfTheEngineerOnTheirOwnItemNamesNoActorSoItIsNotTheirs(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	sources := `"gh":{"plugin":"github","fleet":"alpha","settings":{"views":[{"id":"mine","title":"Mine","query":"is:open Q1"}]}}`
	f := newServiceFixture(t, ghConfig(`"alpha":{"name":"Alpha","claude":{}}`, sources))
	s := &sink{}
	f.Svc.Sup.OnEvent = func(_ string, ev Event) { s.add(ev) }
	g.searchBy("mine", "Q1", "acme/api", 1, "one", "eng")
	g.write("viewer", "eng\n")
	mentions := func(withNew bool) {
		nodes := authored("acme/api", 3, "three", "eng")
		if withNew {
			nodes += "," + authored("acme/api", 4, "four", "eng")
		}
		g.write("search/mn.match", "mentions:@me")
		g.write("search/mn.json", `{"data":{"search":{"nodes":[`+nodes+`]}}}`)
	}
	mentions(false)
	f.startSources()
	f.waitView("gh", "item 1", func(v SourceView) bool { return len(v.orders("mine")) == 1 })
	time.Sleep(200 * time.Millisecond)
	mentions(true)
	f.do(http.MethodPost, "/gh/refresh", map[string]any{})
	deadline := time.Now().Add(5 * time.Second)
	var got []Event
	for len(got) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		s.mu.Lock()
		for _, ev := range s.got {
			if ev.Kind == "mentioned" {
				got = append(got, ev)
			}
		}
		s.mu.Unlock()
	}
	if len(got) != 1 || got[0].Author != "eng" || got[0].Actor == "" || strings.EqualFold(got[0].Actor, "eng") {
		t.Fatalf("mentioned events = %+v, want one on the engineer's own item whose actor is no one the lists could trust", got)
	}
}
