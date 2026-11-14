package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
)

// lookoutWorld is a fleet "work" with a proven guard and judge, the built-in GitHub source and a review request waiting for the viewer.
func lookoutWorld(t *testing.T) (*world, *fakeGH) {
	t.Helper()
	return lookoutWorldWith(t, nil)
}

// lookoutWorldWith is lookoutWorld with more keys in the fleet's limits, as a Limits save would leave them.
func lookoutWorldWith(t *testing.T, limits map[string]any) (*world, *fakeGH) {
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
	// tokens a restricted ship must lose, so the scrub assertion has something to catch
	env["GH_TOKEN"], env["GITHUB_TOKEN"] = "fake-token-work-2", "fake-token-work-3"
	lim := map[string]any{"sender_allowlist": []string{"mallory-the-stranger"}}
	for k, v := range limits {
		lim[k] = v
	}
	f := map[string]any{"name": "Work", "color": "#336699", "search_roots": []string{root}, "limits": lim,
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

// seedRestricted stores the two restricted capabilities for claude's detected version, beside proveGuard's guard and judge.
func (w *world) seedRestricted(fleet string) {
	w.t.Helper()
	store, now := capabilities.Open(w.home), time.Now()
	for _, id := range []capabilities.ID{capabilities.RestrictedSandbox, capabilities.RestrictedAllow} {
		if err := store.Record(fleet, "claude", fakeVersion, id, capabilities.Result{Passed: true, At: now, Detail: "seeded by the test"}); err != nil {
			w.t.Fatal(err)
		}
	}
}

func TestAReviewRequestFromAnAllowlistedSenderLaunchesARestrictedDraftFixShipInItsOwnCloneWithNoTokens(t *testing.T) {
	t.Parallel()
	w, _ := lookoutWorldWith(t, map[string]any{"ceilings": map[string]any{"t2": map[string]any{"level": "A3", "restricted": "draft_fix"}}})
	w.seedRestricted("work")
	w.start()
	id := w.eventRoutine("Draft fix watch", "act")
	run := w.waitRun(id, "launched")
	if run.Restricted != "draft_fix" || run.Ship == "" {
		t.Fatalf("launched record = %+v, want restricted draft_fix and a ship", run)
	}
	if got := w.shipsWhere(func(s shipJSON) bool { return s.Session == run.Ship }); len(got) != 1 {
		t.Errorf("the API lists %d ships named %s, want 1", len(got), run.Ship)
	}
	event, err := os.ReadFile(filepath.Join(w.home, "checkouts", run.Ship, ".admiral", "untrusted", "event.md"))
	if err != nil || !strings.Contains(string(event), "Fix the quarry lantern") || !strings.Contains(string(event), "Please review my lantern fix.") {
		t.Errorf("event.md = %q (%v), want the event's title and body", event, err)
	}
	settingsPath := filepath.Join(w.home, "restricted", run.Ship+"-settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("no restricted settings file: %v", err)
	}
	var cfg struct {
		Sandbox struct {
			Enabled                  bool `json:"enabled"`
			AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands"`
			Network                  struct {
				AllowedDomains []string `json:"allowedDomains"`
			} `json:"network"`
		} `json:"sandbox"`
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, raw)
	}
	if !cfg.Sandbox.Enabled || cfg.Sandbox.AllowUnsandboxedCommands {
		t.Errorf("sandbox = %+v, want it on with unsandboxed commands off", cfg.Sandbox)
	}
	for _, host := range []string{"registry.npmjs.org", "proxy.golang.org", "pypi.org", "crates.io"} {
		if !slices.Contains(cfg.Sandbox.Network.AllowedDomains, host) {
			t.Errorf("allowedDomains = %v, want the registry %s", cfg.Sandbox.Network.AllowedDomains, host)
		}
	}
	for _, tool := range []string{"WebFetch", "WebSearch", "mcp__*", "Bash(git push *)"} {
		if !slices.Contains(cfg.Permissions.Deny, tool) {
			t.Errorf("deny = %v, want %s", cfg.Permissions.Deny, tool)
		}
	}
	rec := w.record(run.Ship)
	want := []string{"--permission-mode", "dontAsk", "--setting-sources", "project", "--settings", settingsPath}
	if at := slices.Index(rec.argv, "--permission-mode"); at < 0 || !slices.Equal(rec.argv[at:min(at+len(want), len(rec.argv))], want) {
		t.Errorf("argv = %q, want %q in a row", rec.argv, want)
	}
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v, ok := rec.env[k]; ok && v != "" {
			t.Errorf("env holds %s = %q, want no token in a restricted ship", k, v)
		}
	}
	if rec.env["GIT_CONFIG_GLOBAL"] != "/dev/null" {
		t.Errorf("GIT_CONFIG_GLOBAL = %q, want /dev/null", rec.env["GIT_CONFIG_GLOBAL"])
	}

	// the launch posts one launch card for the record, with no event body in it
	var chatBody string
	waitCond(t, 30*time.Second, "the fleet chat to hold the launch card", func() bool {
		_, chatBody = send(t, http.MethodGet, w.in.LocalURL+"/api/chat/work/session", nil)
		return strings.Contains(chatBody, "launch_card")
	})
	if n := strings.Count(chatBody, `"launch_card"`); n != 1 {
		t.Fatalf("the fleet chat holds %d launch cards, want 1: %s", n, chatBody)
	}
	if !strings.Contains(chatBody, run.Ship) || !strings.Contains(chatBody, run.ID) {
		t.Errorf("the launch card does not name the ship %s and record %s: %s", run.Ship, run.ID, chatBody)
	}
	if strings.Contains(chatBody, "Please review my lantern fix.") {
		t.Errorf("the chat holds the event body: %s", chatBody)
	}

	// Undo stops the ship, removes its clone and marks the record
	clone := filepath.Join(w.home, "checkouts", run.Ship)
	if _, err := os.Stat(clone); err != nil {
		t.Fatalf("no clone to undo at %s: %v", clone, err)
	}
	undo := w.in.LocalURL + "/api/fleets/work/routines/" + id + "/runs/" + run.ID + "/undo"
	if code, body := send(t, http.MethodPost, undo, map[string]any{}); code != http.StatusOK {
		t.Fatalf("undo = %d %s, want 200", code, body)
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Errorf("the clone %s is still there after undo (%v)", clone, err)
	}
	w.waitShipCount(30*time.Second, "no ship named "+run.Ship+" after undo", func(s shipJSON) bool { return s.Session == run.Ship }, 0)
	var marked bool
	for _, r := range w.routineRuns(id) {
		if r.ID == run.ID {
			marked = r.Undone
		}
	}
	if !marked {
		t.Errorf("the record %s is not marked undone: %+v", run.ID, w.routineRuns(id))
	}
}

