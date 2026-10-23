package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The Needs You inbox, server side (#116 u1): the real binary reads the item
// files ships write under $ADMIRAL_HOME/inbox/<session>/ (#112), serves them on
// GET /api/inbox and the WebSocket `inbox` message, and writes the engineer's
// answer to a question next to the item as <id>.answer.json (ADR 0016).
//
// Wire shape under test: {"items":[<the item file's fields> + "session", and
// "name" and "harness" when the ship is known]}; the answer is POSTed to
// /api/inbox/<session>/<id>/answer as {"answers":{...},"note":"..."}.

type inboxItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Created  string `json:"created"`
	Title    string `json:"title"`
	Session  string `json:"session"`
	Name     string `json:"name"`
	Harness  string `json:"harness"`
	OrderURL string `json:"order_url"`
}

func questionItem(id, created string) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "question", "created": created,
		"title": "Which way?", "order_url": "https://example.com/issues/9",
		"question": map[string]any{"questions": []any{
			map[string]any{"id": "1", "text": "Pick one", "recommended": "Alpha", "multi": false,
				"options": []any{map[string]any{"label": "Alpha", "description": "first"}, map[string]any{"label": "Beta", "description": "second"}}},
			map[string]any{"id": "2", "text": "Pick some", "multi": true,
				"options": []any{map[string]any{"label": "X", "description": "x"}, map[string]any{"label": "Y", "description": "y"}}},
		}}}
}

func reviewItem(id, created string) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "review", "created": created, "title": "Review PR 12",
		"review": map[string]any{"pr_url": "https://example.com/pull/12", "review_id": 123, "summary": "looks fine"}}
}

func findingsItem(id, created string) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "findings", "created": created, "title": "Feedback on PR 12",
		"findings": map[string]any{"pr_url": "https://example.com/pull/12", "cards": []any{
			map[string]any{"id": "r1", "source": "reviewer", "quote": "rename this", "verdict": "agree", "reason": "clearer",
				"proposal": map[string]any{"kind": "diff", "content": "- a\n+ b"}, "evidence": []any{"main.go:3"}}}}}
}

// seedItem writes the item the way Commodore does: whole, to a temp file, then
// a rename.
func seedItem(t *testing.T, dir, id string, item any) []byte {
	t.Helper()
	b, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "."+id+".json.tmp.1")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, id+".json")); err != nil {
		t.Fatal(err)
	}
	return b
}

func (in *fleetInst) inbox(t *testing.T) []inboxItem {
	t.Helper()
	code, body := send(t, http.MethodGet, in.LocalURL+"/api/inbox", nil)
	if code != 200 {
		t.Fatalf("GET /api/inbox = %d, want 200: %s", code, body)
	}
	var wrap struct {
		Items []inboxItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		t.Fatalf("GET /api/inbox is not JSON: %v\n%s", err, body)
	}
	return wrap.Items
}

