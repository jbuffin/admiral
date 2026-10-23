package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// The Needs You inbox, server side (#116 u2): the engineer's actions on a
// review and on finding cards. The real binary, the fake gh on PATH
// (testdata/fakegh/gh) and a ship that sits underway.
//
// Wire shape under test, next to u1's /answer route:
//
//	POST /api/inbox/<session>/<id>/submit   {"event":"APPROVE","edited":false}  a review item
//	POST /api/inbox/<session>/<id>/discard  {}                                  a review item
//	POST /api/inbox/<session>/<id>/findings {"cards":[{"id":"r1","decision":"approved"},
//	    {"id":"r2","decision":"edited","proposal":{"kind":"diff","content":"..."}}]}
//
// A 2xx is success. A failure is a non-2xx with {"error": "..."} that carries
// gh's message. Answer files and outcome fields follow ADR 0016 and #116:
// review_submitted {item, kind, review_event, edited} (a log line's own `event` is the record name), review_discarded {item, kind}
// (plus external:true when the review was already gone), and
// card_approved/card_edited/card_rejected {item, kind, card}.

const (
	ghReviews = "repos/acme/widgets/pulls/12/reviews/123"
	prURL     = "https://github.com/acme/widgets/pull/12"
)

// ---- the fake gh, seen from a test ----

// restCall is one REST call the fake gh recorded.
type restCall struct {
	Method, Endpoint, Host string
	Argv                   []string
	Env                    map[string]string
	Stdin                  string
}

// field is the value Admiral sent for a body field: -f name=value, or a key of
// a JSON object given with --input -.
func (c restCall) field(name string) (string, bool) {
	for i, a := range c.Argv {
		if (a == "-f" || a == "--raw-field") && i+1 < len(c.Argv) {
			if k, v, ok := strings.Cut(c.Argv[i+1], "="); ok && k == name {
				return v, true
			}
		}
	}
	var obj map[string]any
	if json.Unmarshal([]byte(c.Stdin), &obj) == nil {
		if v, ok := obj[name].(string); ok {
			return v, true
		}
	}
	return "", false
}

func (c restCall) String() string { return c.Method + " " + c.Host + "/" + c.Endpoint }

// rest scripts the fake gh: a call with this method and endpoint is answered
// with status and body; for a status of 300 or more stderr is what gh prints.
func (g *fakeGH) rest(key, method, endpoint string, status int, body any, stderr string) {
	g.t.Helper()
	if err := os.MkdirAll(filepath.Join(g.dir, "rest"), 0o755); err != nil {
		g.t.Fatal(err)
	}
	b, err := json.Marshal(body)
	if err != nil {
		g.t.Fatal(err)
	}
	g.write("rest/"+key+".endpoint", endpoint)
	g.write("rest/"+key+".method", method)
	g.write("rest/"+key+".status", itoa(status))
	g.write("rest/"+key+".json", string(b))
	if stderr != "" {
		g.write("rest/"+key+".stderr", stderr)
	}
}

// restCalls is every REST call gh was run for, in no particular order.
func (g *fakeGH) restCalls() []restCall {
	g.t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(g.dir, "calls", "call.*"))
	var out []restCall
	for _, d := range dirs {
		rd := func(f string) string {
			b, _ := os.ReadFile(filepath.Join(d, f))
			return strings.TrimRight(string(b), "\n")
		}
		if rd("endpoint") == "" {
			continue
		}
		c := restCall{Method: rd("method"), Endpoint: rd("endpoint"), Host: rd("host"), Stdin: rd("stdin"), Env: map[string]string{}}
		if raw := rd("argv"); raw != "" {
			c.Argv = strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
		}
		for _, l := range strings.Split(rd("env"), "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				c.Env[k] = v
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// callsTo is the REST calls with this method and endpoint.
func (g *fakeGH) callsTo(method, endpoint string) []restCall {
	var out []restCall
	for _, c := range g.restCalls() {
		if c.Method == method && c.Endpoint == endpoint {
			out = append(out, c)
		}
	}
	return out
}

// ---- the world ----

// ghFleets writes a config with one claude fleet per key, each with its cue
// lines and its own GH_TOKEN, "fake-token-<key>" (a fleet's env is what gh runs
// with, #116 u2).
func (w *world) ghFleets(cues map[string][]string) {
	w.t.Helper()
	fleets := map[string]any{}
	for key, lines := range cues {
		path := filepath.Join(w.root, "cue-"+key)
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			w.t.Fatal(err)
		}
		env := w.harnessEnv()
		env["FAKE_CUE"] = path
		env["GH_TOKEN"] = "fake-token-" + key
		fleets[key] = map[string]any{"name": key, "color": "#336699", "search_roots": []string{w.work},
			"claude": map[string]any{"executable": w.fakeExe("claude"), "env": env}}
	}
	b, err := json.Marshal(map[string]any{"version": 1, "fleets": fleets})
	if err != nil {
		w.t.Fatal(err)
	}
	writeConfig(w.t, w.home, string(b))
}

// reviewWorld is a world with the fake gh, a fleet f whose ships sit underway
// and fleet g likewise, started.
func reviewWorld(t *testing.T) (*world, *fakeGH) {
	t.Helper()
	w := newWorld(t)
	g := w.newFakeGH()
	w.ghFleets(map[string][]string{"f": {"hook working"}, "g": {"hook working"}})
	w.start()
	return w, g
}

// ghShip launches a ship in the fleet and returns it with its inbox directory.
func (w *world) ghShip(fleet, repo string) (shipJSON, string) {
	w.t.Helper()
	ship := w.launchIn(fleet, "claude", repo)
	_, dir := shipPaths(w.record(ship.Session))
	return ship, dir
}

func reviewAt(id, created, url string, reviewID any) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "review", "created": created, "title": "Review PR 12",
		"review": map[string]any{"pr_url": url, "review_id": reviewID, "summary": "summary of " + id}}
}