func TestPromotingARestrictedDraftFixShipStartsANormalShipWithTheTokensInItsOwnCloneAtTheRestrictedWork(t *testing.T) {
	t.Parallel()
	w, _ := lookoutWorldWith(t, map[string]any{"ceilings": map[string]any{"t2": map[string]any{"level": "A3", "restricted": "draft_fix"}}})
	w.seedRestricted("work")
	api := filepath.Join(w.root, "ra", "api")
	if err := os.WriteFile(filepath.Join(api, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.git(api, "add", "base.txt")
	w.git(api, "-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "base")
	w.start()
	id := w.eventRoutine("Draft fix watch", "act")
	run := w.waitRun(id, "launched")
	if run.Restricted != "draft_fix" || run.Ship == "" {
		t.Fatalf("launched record = %+v, want restricted draft_fix and a ship", run)
	}
	clone := filepath.Join(w.home, "checkouts", run.Ship)
	if err := os.WriteFile(filepath.Join(clone, "fix.txt"), []byte("the fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.git(clone, "add", "fix.txt")
	w.git(clone, "-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "fix")

	promote := w.in.LocalURL + "/api/fleets/work/routines/" + id + "/runs/" + run.ID + "/promote"
	code, body := send(t, http.MethodPost, promote, map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("promote = %d %s, want 200", code, body)
	}
	var heir runJSON
	waitCond(t, 30*time.Second, "the record to name the promoted ship", func() bool {
		for _, r := range w.routineRuns(id) {
			if r.ID == run.ID {
				heir = r
			}
		}
		return heir.PromotedShip != ""
	})
	if !heir.Promoted || heir.Ship != run.Ship || heir.PromotedShip == run.Ship {
		t.Fatalf("record = %+v, want promoted to a ship other than %s, with its own ship unchanged", heir, run.Ship)
	}
	w.waitShipCount(30*time.Second, "the promoted ship in the API", func(s shipJSON) bool { return s.Session == heir.PromotedShip }, 1)

	rec := w.record(heir.PromotedShip)
	for _, f := range []string{"--permission-mode", "--setting-sources", "--resume"} {
		if slices.Contains(rec.argv, f) {
			t.Errorf("argv = %q, want no %s: a promoted ship is a normal, new session", rec.argv, f)
		}
	}
	if rec.env["GH_TOKEN"] == "" {
		t.Errorf("env has no GH_TOKEN: a promoted ship keeps the fleet's tokens")
	}
	prompt := rec.argv[len(rec.argv)-1]
	for _, banned := range []string{"Fix the quarry lantern", "Please review my lantern fix.", "mallory"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("the promoted ship's order carries %q: %s", banned, prompt)
		}
	}
	newClone := filepath.Join(w.home, "checkouts", heir.PromotedShip)
	if b, err := os.ReadFile(filepath.Join(newClone, "fix.txt")); err != nil || string(b) != "the fix\n" {
		t.Errorf("fix.txt in %s = %q (%v), want the restricted ship's commit", newClone, b, err)
	}
	if got := w.git(newClone, "branch", "--show-current"); got != "admiral/"+heir.PromotedShip {
		t.Errorf("branch = %q, want admiral/%s", got, heir.PromotedShip)
	}
	if _, err := os.Stat(filepath.Join(newClone, ".admiral", "untrusted")); err == nil {
		t.Error("the promoted ship's clone holds the event file")
	}
	w.waitShipCount(30*time.Second, "the restricted ship stopped by Promote", func(s shipJSON) bool { return s.Session == run.Ship && live(s) }, 0)

	again, _ := send(t, http.MethodPost, promote, map[string]any{})
	if again != http.StatusOK {
		t.Errorf("a second promote = %d, want 200 with the first ship", again)
	}
	if code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/fleets/work/routines/"+id+"/runs/"+run.ID+"/undo", map[string]any{}); code != http.StatusBadRequest || !strings.Contains(body, "promoted") {
		t.Errorf("undo of a promoted record = %d %s, want 400 promoted", code, body)
	}
}