func ids(items []inboxItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

// waitInbox polls GET /api/inbox until the item ids are want, within the
// "about a second" the brief allows (a margin on top for a loaded Mac).
func (in *fleetInst) waitInbox(t *testing.T, want ...string) []inboxItem {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		items := in.inbox(t)
		if reflect.DeepEqual(ids(items), want) || (len(want) == 0 && len(items) == 0) {
			return items
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 4s GET /api/inbox never listed %v; last: %v", want, ids(items))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (in *fleetInst) answer(t *testing.T, session, id string, body any) (int, string) {
	t.Helper()
	return send(t, http.MethodPost, in.LocalURL+"/api/inbox/"+session+"/"+id+"/answer", body)
}

// inboxShip launches a ship that just sits underway and returns it with its
// inbox directory.
func (w *world) inboxShip(repo string) (shipJSON, string) {
	w.t.Helper()
	w.cueFleets(map[string][]string{"f": {"hook working"}})
	w.start()
	ship := w.launchIn("f", "claude", repo)
	_, dir := shipPaths(w.record(ship.Session))
	return ship, dir
}

// S1: a question, a review and a findings item are served by the API and the
// WebSocket, oldest created first, with the ship's name and harness. An item of
// a session with no ship is served without them.
func TestInboxServesItemsOldestFirst(t *testing.T) {
	w := newWorld(t)
	ship, dir := w.inboxShip("inbox1")
	seedItem(t, dir, "f-1", findingsItem("f-1", "2026-10-22T10:30:00Z"))
	seedItem(t, dir, "q-1", questionItem("q-1", "2026-10-22T10:00:00Z"))
	seedItem(t, dir, "r-1", reviewItem("r-1", "2026-10-22T10:15:00Z"))
	seedItem(t, filepath.Join(w.home, "inbox", "ghost-ship"), "g-1", reviewItem("g-1", "2026-10-22T11:00:00Z"))

	items := w.in.waitInbox(t, "q-1", "r-1", "f-1", "g-1")
	for i, kind := range []string{"question", "review", "findings"} {
		it := items[i]
		if it.Kind != kind || it.Session != ship.Session || it.Name != ship.Name || it.Harness != "claude" {
			t.Errorf("item %d = %+v, want kind %s on ship %s (name %q, harness claude)", i, it, kind, ship.Session, ship.Name)
		}
	}
	if items[0].Title != "Which way?" || items[0].OrderURL != "https://example.com/issues/9" || items[0].Created != "2026-10-22T10:00:00Z" {
		t.Errorf("the question item = %+v, want its title, order_url and created carried through", items[0])
	}
	if g := items[3]; g.Session != "ghost-ship" || g.Name != "" || g.Harness != "" {
		t.Errorf("the item of a session with no ship = %+v, want session ghost-ship and no name or harness", g)
	}

	// A WebSocket client gets the same list as an `inbox` message.
	ws := dialWS(t, w.in.instance)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("the websocket never delivered an `inbox` message: %v", err)
		}
		var m struct {
			Type  string      `json:"type"`
			Items []inboxItem `json:"items"`
		}
		if json.Unmarshal(data, &m) != nil || m.Type != "inbox" {
			continue
		}
		if !reflect.DeepEqual(ids(m.Items), []string{"q-1", "r-1", "f-1", "g-1"}) {
			t.Errorf("websocket inbox = %v, want [q-1 r-1 f-1 g-1]", ids(m.Items))
		}
		break
	}

	// A new item reaches an open socket too.
	seedItem(t, dir, "q-2", questionItem("q-2", "2026-10-22T09:00:00Z"))
	waitInboxMsg(t, ws, 4*time.Second, "q-2 first", func(l []inboxItem) bool { return len(l) == 5 && l[0].ID == "q-2" })
}

func waitInboxMsg(t *testing.T, c *websocket.Conn, d time.Duration, what string, pred func([]inboxItem) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("within %s the websocket never delivered an `inbox` message with %s: %v", d, what, err)
		}
		var m struct {
			Type  string      `json:"type"`
			Items []inboxItem `json:"items"`
		}
		if json.Unmarshal(data, &m) == nil && m.Type == "inbox" && pred(m.Items) {
			return
		}
	}
}

