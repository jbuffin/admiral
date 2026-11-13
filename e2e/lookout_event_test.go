package e2e

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lookoutWorld is a fleet "work" with a proven guard and judge, the built-in GitHub source and a review request waiting for the viewer.
func lookoutWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.proveGuard()
	g := w.newFakeGH()
	g.token("", "fake-token-default-1")
	root := filepath.Join(w.root, "ra")
	w.checkoutOf(root, "api", "acme/api")
	node := ghNode("github.com", "acme/api", 7, true, "Fix the quarry lantern")
	node["author"] = map[string]any{"login": "mallory-the-stranger"}
	g.search("review", "review-requested:@me", node)
	env := w.harnessEnv()
	env["FAKE_JUDGE"] = "lookout"
	f := map[string]any{"name": "Work", "color": "#336699", "search_roots": []string{root},
		"claude": map[string]any{"executable": w.fakeExe("claude"), "env": env}}
	fb, _ := json.Marshal(f)
	writeConfig(t, w.home, ghConfig([]string{"work", string(fb)}, rawSources(
		srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Fleet: "work", Settings: map[string]any{"views": []any{}}})))
	return w
}

func (w *world) eventRoutine(name, before string) string {
	w.t.Helper()
	code, body := send(w.t, http.MethodPost, w.in.LocalURL+"/api/fleets/work/routines", map[string]any{
		"name": name, "enabled": true, "before_acting": before,
		"on": map[string]any{"source": "gh", "kind": "review_requested"},
		"do": map[string]any{"launch": map[string]any{"harness": "claude", "dir": w.checkout("lookout-" + before), "text": "sweep the board"}},
	})
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
	for {
		for _, r := range w.routineRuns(routine) {
			if r.Outcome == outcome {
				return r
			}
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("no %q record in 30 s: %+v", outcome, w.routineRuns(routine))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestAReviewRequestReachesAnAskFirstRoutineAsATieredRecordAndAnEventProposalWithNoEventText(t *testing.T) {
	t.Parallel()
	w := lookoutWorld(t)
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
	w := lookoutWorld(t)
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
