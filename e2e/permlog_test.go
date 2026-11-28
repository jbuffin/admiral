package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/permlog"
)

const (
	permAsk   = `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"gh pr view 7"},"permission_suggestions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"gh pr view:*"}],"behavior":"allow","destination":"localSettings"}]}`
	permRan   = `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"gh pr view 7"},"tool_response":{"stdout":"ok"}}`
	permAsk2  = `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`
	permStop2 = `{"hook_event_name":"Stop","background_tasks":[]}`
)

// permWorld has fleet "cl" playing, once released, a Claude prompt allowed and then one refused, and fleet "co" playing three
// Copilot prompts answered by a person, the assisted reviewer and the unattended fallback.
func permWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	cues := map[string][]string{
		"cl": {"hook working", "wait go0", "event PermissionRequest " + permAsk, "wait go1", "event PostToolUse " + permRan,
			"event PermissionRequest " + permAsk2, "wait go2", "event Stop " + permStop2},
		"co": {"hook working", "permission human_response gh pr checks 12", "permission assisted_approval ls -la",
			"permission unattended_fallback git push origin main", `copilothook agentStop {"hook_event_name":"agentStop"}`},
	}
	fleets := map[string]any{}
	for key, lines := range cues {
		path := filepath.Join(w.root, "cue-"+key)
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		env := w.harnessEnv()
		env["FAKE_CUE"] = path
		env["COPILOT_HOME"] = filepath.Join(w.root, "copilot-"+key)
		dock := func(h string) map[string]any { return map[string]any{"executable": w.fakeExe(h), "env": env} }
		fleets[key] = map[string]any{"name": key, "search_roots": []string{w.work}, "claude": dock("claude"), "copilot": dock("copilot")}
	}
	b, err := json.Marshal(map[string]any{"version": 1, "fleets": fleets})
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(b))
	w.start()
	return w
}

// waitPrompts waits until the ship's log holds n answered prompts.
func (w *world) waitPrompts(ship string, n int) []permlog.Record {
	w.t.Helper()
	var got []permlog.Record
	eventually(w.t, fmt.Sprintf("the permission log of %s holding %d answered prompts", ship, n), func() (bool, string) {
		recs, err := permlog.Read(w.home, ship)
		answered := 0
		for _, r := range recs {
			if r.Answer != permlog.Open {
				answered++
			}
		}
		if err == nil && answered >= n {
			got = recs
			return true, ""
		}
		return false, fmt.Sprintf("%+v (err %v)", recs, err)
	})
	return got
}

func TestPermissionLog_RecordsEachPromptOfAShipAndItsCrewWithWhoAnswered(t *testing.T) {
	t.Parallel()
	w := permWorld(t)
	w.checkoutOf(w.work, "perms", "acme/perms")
	ship := w.launch(map[string]any{"fleet": "cl", "harness": "claude", "dir": filepath.Join(w.work, "perms"), "skill": noSkill, "order_id": "o-perms", "text": "go"})
	w.waitFile(ship.Session, "recorded")
	w.waitPaneWorking(shipPaneOf(w, ship.Session))
	w.crewBoat(ship.Session, "scout", "claude")
	for _, d := range []string{ship.Session, ship.Session + "/crew/scout"} {
		touch(t, filepath.Join(w.rec, d, "go0"))
	}
	w.waitShip(ship.Session, "asking for gh pr view", func(s apiShip) bool { return s.Reason == "permission:Bash" })

	// The engineer allows the ship's prompt from the in-place card, and the boat's in its terminal.
	code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, "/answer"), answerReq("permission", "permission:Bash", map[string]any{"choice": "allow"}))
	if !succeeded(code) {
		t.Fatalf("the card's allow = %d", code)
	}
	for _, d := range []string{ship.Session, ship.Session + "/crew/scout"} {
		touch(t, filepath.Join(w.rec, d, "go1"))
	}
	w.waitPrompts(ship.Session, 2)
	for _, d := range []string{ship.Session, ship.Session + "/crew/scout"} {
		touch(t, filepath.Join(w.rec, d, "go2"))
	}
	recs := w.waitPrompts(ship.Session, 4)

	byKey := map[string]permlog.Record{}
	for _, r := range recs {
		byKey[r.Crew+"|"+r.Call.Command] = r
	}
	for _, c := range []struct {
		key string
		ans permlog.Answer
		via permlog.Via
	}{
		{"|gh pr view 7", permlog.Allow, permlog.Card},
		{"scout|gh pr view 7", permlog.Allow, permlog.Terminal},
		{"|rm -rf build", permlog.Deny, permlog.Terminal},
		{"scout|rm -rf build", permlog.Deny, permlog.Terminal},
	} {
		r, ok := byKey[c.key]
		if !ok {
			t.Errorf("no record for %q in %+v", c.key, recs)
			continue
		}
		if r.Answer != c.ans || r.By != permlog.Person || r.Via != c.via {
			t.Errorf("%q: %s by %s via %s, want %s by a person via %s", c.key, r.Answer, r.By, r.Via, c.ans, c.via)
		}
		if r.Ship != ship.Session || r.Harness != "claude" || r.Fleet != "cl" || r.Repo != w.repoKeyOf("perms") || r.Call.Kind != "shell" {
			t.Errorf("%q: %+v", c.key, r)
		}
	}
	if s := byKey["|gh pr view 7"].Call.Suggestions; !strings.Contains(string(s), "gh pr view:*") {
		t.Errorf("the ship's record lost Claude's suggestion: %s", s)
	}

	t.Run("a Copilot ship's prompts say who answered", func(t *testing.T) {
		w.checkoutOf(w.work, "perms2", "acme/perms2")
		co := w.launch(map[string]any{"fleet": "co", "harness": "copilot", "dir": filepath.Join(w.work, "perms2"), "skill": noSkill, "order_id": "o-perms2", "text": "go"})
		recs := w.waitPrompts(co.Session, 3)
		repo := w.repoKeyOf("perms2")
		want := []struct {
			cmd string
			ans permlog.Answer
			by  permlog.By
		}{
			{"gh pr checks 12", permlog.Allow, permlog.Person},
			{"ls -la", permlog.Allow, permlog.Reviewer},
			{"git push origin main", permlog.Deny, permlog.Unattended},
		}
		if len(recs) != len(want) {
			t.Fatalf("got %+v", recs)
		}
		for i, c := range want {
			r := recs[i]
			if r.Call.Command != c.cmd || r.Answer != c.ans || r.By != c.by || r.Harness != "copilot" || r.Fleet != "co" || r.Repo != repo {
				t.Errorf("prompt %d: %+v, want %q %s by %s", i, r, c.cmd, c.ans, c.by)
			}
		}
		if ids := recs[0].Call.Identifiers; len(ids) != 1 || ids[0] != "gh" {
			t.Errorf("the person's prompt has identifiers %v", ids)
		}
	})
}

// repoKeyOf is how launch and the log name the checkout dir under w.work. Its insteadOf sends origin to a local
// bare repo, which is no GitHub origin, so the key is the checkout's real path.
func (w *world) repoKeyOf(dir string) string {
	w.t.Helper()
	p, err := filepath.EvalSymlinks(filepath.Join(w.work, dir))
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}