// S2: files that are not valid items are skipped without hiding the others.
func TestInboxSkipsInvalidItems(t *testing.T) {
	w := newWorld(t)
	_, dir := w.inboxShip("inbox2")
	secret := filepath.Join(w.root, "outside.json")
	b, _ := json.Marshal(reviewItem("sneaky", "2026-10-22T10:00:00Z"))
	if err := os.WriteFile(secret, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "sneaky.json")); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("bad.json", `{"version":1,"id":"bad","kind":`)
	write("wrongver.json", string(mustJSON(t, map[string]any{"version": 2, "id": "wrongver", "kind": "review", "created": "2026-10-22T10:00:00Z", "title": "t", "review": map[string]any{}})))
	write("nokind.json", string(mustJSON(t, map[string]any{"version": 1, "id": "nokind", "kind": "poem", "created": "2026-10-22T10:00:00Z", "title": "t"})))
	write("mismatch.json", string(mustJSON(t, reviewItem("other-id", "2026-10-22T10:00:00Z"))))
	write("big.json", `{"version":1,"id":"big","kind":"review","created":"2026-10-22T10:00:00Z","title":"`+strings.Repeat("x", 1<<20)+`"}`)
	write(".hidden.json", string(mustJSON(t, reviewItem(".hidden", "2026-10-22T10:00:00Z"))))
	write("q-9.answer.json", `{"version":1,"id":"q-9","kind":"question"}`)
	write("note.txt", "hello")
	write(".ok.json.tmp.7", string(mustJSON(t, reviewItem("ok", "2026-10-22T10:00:00Z"))))
	seedItem(t, dir, "good", reviewItem("good", "2026-10-22T12:00:00Z"))

	w.in.waitInbox(t, "good")
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// S3: answering a question writes the contract's file, leaves the item alone,
// drops the item from the inbox and records `answered`. A second answer, and an
// answer to an item that isn't there, are errors.
func TestInboxAnswerQuestion(t *testing.T) {
	w := newWorld(t)
	ship, dir := w.inboxShip("inbox3")
	itemBytes := seedItem(t, dir, "q-1", questionItem("q-1", "2026-10-22T10:00:00Z"))
	seedItem(t, dir, "r-1", reviewItem("r-1", "2026-10-22T10:05:00Z"))
	w.in.waitInbox(t, "q-1", "r-1")

	body := map[string]any{
		"answers": map[string]any{
			"1": map[string]any{"choice": "Beta"},
			"2": map[string]any{"choice": []string{"X", "Y"}, "other": "and Z"},
		},
		"note": "go with beta",
	}
	before := time.Now().Add(-2 * time.Second)
	if code, resp := w.in.answer(t, ship.Session, "q-1", body); code/100 != 2 {
		t.Fatalf("answer = %d, want 2xx: %s", code, resp)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "q-1.answer.json"))
	if err != nil {
		t.Fatalf("the answer file was not written: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the answer file is not JSON: %v\n%s", err, raw)
	}
	answered, _ := got["answered"].(string)
	ts, perr := time.Parse(time.RFC3339, answered)
	if perr != nil || ts.Before(before) {
		t.Errorf("answered = %q, want an RFC 3339 time of now", answered)
	}
	delete(got, "answered")
	want := map[string]any{"version": float64(1), "id": "q-1", "kind": "question", "note": "go with beta",
		"answers": map[string]any{
			"1": map[string]any{"choice": "Beta"},
			"2": map[string]any{"choice": []any{"X", "Y"}, "other": "and Z"},
		}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("answer file = %v, want %v", got, want)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "q-1.json")); string(after) != string(itemBytes) {
		t.Errorf("the item file changed: now %q, was %q", after, itemBytes)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 3 { // q-1.json, q-1.answer.json, r-1.json
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the ship's inbox holds %v, want only the item, its answer and the other item (no temp file left)", names)
	}

	w.in.waitInbox(t, "r-1")
	log := w.waitEvent(ship.Session, "answered", 1)
	if a := lastOf(log, "answered"); a["item"] != "q-1" || a["kind"] != "question" {
		t.Errorf("answered = %v, want item q-1 and kind question", a)
	}

	if code, _ := w.in.answer(t, ship.Session, "q-1", body); code != http.StatusConflict && code != http.StatusNotFound {
		t.Errorf("answering twice = %d, want 409 (or 404 once the item is gone)", code)
	}
	if code, _ := w.in.answer(t, ship.Session, "nope", body); code != http.StatusNotFound {
		t.Errorf("answering an item that doesn't exist = %d, want 404", code)
	}
	if code, _ := w.in.answer(t, "no-such-ship", "q-1", body); code != http.StatusNotFound {
		t.Errorf("answering in a session with no inbox = %d, want 404", code)
	}
	if n := len(w.waitEvent(ship.Session, "answered", 1)); n < 1 || countEvent(w.shipLog(ship.Session), "answered") != 1 {
		t.Errorf("the outcome log has %d answered records, want exactly 1", countEvent(w.shipLog(ship.Session), "answered"))
	}
}