func findingsCards(ids ...string) map[string]any {
	cards := []any{}
	for _, id := range ids {
		cards = append(cards, map[string]any{"id": id, "source": "reviewer", "quote": "q " + id, "verdict": "agree", "reason": "r",
			"proposal": map[string]any{"kind": "diff", "content": "- a\n+ b"}, "evidence": []any{"main.go:3"}})
	}
	return map[string]any{"version": 1, "id": "f-1", "kind": "findings", "created": "2026-10-22T10:00:00Z", "title": "Feedback on PR 12",
		"findings": map[string]any{"pr_url": prURL, "cards": cards}}
}

func (in *fleetInst) act(t *testing.T, session, id, action string, body any) (int, string) {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	return send(t, http.MethodPost, in.LocalURL+"/api/inbox/"+session+"/"+id+"/"+action, body)
}

func ok2xx(code int) bool { return code/100 == 2 }

// readAnswer is the parsed <id>.answer.json, with `answered` checked and removed.
func readAnswer(t *testing.T, dir, id string, before time.Time) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, id+".answer.json"))
	if err != nil {
		t.Fatalf("the answer file of %s was not written: %v", id, err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the answer file of %s is not JSON: %v\n%s", id, err, raw)
	}
	answered, _ := got["answered"].(string)
	if ts, err := time.Parse(time.RFC3339, answered); err != nil || ts.Before(before.Add(-2*time.Second)) {
		t.Errorf("answered = %q, want an RFC 3339 time of now", answered)
	}
	delete(got, "answered")
	return got
}

// noAnswerOrOutcome checks, once the server has had time to log, that nothing
// was answered: no answer file and no record of the given events.
func (w *world) noAnswerOrOutcome(session, dir string, ids []string, events ...string) {
	w.t.Helper()
	time.Sleep(700 * time.Millisecond)
	for _, id := range ids {
		if exists(filepath.Join(dir, id+".answer.json")) {
			w.t.Errorf("%s.answer.json was written", id)
		}
	}
	for _, e := range append(events, "answered") {
		if n := countEvent(w.shipLog(session), e); n != 0 {
			w.t.Errorf("the outcome log has %d %s records, want none", n, e)
		}
	}
}

// S1 and S2: Submit reads the review's current body from GitHub, sends it with
// the event as the engineer (the fleet's environment), writes the answer, logs
// review_submitted and the item leaves. A body GitHub holds empty falls back to
// the item's summary. A second Submit is refused and gh is not run again.
func TestInboxReviewSubmit(t *testing.T) {
	w, g := reviewWorld(t)
	ship, dir := w.ghShip("f", "rev1")
	seedItem(t, dir, "r-1", reviewAt("r-1", "2026-10-22T10:00:00Z", prURL, 123))
	seedItem(t, dir, "r-2", reviewAt("r-2", "2026-10-22T10:05:00Z", "https://github.com/acme/widgets/pull/12", 124))
	w.in.waitInbox(t, "r-1", "r-2")

	// The engineer edited the body on GitHub; its text starts like a flag.
	edited := "-X DELETE --input /etc/passwd looks good with a nit"
	g.rest("get1", "GET", ghReviews, 200, map[string]any{"id": 123, "state": "PENDING", "body": edited}, "")
	g.rest("post1", "POST", ghReviews+"/events", 200, map[string]any{"id": 123, "state": "APPROVED"}, "")
	g.rest("get2", "GET", "repos/acme/widgets/pulls/12/reviews/124", 200, map[string]any{"id": 124, "state": "PENDING", "body": ""}, "")
	g.rest("post2", "POST", "repos/acme/widgets/pulls/12/reviews/124/events", 200, map[string]any{"id": 124}, "")

	before := time.Now()
	if code, resp := w.in.act(t, ship.Session, "r-1", "submit", map[string]any{"event": "APPROVE", "edited": true}); !ok2xx(code) {
		t.Fatalf("submit = %d, want 2xx: %s", code, resp)
	}
	posts := g.callsTo("POST", ghReviews+"/events")
	if len(posts) != 1 || len(g.callsTo("GET", ghReviews)) != 1 {
		t.Fatalf("gh calls = %v, want one GET of the review and one POST of its events", g.restCalls())
	}
	post := posts[0]
	if v, _ := post.field("event"); v != "APPROVE" {
		t.Errorf("event sent = %q, want APPROVE (argv %q)", v, post.Argv)
	}
	if v, ok := post.field("body"); !ok || v != edited {
		t.Errorf("body sent = %q (present %v), want the body read back from GitHub %q", v, ok, edited)
	}
	for _, a := range post.Argv {
		if a == edited || a == "-F" || a == "--field" {
			t.Errorf("argv %q holds the item text as its own argument or uses -F", post.Argv)
		}
	}
	if post.Host != "github.com" {
		t.Errorf("--hostname = %q for a github.com PR, want github.com", post.Host)
	}
	if post.Env["GH_TOKEN"] != "fake-token-f" || post.Env["GH_PROMPT_DISABLED"] != "1" || post.Env["GH_NO_UPDATE_NOTIFIER"] != "1" {
		t.Errorf("gh env = %v, want fake-token-f as the fleet's token and both GH_ switches set to 1", post.Env)
	}

	got := readAnswer(t, dir, "r-1", before)
	if want := map[string]any{"version": float64(1), "id": "r-1", "kind": "review", "action": "submitted", "event": "APPROVE"}; !reflect.DeepEqual(got, want) {
		t.Errorf("answer file = %v, want %v", got, want)
	}
	w.in.waitInbox(t, "r-2")
	log := w.waitEvent(ship.Session, "review_submitted", 1)
	if e := lastOf(log, "review_submitted"); e["item"] != "r-1" || e["kind"] != "review" || e["review_event"] != "APPROVE" || e["edited"] != true {
		t.Errorf("review_submitted = %v, want item r-1, kind review, event APPROVE, edited true", e)
	}

	if code, _ := w.in.act(t, ship.Session, "r-1", "submit", map[string]any{"event": "APPROVE"}); code != http.StatusConflict && code != http.StatusNotFound {
		t.Errorf("submitting twice = %d, want 409 (or 404)", code)
	}
	if n := len(g.callsTo("POST", ghReviews+"/events")); n != 1 {
		t.Errorf("gh posted the events %d times, want 1", n)
	}

	// S2: REQUEST_CHANGES with an empty body on GitHub sends the item's summary.
	if code, resp := w.in.act(t, ship.Session, "r-2", "submit", map[string]any{"event": "REQUEST_CHANGES", "edited": false}); !ok2xx(code) {
		t.Fatalf("submit REQUEST_CHANGES = %d, want 2xx: %s", code, resp)
	}
	p2 := g.callsTo("POST", "repos/acme/widgets/pulls/12/reviews/124/events")
	if len(p2) != 1 {
		t.Fatalf("gh calls = %v, want one POST for review 124", g.restCalls())
	}
	if v, _ := p2[0].field("event"); v != "REQUEST_CHANGES" {
		t.Errorf("event sent = %q, want REQUEST_CHANGES", v)
	}
	if v, _ := p2[0].field("body"); v != "summary of r-2" {
		t.Errorf("body sent = %q, want the item's summary when GitHub's body is empty", v)
	}
	log = w.waitEvent(ship.Session, "review_submitted", 2)
	if e := lastOf(log, "review_submitted"); e["item"] != "r-2" || e["review_event"] != "REQUEST_CHANGES" || e["edited"] != false {
		t.Errorf("review_submitted = %v, want item r-2, event REQUEST_CHANGES, edited false", e)
	}
	if n := countEvent(log, "answered"); n != 0 {
		t.Errorf("%d `answered` records for review items, want none (the review_* events are the record)", n)
	}
	w.in.waitInbox(t)
}

