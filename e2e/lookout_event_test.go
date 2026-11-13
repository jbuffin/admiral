package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lookoutWorld is a fleet "work" with a proven guard and judge, the built-in GitHub source and a review request waiting for the viewer.
func lookoutWorld(t *testing.T) (*world, *fakeGH) {
	t.Helper()
	w := newWorld(t)
	w.proveGuard()
	g := w.newFakeGH()
	g.token("", "fake-token-default-1")
	root := filepath.Join(w.root, "ra")
	w.checkoutOf(root, "api", "acme/api")
	node := ghNode("github.com", "acme/api", 7, true, "Fix the quarry lantern")
	node["author"] = map[string]any{"login": "mallory-the-stranger"}
	// dated ahead, so it is newer than the baseline poll whenever that ran
	node["timelineItems"] = map[string]any{"nodes": []map[string]any{{"__typename": "ReviewRequestedEvent",
		"createdAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "actor": map[string]any{"login": "mallory-the-stranger"},
		"requestedReviewer": map[string]any{"login": "fake-viewer"}}}}
	g.search("review", "review-requested:@me", node)
	if err := os.MkdirAll(filepath.Join(g.dir, "rest"), 0o755); err != nil {
		t.Fatal(err)
	}
	g.write("viewer", "fake-viewer")
	g.write("rest/item.endpoint", "repos/acme/api/issues/7")
	g.write("rest/item.json", `{"title":"Fix the quarry lantern","body":"Please review my lantern fix."}`)
	env := w.harnessEnv()
	env["FAKE_JUDGE"] = "lookout"
	f := map[string]any{"name": "Work", "color": "#336699", "search_roots": []string{root}, "limits": map[string]any{"sender_allowlist": []string{"mallory-the-stranger"}},
		"claude": map[string]any{"executable": w.fakeExe("claude"), "env": env}}
	fb, _ := json.Marshal(f)
	writeConfig(t, w.home, ghConfig([]string{"work", string(fb)}, rawSources(
		srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Fleet: "work", Settings: map[string]any{"views": []any{}}})))
	return w, g
}

func (w *world) eventRoutine(name, before string) string {
	w.t.Helper()
	var code int
	var body string
	// the source reports its event kinds once its plugin has initialized, which can be after the server starts
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		code, body = send(w.t, http.MethodPost, w.in.LocalURL+"/api/fleets/work/routines", map[string]any{
			"name": name, "enabled": true, "before_acting": before,
			"on": map[string]any{"source": "gh", "kind": "review_requested"},
			"do": map[string]any{"launch": map[string]any{"harness": "claude", "dir": filepath.Join(w.root, "ra", "api"), "text": "sweep the board"}},
		})
		if code != http.StatusBadRequest || !strings.Contains(body, "raises events") || time.Now().After(deadline) {
			break
		}
	}
	if code != http.StatusCreated && code != http.StatusOK {
		w.t.Fatalf("POST routine = %d: %s", code, body)
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &made); err != nil || made.ID == "" {
		w.t.Fatalf("created routine has no id: %v\n%s", err, body)
	}
	return made.ID
}

func (w *world) waitRun(routine, outcome string) runJSON {
	w.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for i := 0; ; i++ {
		for _, r := range w.routineRuns(routine) {
			if r.Outcome == outcome {
				return r
			}
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("no %q record in 30 s: %+v", outcome, w.routineRuns(routine))
		}
		if i%10 == 0 { // the first poll only baselines, so ask for another
			w.srcReq(http.MethodPost, "/gh/refresh", map[string]any{})
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestAReviewRequestReachesAnAskFirstRoutineAsATieredRecordAndAnEventProposalWithNoEventText(t *testing.T) {
	t.Parallel()
	w, _ := lookoutWorld(t)
	w.start()
	id := w.eventRoutine("Review watch", "ask_first")
	rec := w.waitRun(id, "proposed")
	if rec.Tier == "" {
		t.Errorf("record = %+v, want a tier", rec)
	}
	var item inboxItem
	deadline := time.Now().Add(30 * time.Second)
	for item.ID == "" {
		if time.Now().After(deadline) {
			t.Fatalf("no event proposal in the Inbox: %+v", w.in.inbox(t))
		}
		for _, it := range w.in.inbox(t) {
			if it.Kind == "event_proposal" {
				item = it
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/inbox", nil); code != http.StatusOK || strings.Contains(body, "mallory") || strings.Contains(body, "quarry") {
		t.Errorf("GET /api/inbox = %d %s, want no event text", code, body)
	}
	code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/inbox/_admiral/"+item.ID+"/event", nil)
	if code != http.StatusOK || !strings.Contains(body, "https://github.com/acme/api/pull/7") {
		t.Errorf("event detail = %d %s, want the link", code, body)
	}
	if code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/inbox/_admiral/"+item.ID+"/launch", map[string]any{}); code != http.StatusCreated {
		t.Fatalf("launch = %d %s, want 201", code, body)
	}
	if r := w.waitRun(id, "launched"); r.Ship == "" {
		t.Errorf("launched record = %+v, want a ship", r)
	}
}

func TestAReviewRequestReachesAReportOnlyRoutineAsAnEventCardInChatAndNothingLaunches(t *testing.T) {
	t.Parallel()
	w, _ := lookoutWorld(t)
	w.start()
	id := w.eventRoutine("Report watch", "report_only")
	w.waitRun(id, "reported")
	deadline := time.Now().Add(30 * time.Second)
	for {
		code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/chat/work/session", nil)
		if code == http.StatusOK && strings.Contains(body, "event_card") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no event_card in the fleet chat: %d %s", code, body)
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, r := range w.routineRuns(id) {
		if r.Ship != "" {
			t.Errorf("record %+v launched a ship for a Report-only routine", r)
		}
	}
}