func countEvent(log []outRec, event string) int {
	n := 0
	for _, m := range log {
		if m["event"] == event {
			n++
		}
	}
	return n
}

// A question can be answered by `other` text alone.
func TestInboxAnswerByOtherTextAlone(t *testing.T) {
	w := newWorld(t)
	ship, dir := w.inboxShip("inbox3b")
	seedItem(t, dir, "q-1", questionItem("q-1", "2026-10-22T10:00:00Z"))
	w.in.waitInbox(t, "q-1")
	body := map[string]any{"answers": map[string]any{
		"1": map[string]any{"other": "something else"},
		"2": map[string]any{"choice": []string{"Y"}},
	}}
	if code, resp := w.in.answer(t, ship.Session, "q-1", body); code/100 != 2 {
		t.Fatalf("an answer by other text alone = %d, want 2xx: %s", code, resp)
	}
	if !exists(filepath.Join(dir, "q-1.answer.json")) {
		t.Errorf("no answer file for the other-text answer")
	}
}

// S4: an invalid answer is a 4xx and writes no file.
func TestInboxRejectsInvalidAnswers(t *testing.T) {
	w := newWorld(t)
	ship, dir := w.inboxShip("inbox4")
	seedItem(t, dir, "q-1", questionItem("q-1", "2026-10-22T10:00:00Z"))
	seedItem(t, dir, "r-1", reviewItem("r-1", "2026-10-22T10:05:00Z"))
	w.in.waitInbox(t, "q-1", "r-1")

	cases := map[string]map[string]any{
		"a question left out": {"answers": map[string]any{"1": map[string]any{"choice": "Alpha"}}},
		"an unknown label": {"answers": map[string]any{"1": map[string]any{"choice": "Gamma"},
			"2": map[string]any{"choice": []string{"X"}}}},
		"a list on a single-choice question": {"answers": map[string]any{"1": map[string]any{"choice": []string{"Alpha", "Beta"}},
			"2": map[string]any{"choice": []string{"X"}}}},
		"an unknown question id": {"answers": map[string]any{"1": map[string]any{"choice": "Alpha"},
			"2": map[string]any{"choice": []string{"X"}}, "9": map[string]any{"choice": "Alpha"}}},
		"an empty answer": {"answers": map[string]any{"1": map[string]any{}, "2": map[string]any{"choice": []string{"X"}}}},
		"no answers":      {},
	}
	for name, body := range cases {
		if code, resp := w.in.answer(t, ship.Session, "q-1", body); code/100 != 4 {
			t.Errorf("%s: answer = %d, want 4xx: %s", name, code, resp)
		}
	}
	// A review item takes no question answer (u2 handles its action).
	if code, _ := w.in.answer(t, ship.Session, "r-1", cases["a question left out"]); code/100 != 4 {
		t.Errorf("a question answer to a review item = %d, want 4xx", code)
	}
	if code, _ := send(t, http.MethodPost, w.in.LocalURL+"/api/inbox/"+ship.Session+"/q-1/answer", "not an object"); code/100 != 4 {
		t.Errorf("a body that is not an object = %d, want 4xx", code)
	}
	for _, f := range []string{"q-1.answer.json", "r-1.answer.json"} {
		if exists(filepath.Join(dir, f)) {
			t.Errorf("%s was written for an invalid answer", f)
		}
	}
	w.in.waitInbox(t, "q-1", "r-1")
	if n := countEvent(w.shipLog(ship.Session), "answered"); n != 0 {
		t.Errorf("the outcome log has %d answered records after only invalid answers", n)
	}
}