// S3: Discard deletes the pending review through gh, writes the answer without
// an event and logs review_discarded.
func TestInboxReviewDiscard(t *testing.T) {
	w, g := reviewWorld(t)
	ship, dir := w.ghShip("f", "rev3")
	seedItem(t, dir, "r-1", reviewAt("r-1", "2026-10-22T10:00:00Z", prURL, 123))
	w.in.waitInbox(t, "r-1")
	g.rest("del", "DELETE", ghReviews, 200, map[string]any{"id": 123, "state": "PENDING"}, "")

	before := time.Now()
	if code, resp := w.in.act(t, ship.Session, "r-1", "discard", nil); !ok2xx(code) {
		t.Fatalf("discard = %d, want 2xx: %s", code, resp)
	}
	if calls := g.callsTo("DELETE", ghReviews); len(calls) != 1 || calls[0].Env["GH_TOKEN"] != "fake-token-f" {
		t.Errorf("gh calls = %v, want one DELETE of the review as the fleet's engineer", g.restCalls())
	}
	if got, want := readAnswer(t, dir, "r-1", before), map[string]any{"version": float64(1), "id": "r-1", "kind": "review", "action": "discarded"}; !reflect.DeepEqual(got, want) {
		t.Errorf("answer file = %v, want %v (no event)", got, want)
	}
	log := w.waitEvent(ship.Session, "review_discarded", 1)
	if e := lastOf(log, "review_discarded"); e["item"] != "r-1" || e["kind"] != "review" || (e["external"] != nil && e["external"] != false) {
		t.Errorf("review_discarded = %v, want item r-1, kind review and no external flag", e)
	}
	w.in.waitInbox(t)
}

// S4: when gh fails, the engineer gets an error with gh's message, no answer is
// written, nothing is recorded, the item stays, and gh is not retried. 422 "Can
// not approve your own pull request" is such a failure, and a huge stderr is cut.
func TestInboxReviewGhFailureKeepsTheItem(t *testing.T) {
	w, g := reviewWorld(t)
	ship, dir := w.ghShip("f", "rev4")
	seedItem(t, dir, "r-1", reviewAt("r-1", "2026-10-22T10:00:00Z", prURL, 123))
	seedItem(t, dir, "r-2", reviewAt("r-2", "2026-10-22T10:05:00Z", "https://github.com/acme/widgets/pull/12", 124))
	w.in.waitInbox(t, "r-1", "r-2")

	own := `{"message":"Unprocessable Entity","errors":["Can not approve your own pull request"]}`
	g.rest("get1", "GET", ghReviews, 200, map[string]any{"id": 123, "state": "PENDING", "body": "ok"}, "")
	g.rest("post1", "POST", ghReviews+"/events", 422, json.RawMessage(own), "gh: Unprocessable Entity (HTTP 422)\nCan not approve your own pull request\n")
	code, resp := w.in.act(t, ship.Session, "r-1", "submit", map[string]any{"event": "APPROVE"})
	if ok2xx(code) {
		t.Fatalf("submit with gh failing = %d, want an error status: %s", code, resp)
	}
	if !strings.Contains(resp, "Can not approve your own pull request") {
		t.Errorf("the error %q does not carry gh's message", resp)
	}
	if n := len(g.callsTo("POST", ghReviews+"/events")); n != 1 {
		t.Errorf("gh posted %d times, want 1 (no retries)", n)
	}

	// Discard failing the same way, and a huge stderr.
	huge := "gh: Server Error (HTTP 500)\n" + strings.Repeat("x", 300_000)
	g.rest("del2", "DELETE", "repos/acme/widgets/pulls/12/reviews/124", 500, map[string]any{"message": "Server Error"}, huge)
	code, resp = w.in.act(t, ship.Session, "r-2", "discard", nil)
	if ok2xx(code) {
		t.Fatalf("discard with gh failing = %d, want an error status: %s", code, resp)
	}
	if len(resp) > 100_000 {
		t.Errorf("the error is %d bytes, want gh's stderr cut", len(resp))
	}

	w.noAnswerOrOutcome(ship.Session, dir, []string{"r-1", "r-2"}, "review_submitted", "review_discarded")
	w.in.waitInbox(t, "r-1", "r-2")
}

// S4: with no gh on PATH there is an error that names gh, and the same
// no-answer, no-outcome, item-stays result.
func TestInboxReviewMissingGh(t *testing.T) {
	for _, d := range []string{"/usr/bin", "/bin"} {
		if exists(filepath.Join(d, "gh")) {
			t.Skipf("a gh at %s/gh would stay on the trimmed PATH", d)
		}
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t)
	bin := filepath.Join(w.root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tmux, filepath.Join(bin, "tmux")); err != nil {
		t.Fatal(err)
	}
	w.extra = append(w.extra, "PATH="+bin+":/usr/bin:/bin")
	w.ghFleets(map[string][]string{"f": {"hook working"}})
	w.start()
	ship, dir := w.ghShip("f", "rev4b")
	seedItem(t, dir, "r-1", reviewAt("r-1", "2026-10-22T10:00:00Z", prURL, 123))
	w.in.waitInbox(t, "r-1")

	code, resp := w.in.act(t, ship.Session, "r-1", "discard", nil)
	if ok2xx(code) || !strings.Contains(strings.ToLower(resp), "gh") {
		t.Errorf("discard with no gh = %d %q, want an error that names gh", code, resp)
	}
	w.noAnswerOrOutcome(ship.Session, dir, []string{"r-1"}, "review_discarded")
	w.in.waitInbox(t, "r-1")
}

// S5: a pr_url or review_id that does not have the form
// https://<host>/<owner>/<repo>/pull/<n> with an integer review id is refused
// without running gh. Submit refuses it. Discard has nothing on GitHub to
// delete, so it answers the item as discarded (invalid_target), which is the
// item's way out of the inbox.
func TestInboxReviewRefusesBadTargets(t *testing.T) {
	w, g := reviewWorld(t)
	ship, dir := w.ghShip("f", "rev5")
	g.rest("any", "GET", ghReviews, 200, map[string]any{"body": "x"}, "")
	cases := []struct {
		name     string
		url      string
		reviewID any
	}{
		{"http", "http://github.com/acme/widgets/pull/12", 123},
		{"file", "file:///acme/widgets/pull/12", 123},
		{"ssh", "ssh://github.com/acme/widgets/pull/12", 123},
		{"an extra path", "https://github.com/acme/widgets/pull/12/files", 123},
		{"a query", "https://github.com/acme/widgets/pull/12?x=1", 123},
		{"a fragment", "https://github.com/acme/widgets/pull/12#review", 123},
		{"user info", "https://evil@github.com/acme/widgets/pull/12", 123},
		{"an issue", "https://github.com/acme/widgets/issues/12", 123},
		{"a dotted owner", "https://github.com/ac.me/widgets/pull/12", 123},
		{"an underscore owner", "https://github.com/ac_me/widgets/pull/12", 123},
		{"repo dot dot", "https://github.com/acme/../pull/12", 123},
		{"repo dot", "https://github.com/acme/./pull/12", 123},
		{"a path in the repo", "https://github.com/acme/wid%2Fgets/pull/12", 123},
		{"a PR number with letters", "https://github.com/acme/widgets/pull/12abc", 123},
		{"a negative PR number", "https://github.com/acme/widgets/pull/-1", 123},
		{"no scheme", "github.com/acme/widgets/pull/12", 123},
		{"not a URL", "--help", 123},
		{"a string review id", prURL, "123abc"},
		{"a flag review id", prURL, "-X"},
		{"a fractional review id", prURL, 1.5},
		{"a negative review id", prURL, -3},
		{"no review id", prURL, nil},
	}
	var all []string
	for i, c := range cases {
		id := "b-" + fmt.Sprintf("%02d", i)
		all = append(all, id)
		seedItem(t, dir, id, reviewAt(id, "2026-10-22T10:00:00Z", c.url, c.reviewID))
	}
	w.in.waitInbox(t, all...)
	for i, c := range cases {
		id := "b-" + fmt.Sprintf("%02d", i)
		if code, resp := w.in.act(t, ship.Session, id, "submit", map[string]any{"event": "APPROVE"}); !refused(code) {
			t.Errorf("submit with %s = %d, want 400 or 422: %s", c.name, code, resp)
		}
	}
	// An event Admiral does not know is refused too, on a good target.
	seedItem(t, dir, "ok-1", reviewAt("ok-1", "2026-10-22T10:00:00Z", prURL, 123))
	w.in.waitInbox(t, append(all, "ok-1")...)
	for _, ev := range []string{"PENDING", "approve", "", "DISMISS"} {
		if code, resp := w.in.act(t, ship.Session, "ok-1", "submit", map[string]any{"event": ev}); !refused(code) {
			t.Errorf("submit with event %q = %d, want 400 or 422: %s", ev, code, resp)
		}
	}
	w.noAnswerOrOutcome(ship.Session, dir, append(all, "ok-1"), "review_submitted", "review_discarded")

	// Discard answers each of them as discarded, without gh.
	before := time.Now()
	for i, c := range cases {
		id := "b-" + fmt.Sprintf("%02d", i)
		code, resp := w.in.act(t, ship.Session, id, "discard", nil)
		if !ok2xx(code) {
			t.Errorf("discard with %s = %d, want 2xx: %s", c.name, code, resp)
			continue
		}
		if !strings.Contains(resp, `"invalid_target":true`) {
			t.Errorf("discard with %s answered %s, want invalid_target true: nothing changed on GitHub", c.name, resp)
		}
		if got, want := readAnswer(t, dir, id, before), map[string]any{"version": float64(1), "id": id, "kind": "review", "action": "discarded"}; !reflect.DeepEqual(got, want) {
			t.Errorf("answer file of %s = %v, want %v", id, got, want)
		}
	}
	log := w.waitEvent(ship.Session, "review_discarded", len(cases))
	for _, r := range log {
		if r["event"] == "review_discarded" && (r["invalid_target"] != true || r["external"] != nil) {
			t.Errorf("review_discarded = %v, want invalid_target true and no external flag", r)
		}
	}
	if calls := g.restCalls(); len(calls) != 0 {
		t.Errorf("gh was run for a refused request: %v", calls)
	}
	w.in.waitInbox(t, "ok-1")
}