// S5: an answer file written by someone else hides the item, and so does the
// ship's directory going away.
func TestInboxHidesAnsweredAndRemovedItems(t *testing.T) {
	w := newWorld(t)
	_, dir := w.inboxShip("inbox5")
	seedItem(t, dir, "q-1", questionItem("q-1", "2026-10-22T10:00:00Z"))
	seedItem(t, dir, "r-1", reviewItem("r-1", "2026-10-22T10:05:00Z"))
	w.in.waitInbox(t, "q-1", "r-1")

	answered := `{"version":1,"id":"q-1","kind":"question","answered":"2026-10-22T10:10:00Z","answers":{}}`
	if err := os.WriteFile(filepath.Join(dir, "q-1.answer.json"), []byte(answered), 0o600); err != nil {
		t.Fatal(err)
	}
	w.in.waitInbox(t, "r-1")

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	w.in.waitInbox(t)

	// The directory can come back (a reused name) and its items show again.
	seedItem(t, dir, "q-3", questionItem("q-3", "2026-10-22T10:20:00Z"))
	w.in.waitInbox(t, "q-3")
}

// S6: a ship that needs you for a permission prompt shows the reason and when
// the stop began on /api/ships, and neither once it is underway again.
func TestShipWireCarriesReasonAndStopSince(t *testing.T) {
	w := newWorld(t)
	w.cueFleets(map[string][]string{"f": {"hook working", "sleep 1",
		`event PermissionRequest {"hook_event_name":"PermissionRequest","tool_name":"Bash"}`, "wait go",
		`event PostToolUse {"hook_event_name":"PostToolUse","tool_name":"Bash"}`}})
	w.start()
	ship := w.launchIn("f", "claude", "inbox6")

	type reasoned struct {
		shipJSON
		Reason    *string `json:"reason"`
		StopSince *string `json:"stop_since"`
	}
	read := func() reasoned {
		code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/ships", nil)
		if code != 200 {
			t.Fatalf("GET /api/ships = %d: %s", code, body)
		}
		var wrap struct {
			Ships []reasoned `json:"ships"`
		}
		if err := json.Unmarshal([]byte(body), &wrap); err != nil {
			t.Fatal(err)
		}
		for _, s := range wrap.Ships {
			if s.Session == ship.Session {
				return s
			}
		}
		t.Fatalf("ship %s is not listed: %s", ship.Session, body)
		return reasoned{}
	}
	if s := read(); s.Reason != nil && *s.Reason != "" || s.StopSince != nil && *s.StopSince != "" {
		t.Errorf("an underway ship shows reason %v and stop_since %v, want neither", s.Reason, s.StopSince)
	}
	start := time.Now().Add(-time.Second)
	w.in.waitStatusList(t, 10*time.Second, ship.Session, "needs_you")
	s := read()
	if s.Reason == nil || *s.Reason != "permission:Bash" {
		t.Errorf("reason = %v, want permission:Bash", s.Reason)
	}
	if s.StopSince == nil {
		t.Fatalf("stop_since is missing on a needs_you ship")
	}
	if ts, err := time.Parse(time.RFC3339, *s.StopSince); err != nil || ts.Before(start) || ts.After(time.Now().Add(time.Second)) {
		t.Errorf("stop_since = %q, want an RFC 3339 time within the last few seconds", *s.StopSince)
	}

	w.touch(ship.Session, "go")
	w.in.waitStatusList(t, 10*time.Second, ship.Session, "underway")
	if s := read(); s.Reason != nil && *s.Reason != "" || s.StopSince != nil && *s.StopSince != "" {
		t.Errorf("after the stop reason = %v and stop_since = %v, want neither", s.Reason, s.StopSince)
	}
}