// The host comes from the pr_url and the environment from the ship's fleet:
// a GitHub Enterprise PR gets --hostname, and a ship of fleet g runs gh with
// fleet g's token. A session with no ship runs gh in the server's own
// environment.
func TestInboxReviewHostAndFleetEnvironment(t *testing.T) {
	w, g := reviewWorld(t)
	ship, dir := w.ghShip("g", "rev6")
	ghe := "https://ghe.example.com/acme/widgets/pull/7"
	seedItem(t, dir, "r-1", reviewAt("r-1", "2026-10-22T10:00:00Z", ghe, 77))
	ghost := filepath.Join(w.home, "inbox", "ghost-ship")
	seedItem(t, ghost, "r-9", reviewAt("r-9", "2026-10-22T10:05:00Z", prURL, 123))
	w.in.waitInbox(t, "r-1", "r-9")

	g.rest("get1", "GET", "repos/acme/widgets/pulls/7/reviews/77", 200, map[string]any{"body": "hi"}, "")
	g.rest("post1", "POST", "repos/acme/widgets/pulls/7/reviews/77/events", 200, map[string]any{}, "")
	if code, resp := w.in.act(t, ship.Session, "r-1", "submit", map[string]any{"event": "COMMENT"}); !ok2xx(code) {
		t.Fatalf("submit on GHE = %d: %s", code, resp)
	}
	for _, m := range []string{"GET", "POST"} {
		ep := "repos/acme/widgets/pulls/7/reviews/77"
		if m == "POST" {
			ep += "/events"
		}
		calls := g.callsTo(m, ep)
		if len(calls) != 1 || calls[0].Host != "ghe.example.com" || calls[0].Env["GH_TOKEN"] != "fake-token-g" {
			t.Errorf("%s %s = %v, want --hostname ghe.example.com and fleet g's GH_TOKEN fake-token-g", m, ep, calls)
		}
	}

	g.rest("get2", "GET", ghReviews, 200, map[string]any{"body": "hi"}, "")
	g.rest("post2", "POST", ghReviews+"/events", 200, map[string]any{}, "")
	if code, resp := w.in.act(t, "ghost-ship", "r-9", "submit", map[string]any{"event": "COMMENT"}); !ok2xx(code) {
		t.Fatalf("submit in a session with no ship = %d: %s", code, resp)
	}
	if calls := g.callsTo("POST", ghReviews+"/events"); len(calls) != 1 {
		t.Fatalf("gh calls = %v, want one POST for the ghost ship's review", g.restCalls())
	} else if tok, set := calls[0].Env["GH_TOKEN"]; set {
		t.Errorf("the ghost ship's gh ran with GH_TOKEN %q, want the server's own environment", tok)
	}
}

// S8: GitHub saying the review is gone (404) or no longer pending (422) is not
// an error. The answer is {action: gone}, review_discarded is recorded with
// external: true, and the item leaves. Any other failure stays an error.
func TestInboxReviewGone(t *testing.T) {
	w, g := reviewWorld(t)
	ship, dir := w.ghShip("f", "rev8")
	for i, id := range []string{"gone-get", "gone-del", "gone-422", "own-422", "gone-state", "own-del"} {
		seedItem(t, dir, id, reviewAt(id, "2026-10-22T10:0"+itoa(i)+":00Z", "https://github.com/acme/widgets/pull/12", 200+i))
	}
	// A 404 from a token that cannot see the repository reads the same as a deleted
	// review. Admiral looks for the pull request before it calls the review gone.
	seedItem(t, dir, "noaccess-get", reviewAt("noaccess-get", "2026-10-22T10:06:00Z", "https://github.com/acme/widgets/pull/13", 300))
	seedItem(t, dir, "noaccess-del", reviewAt("noaccess-del", "2026-10-22T10:07:00Z", "https://github.com/acme/widgets/pull/14", 301))
	w.in.waitInbox(t, "gone-get", "gone-del", "gone-422", "own-422", "gone-state", "own-del", "noaccess-get", "noaccess-del")
	ep := func(n int) string { return "repos/acme/widgets/pulls/12/reviews/" + itoa(n) }
	notFound := json.RawMessage(`{"message":"Not Found"}`)
	// GitHub's error bodies carry a documentation_url, and the one for deleting a
	// review has "pending" in its anchor; that is not the review being gone.
	doc := `"documentation_url":"https://docs.github.com/rest/pulls/reviews#delete-a-pending-review-for-a-pull-request"`

	g.rest("g0", "GET", ep(200), 404, notFound, "gh: Not Found (HTTP 404)\n")
	g.rest("d1", "DELETE", ep(201), 404, notFound, "gh: Not Found (HTTP 404)\n")
	g.rest("g2", "GET", ep(202), 200, map[string]any{"state": "PENDING", "body": "b"}, "")
	g.rest("p2", "POST", ep(202)+"/events", 422, json.RawMessage(`{"message":"Unprocessable Entity","errors":["Can not submit a review that is not pending"],`+doc+`}`), "gh: Unprocessable Entity (HTTP 422)\nCan not submit a review that is not pending\n")
	g.rest("g3", "GET", ep(203), 200, map[string]any{"state": "PENDING", "body": "b"}, "")
	g.rest("p3", "POST", ep(203)+"/events", 422, json.RawMessage(`{"message":"Unprocessable Entity","errors":["Can not approve your own pull request"],`+doc+`}`), "gh: Unprocessable Entity (HTTP 422)\nCan not approve your own pull request\n")
	g.rest("pr12", "GET", "repos/acme/widgets/pulls/12", 200, map[string]any{"number": 12}, "")
	g.rest("n1", "GET", "repos/acme/widgets/pulls/13/reviews/300", 404, notFound, "gh: Not Found (HTTP 404)\n")
	g.rest("pr13", "GET", "repos/acme/widgets/pulls/13", 404, notFound, "gh: Not Found (HTTP 404)\n")
	g.rest("n2", "DELETE", "repos/acme/widgets/pulls/14/reviews/301", 404, notFound, "gh: Not Found (HTTP 404)\n")
	g.rest("pr14", "GET", "repos/acme/widgets/pulls/14", 500, map[string]any{"message": "Server Error"}, "gh: Server Error (HTTP 500)\n")
	// The review was submitted on GitHub already: its state says so, and no POST follows.
	g.rest("g4", "GET", ep(204), 200, map[string]any{"state": "APPROVED", "body": "b"}, "")
	// A Discard 422 that is not about the review being pending stays an error.
	g.rest("d5", "DELETE", ep(205), 422, json.RawMessage(`{"message":"Validation Failed","errors":["Something else"],`+doc+`}`), "gh: Validation Failed (HTTP 422)\n")

	before := time.Now()
	for _, c := range []struct{ id, action string }{{"gone-get", "submit"}, {"gone-del", "discard"}, {"gone-422", "submit"}, {"gone-state", "submit"}} {
		if code, resp := w.in.act(t, ship.Session, c.id, c.action, map[string]any{"event": "APPROVE"}); !ok2xx(code) {
			t.Fatalf("%s of %s with the review gone = %d, want 2xx: %s", c.action, c.id, code, resp)
		}
		got := readAnswer(t, dir, c.id, before)
		if want := map[string]any{"version": float64(1), "id": c.id, "kind": "review", "action": "gone"}; !reflect.DeepEqual(got, want) {
			t.Errorf("answer file of %s = %v, want %v", c.id, got, want)
		}
	}
	if code, _ := w.in.act(t, ship.Session, "own-422", "submit", map[string]any{"event": "APPROVE"}); ok2xx(code) {
		t.Errorf("a 422 that is not 'not pending' = %d, want an error", code)
	}
	if code, _ := w.in.act(t, ship.Session, "own-del", "discard", nil); ok2xx(code) {
		t.Errorf("a Discard 422 that is not about pending = %d, want an error", code)
	}
	for _, id := range []string{"noaccess-get", "noaccess-del"} {
		act := map[string]string{"noaccess-get": "submit", "noaccess-del": "discard"}[id]
		code, resp := w.in.act(t, ship.Session, id, act, map[string]any{"event": "APPROVE"})
		if ok2xx(code) || !strings.Contains(resp, "Not Found") && !strings.Contains(resp, "Server Error") {
			t.Errorf("%s of %s with the pull request out of reach = %d %q, want an error with gh's message", act, id, code, resp)
		}
	}
	if n := len(g.callsTo("GET", "repos/acme/widgets/pulls/12")); n != 2 {
		t.Errorf("the pull request was looked up %d times for the two 404s on a visible one, want 2", n)
	}
	if n := len(g.callsTo("POST", ep(204)+"/events")); n != 0 {
		t.Errorf("gh posted %d times for a review that is not pending, want none", n)
	}
	log := w.waitEvent(ship.Session, "review_discarded", 4)
	items := map[any]bool{}
	for _, r := range log {
		if r["event"] == "review_discarded" {
			items[r["item"]] = true
			if r["external"] != true || r["kind"] != "review" {
				t.Errorf("review_discarded = %v, want kind review and external true", r)
			}
		}
	}
	if !reflect.DeepEqual(items, map[any]bool{"gone-get": true, "gone-del": true, "gone-422": true, "gone-state": true}) {
		t.Errorf("review_discarded for %v, want the four gone items", items)
	}
	if n := countEvent(log, "review_submitted"); n != 0 {
		t.Errorf("%d review_submitted records, want none", n)
	}
	w.in.waitInbox(t, "own-422", "own-del", "noaccess-get", "noaccess-del")
	for _, id := range []string{"own-422", "own-del", "noaccess-get", "noaccess-del"} {
		if exists(filepath.Join(dir, id+".answer.json")) {
			t.Errorf("%s was answered", id)
		}
	}
}

// S6: Send writes the findings answer (the proposal only on the edited card),
// logs one outcome per card, posts nothing to GitHub and the item leaves. A
// request with a card missing, unknown or twice, a decision that is not one of
// the three, a proposal on a card that is not edited, a bad proposal kind or an
// edited card with none is refused and changes nothing.
func TestInboxFindingsSend(t *testing.T) {
	w, g := reviewWorld(t)
	ship, dir := w.ghShip("f", "fin6")
	seedItem(t, dir, "f-1", findingsCards("r1", "r2", "r3"))
	seedItem(t, dir, "q-1", questionItem("q-1", "2026-10-22T10:05:00Z"))
	w.in.waitInbox(t, "f-1", "q-1")

	card := func(id, decision string, proposal any) map[string]any {
		c := map[string]any{"id": id, "decision": decision}
		if proposal != nil {
			c["proposal"] = proposal
		}
		return c
	}
	good := map[string]any{"kind": "reply", "content": "Why this name?"}
	bad := map[string][]map[string]any{
		"a card missing":             {card("r1", "approved", nil), card("r2", "rejected", nil)},
		"an unknown card":            {card("r1", "approved", nil), card("r2", "rejected", nil), card("r3", "approved", nil), card("r9", "approved", nil)},
		"a card twice":               {card("r1", "approved", nil), card("r1", "rejected", nil), card("r2", "rejected", nil), card("r3", "approved", nil)},
		"a decision that is no":      {card("r1", "maybe", nil), card("r2", "rejected", nil), card("r3", "approved", nil)},
		"a proposal on an approval":  {card("r1", "approved", good), card("r2", "rejected", nil), card("r3", "approved", nil)},
		"a proposal on a rejection":  {card("r1", "rejected", good), card("r2", "rejected", nil), card("r3", "approved", nil)},
		"an edit with no proposal":   {card("r1", "edited", nil), card("r2", "rejected", nil), card("r3", "approved", nil)},
		"an edit of the wrong kind":  {card("r1", "edited", map[string]any{"kind": "patch", "content": "x"}), card("r2", "rejected", nil), card("r3", "approved", nil)},
		"an edit with empty content": {card("r1", "edited", map[string]any{"kind": "diff", "content": ""}), card("r2", "rejected", nil), card("r3", "approved", nil)},
	}
	for name, cards := range bad {
		if code, resp := w.in.act(t, ship.Session, "f-1", "findings", map[string]any{"cards": cards}); !refused(code) {
			t.Errorf("%s: send = %d, want 400 or 422: %s", name, code, resp)
		}
	}
	if code, _ := w.in.act(t, ship.Session, "f-1", "findings", map[string]any{}); !refused(code) {
		t.Errorf("send with no cards = %d, want 400 or 422", code)
	}
	if code, _ := w.in.act(t, ship.Session, "q-1", "findings", map[string]any{"cards": []any{card("r1", "approved", nil)}}); !refused(code) {
		t.Errorf("send to a question item = %d, want 400 or 422", code)
	}
	if code, _ := w.in.act(t, ship.Session, "nope", "findings", map[string]any{"cards": []any{}}); code != http.StatusNotFound {
		t.Errorf("send to an item that is not there = %d, want 404", code)
	}
	w.noAnswerOrOutcome(ship.Session, dir, []string{"f-1", "q-1"}, "card_approved", "card_edited", "card_rejected")

	edited := map[string]any{"kind": "diff", "content": "- a\n+ c"}
	before := time.Now()
	req := map[string]any{"cards": []any{card("r1", "approved", nil), card("r2", "edited", edited), card("r3", "rejected", nil)}}
	if code, resp := w.in.act(t, ship.Session, "f-1", "findings", req); !ok2xx(code) {
		t.Fatalf("send = %d, want 2xx: %s", code, resp)
	}
	want := map[string]any{"version": float64(1), "id": "f-1", "kind": "findings", "cards": map[string]any{
		"r1": map[string]any{"decision": "approved"},
		"r2": map[string]any{"decision": "edited", "proposal": map[string]any{"kind": "diff", "content": "- a\n+ c"}},
		"r3": map[string]any{"decision": "rejected"},
	}}
	if got := readAnswer(t, dir, "f-1", before); !reflect.DeepEqual(got, want) {
		t.Errorf("answer file = %v, want %v", got, want)
	}
	log := w.waitShipLog(ship.Session, 15*time.Second, "three card outcomes", func(l []outRec) bool {
		return countEvent(l, "card_approved")+countEvent(l, "card_edited")+countEvent(l, "card_rejected") >= 3
	})
	for event, card := range map[string]string{"card_approved": "r1", "card_edited": "r2", "card_rejected": "r3"} {
		if n := countEvent(log, event); n != 1 {
			t.Errorf("%d %s records, want 1", n, event)
			continue
		}
		if e := lastOf(log, event); e["item"] != "f-1" || e["kind"] != "findings" || e["card"] != card {
			t.Errorf("%s = %v, want item f-1, kind findings, card %s", event, e, card)
		}
	}
	if n := countEvent(log, "answered"); n != 0 {
		t.Errorf("%d `answered` records for a findings item, want none", n)
	}
	w.in.waitInbox(t, "q-1")
	if calls := g.restCalls(); len(calls) != 0 {
		t.Errorf("gh was run for findings: %v, want nothing posted to GitHub", calls)
	}
	if code, _ := w.in.act(t, ship.Session, "f-1", "findings", req); code != http.StatusConflict && code != http.StatusNotFound {
		t.Errorf("sending twice = %d, want 409 (or 404)", code)
	}

	// An item with no cards takes an empty list, so it can leave the inbox.
	empty := findingsCards()
	empty["id"] = "f-0"
	seedItem(t, dir, "f-0", empty)
	w.in.waitInbox(t, "f-0", "q-1")
	if code, resp := w.in.act(t, ship.Session, "f-0", "findings", map[string]any{"cards": []any{}}); !ok2xx(code) {
		t.Fatalf("sending an empty list to an item with no cards = %d: %s", code, resp)
	}
	if got, want := readAnswer(t, dir, "f-0", before), map[string]any{"version": float64(1), "id": "f-0", "kind": "findings", "cards": map[string]any{}}; !reflect.DeepEqual(got, want) {
		t.Errorf("answer file = %v, want %v", got, want)
	}
	w.in.waitInbox(t, "q-1")
}

// S7: a ship that exits 0 with an unanswered item keeps its inbox and report.
// After sail-off its items are still served with the ship's name and harness,
// and still after a restart; gh runs in the environment of the fleet the ship
// sailed from. The files go, and the item with them, when the last item is
// answered.
func TestInboxKeepsUnansweredFilesAfterSailOff(t *testing.T) {
	w := newWorld(t)
	g := w.newFakeGH()
	w.ghFleets(map[string][]string{"f": {"hook working", "wait go", "exit 0"}})
	w.start()
	ship := w.launchIn("f", "claude", "keep7")
	rec := w.record(ship.Session)
	w.waitEvent(ship.Session, "launched", 1)
	reportPath, inboxPath := w.seedShipFiles(rec, `{"status":"done"}`)
	seedItem(t, inboxPath, "q-1", questionItem("q-1", "2026-10-22T10:00:00Z"))
	seedItem(t, inboxPath, "r-1", reviewAt("r-1", "2026-10-22T10:05:00Z", prURL, 123))
	w.in.waitInbox(t, "q-1", "r-1")
	g.rest("get", "GET", ghReviews, 200, map[string]any{"body": "fine"}, "")
	g.rest("post", "POST", ghReviews+"/events", 200, map[string]any{}, "")

	w.touch(ship.Session, "go")
	w.waitEvent(ship.Session, "ended", 1)
	deadline := time.Now().Add(15 * time.Second)
	for w.hasSession(ship.Session) {
		if time.Now().After(deadline) {
			t.Fatalf("the exit-0 ship's session never went")
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(2 * time.Second) // the cleanup, if it were to run, has had its pass

	served := func(when string) {
		t.Helper()
		items := w.in.waitInbox(t, "q-1", "r-1")
		for _, it := range items {
			if it.Session != ship.Session || it.Name != ship.Name || it.Harness != "claude" {
				t.Errorf("%s: item %+v, want ship %s, name %q and harness claude served from the kept record", when, it, ship.Session, ship.Name)
			}
		}
		if !exists(inboxPath) || !exists(reportPath) {
			t.Errorf("%s: the ship's report or inbox was removed with an item unanswered", when)
		}
	}
	served("after sail-off")
	w.restart()
	served("after a restart")
	time.Sleep(2 * time.Second)
	served("after a restart and a pass")

	body := map[string]any{"answers": map[string]any{"1": map[string]any{"choice": "Alpha"}, "2": map[string]any{"choice": []string{"X"}}}}
	if code, resp := w.in.act(t, ship.Session, "q-1", "answer", body); !ok2xx(code) {
		t.Fatalf("answering the question = %d: %s", code, resp)
	}
	time.Sleep(1500 * time.Millisecond)
	if !exists(inboxPath) || !exists(reportPath) {
		t.Fatalf("the files were removed with the review still unanswered")
	}
	if code, resp := w.in.act(t, ship.Session, "r-1", "submit", map[string]any{"event": "APPROVE"}); !ok2xx(code) {
		t.Fatalf("submitting after sail-off and a restart = %d: %s", code, resp)
	}
	if calls := g.callsTo("POST", ghReviews+"/events"); len(calls) != 1 || calls[0].Env["GH_TOKEN"] != "fake-token-f" {
		t.Errorf("gh calls = %v, want one POST with the sailed-off ship's fleet token fake-token-f", g.restCalls())
	}
	waitGone(t, 15*time.Second, inboxPath, reportPath)
	w.in.waitInbox(t)
	// The record goes with the files, or the name is never drawn again.
	deadline = time.Now().Add(15 * time.Second)
	for kept(t, w.home, ship.Session) {
		if time.Now().After(deadline) {
			t.Fatalf("state.json still holds a kept_inboxes entry for %s", ship.Session)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// kept is whether state.json holds a kept inbox record for the session.
func kept(t *testing.T, home, session string) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "state.json"))
	if err != nil {
		return false
	}
	var st struct {
		Kept map[string]json.RawMessage `json:"kept_inboxes"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("state.json is not JSON: %v", err)
	}
	_, ok := st.Kept[session]
	return ok
}

// refused is a request the server turns down as invalid: not a missing route
// (404), not a failure of gh.
func refused(code int) bool {
	return code == http.StatusBadRequest || code == http.StatusUnprocessableEntity
}
