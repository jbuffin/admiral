package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/keymap"
)

// answerWorld starts a Claude ship "alpha" whose pane records its raw input, a Codex ship, a Copilot ship, and a fleet whose Claude config dir the test can write transcripts into.
func answerWorld(t *testing.T) (w *world, cfgDir string) {
	t.Helper()
	w = newWorld(t)
	cfgDir = filepath.Join(w.root, "cfg")
	if err := os.MkdirAll(filepath.Join(cfgDir, "projects", "zz"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := w.harnessEnv()
	env["CLAUDE_CONFIG_DIR"] = cfgDir
	cfg, err := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{
		"work": map[string]any{"name": "Work", "search_roots": []string{w.work},
			"claude":  map[string]any{"executable": w.fakeExe("claude"), "env": env},
			"codex":   map[string]any{"executable": w.fakeExe("codex"), "env": env},
			"copilot": map[string]any{"executable": w.fakeExe("copilot"), "env": env}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(cfg))
	w.writeCue(inputCue...)
	w.start()
	return w, cfgDir
}

func claudeKeys(t *testing.T) keymap.Map {
	t.Helper()
	m, ok := keymap.For("claude")
	if !ok {
		t.Fatal("keymap.For(claude) has no key map")
	}
	return m
}

func codexKeys(t *testing.T) keymap.Map {
	t.Helper()
	m, ok := keymap.For("codex")
	if !ok {
		t.Fatal("keymap.For(codex) has no key map")
	}
	return m
}

func answerReq(card, reason string, more map[string]any) map[string]any {
	b := map[string]any{"card": card, "reason": reason}
	for k, v := range more {
		b[k] = v
	}
	return b
}

func succeeded(code int) bool { return code == http.StatusOK || code == http.StatusNoContent }

func TestShipAnswer_PermissionCardSendsTheKeyMapsKeysOnlyWhileTheReasonMatches(t *testing.T) {
	t.Parallel()
	w, _ := answerWorld(t)
	keys := claudeKeys(t)
	allow, deny := mustKeys(t)(keys.Permission("allow")), mustKeys(t)(keys.Permission("deny"))
	ship := w.launch(w.order("work", "claude", "alpha", noSkill, "go"))
	w.waitFile(ship.Session, "recorded")
	shipPane := shipPaneOf(w, ship.Session)
	w.waitPaneWorking(shipPane)
	scout := w.crewBoat(ship.Session, "scout", "claude")
	answer := func(tail string, body map[string]any) int {
		code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, tail), body)
		return code
	}

	w.hookAt(shipPane, "PermissionRequest", hookPerm)
	w.waitShip(ship.Session, "needs_you", func(s apiShip) bool { return s.Status == "needs_you" && s.Reason == "permission:Bash" })

	t.Run("a card for another reason sends nothing", func(t *testing.T) {
		if code := answer("/answer", answerReq("permission", "permission:Edit", map[string]any{"choice": "allow"})); code != http.StatusConflict {
			t.Errorf("answer with a stale reason = %d, want 409", code)
		}
		w.holdStdin(ship.Session, "", "")
		w.waitShip(ship.Session, "still needs_you", func(s apiShip) bool { return s.Status == "needs_you" })
	})

	t.Run("Allow once sends the key map's keys and clears needs you", func(t *testing.T) {
		if code := answer("/answer", answerReq("permission", "permission:Bash", map[string]any{"choice": "allow"})); !succeeded(code) {
			t.Fatalf("answer allow = %d, want 200 or 204", code)
		}
		w.waitStdinOf(ship.Session, "", allow)
		w.waitShip(ship.Session, "underway after the answer", func(s apiShip) bool { return s.Status == "underway" })
	})

	t.Run("Deny sends its keys", func(t *testing.T) {
		w.hookAt(shipPane, "PermissionRequest", hookPerm)
		w.waitShip(ship.Session, "needs_you again", func(s apiShip) bool { return s.Status == "needs_you" })
		if code := answer("/answer", answerReq("permission", "permission:Bash", map[string]any{"choice": "deny"})); !succeeded(code) {
			t.Fatalf("answer deny = %d, want 200 or 204", code)
		}
		w.waitStdinOf(ship.Session, "", allow+deny)
		w.waitShip(ship.Session, "needs_you stop after the deny", func(s apiShip) bool { return s.Status == "needs_you" && s.Reason == "stop" })
		if code := answer("/answer", answerReq("permission", "permission:Bash", map[string]any{"choice": "deny"})); code != http.StatusConflict {
			t.Errorf("a second deny of the same card = %d, want 409", code)
		}
		w.holdStdin(ship.Session, "", allow+deny)
	})

	t.Run("an answer sent just after the hook moved on is refused", func(t *testing.T) {
		w.hookAt(shipPane, "PermissionRequest", hookPerm)
		w.waitShip(ship.Session, "needs_you again", func(s apiShip) bool { return s.Reason == "permission:Bash" })
		w.hookAt(shipPane, "PostToolUse", `{"hook_event_name":"PostToolUse","tool_name":"Bash"}`)
		if code := answer("/answer", answerReq("permission", "permission:Bash", map[string]any{"choice": "allow"})); code != http.StatusConflict {
			t.Errorf("answer right after the card was answered elsewhere = %d, want 409", code)
		}
		w.holdStdin(ship.Session, "", allow+deny)
	})

	t.Run("two answers at once send once", func(t *testing.T) {
		w.hookAt(shipPane, "PermissionRequest", hookPerm)
		w.waitShip(ship.Session, "needs_you again", func(s apiShip) bool { return s.Reason == "permission:Bash" })
		codes := make(chan int, 2)
		for range 2 {
			go func() {
				codes <- answer("/answer", answerReq("permission", "permission:Bash", map[string]any{"choice": "allow"}))
			}()
		}
		a, b := <-codes, <-codes
		if !(succeeded(a) && b == http.StatusConflict) && !(succeeded(b) && a == http.StatusConflict) {
			t.Errorf("two answers at once = %d and %d, want one success and one 409", a, b)
		}
		w.waitStdinOf(ship.Session, "", allow+deny+allow)
		w.holdStdin(ship.Session, "", allow+deny+allow)
	})

	t.Run("a crew boat's card answers that boat only", func(t *testing.T) {
		w.hookAt(scout.pane, "PermissionRequest", hookPerm)
		w.waitShip(ship.Session, "scout needs_you", func(s apiShip) bool {
			c, _ := crewOf(s, "scout")
			return c.Status == "needs_you" && c.Reason != nil && *c.Reason == "permission:Bash"
		})
		if code := answer("/crew/scout/answer", answerReq("permission", "permission:Bash", map[string]any{"choice": "allow"})); !succeeded(code) {
			t.Fatalf("answer to the boat = %d, want 200 or 204", code)
		}
		w.waitStdinOf(ship.Session, "scout", allow)
		w.waitShip(ship.Session, "scout underway", func(s apiShip) bool {
			c, _ := crewOf(s, "scout")
			return c.Status == "underway"
		})
		w.holdStdin(ship.Session, "", allow+deny+allow)
	})
}

func TestShipAnswer_CodexPermissionCardSendsCodexKeysOnlyWhileTheReasonMatches(t *testing.T) {
	t.Parallel()
	w, _ := answerWorld(t)
	keys := codexKeys(t)
	allow, deny := mustKeys(t)(keys.Permission("allow")), mustKeys(t)(keys.Permission("deny"))
	ship := w.launch(w.order("work", "codex", "beta", noSkill, "go"))
	w.waitFile(ship.Session, "recorded")
	pane := shipPaneOf(w, ship.Session)
	w.waitPaneWorking(pane)
	answer := func(reason, choice string) int {
		code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, "/answer"), answerReq("permission", reason, map[string]any{"choice": choice}))
		return code
	}

	w.hookAt(pane, "PermissionRequest", hookPerm)
	w.waitShip(ship.Session, "needs_you", func(s apiShip) bool { return s.Status == "needs_you" && s.Reason == "permission:Bash" })

	if code := answer("permission:apply_patch", "allow"); code != http.StatusConflict {
		t.Errorf("answer to a Codex ship with a stale reason = %d, want 409", code)
	}
	w.holdStdin(ship.Session, "", "")

	if code := answer("permission:Bash", "allow"); !succeeded(code) {
		t.Fatalf("answer allow to a Codex ship = %d, want 200 or 204", code)
	}
	w.waitStdinOf(ship.Session, "", allow)
	w.waitShip(ship.Session, "underway after the answer", func(s apiShip) bool { return s.Status == "underway" })

	w.hookAt(pane, "PermissionRequest", hookPerm)
	w.waitShip(ship.Session, "needs_you again", func(s apiShip) bool { return s.Reason == "permission:Bash" })
	if code := answer("permission:Bash", "deny"); !succeeded(code) {
		t.Fatalf("answer deny to a Codex ship = %d, want 200 or 204", code)
	}
	w.waitStdinOf(ship.Session, "", allow+deny)
}

func TestShipAnswer_AShipOnAHarnessWithoutAKeyMapIsRefusedWith409(t *testing.T) {
	t.Parallel()
	w, _ := answerWorld(t)
	ship := w.launch(w.order("work", "copilot", "gamma", noSkill, "go"))
	w.waitFile(ship.Session, "recorded")
	pane := shipPaneOf(w, ship.Session)
	w.waitPaneWorking(pane)
	w.hookAt(pane, "PermissionRequest", hookPerm)
	w.waitShip(ship.Session, "needs_you", func(s apiShip) bool { return s.Status == "needs_you" && s.Reason == "permission:Bash" })

	code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, "/answer"), answerReq("permission", "permission:Bash", map[string]any{"choice": "allow"}))
	if code != http.StatusConflict {
		t.Errorf("answer to a Copilot ship = %d, want 409", code)
	}
	w.holdStdin(ship.Session, "", "")
}

func TestShipAnswer_QuestionCardSendsTheOptionNumberOnlyForTheNewestSingleChoiceQuestion(t *testing.T) {
	t.Parallel()
	w, cfgDir := answerWorld(t)
	keys := claudeKeys(t)
	second := mustKeys(t)(keys.Question(2))
	ship := w.launch(w.order("work", "claude", "alpha", noSkill, "go"))
	w.waitFile(ship.Session, "recorded")
	pane := shipPaneOf(w, ship.Session)
	w.waitPaneWorking(pane)
	id := sessionIDOf(t, w.record(ship.Session).argv)
	transcript := filepath.Join(cfgDir, "projects", "zz", id+".jsonl")
	var last string
	ask := func(uuid, call string, multi bool, questions int) string {
		m, _ := json.Marshal(multi)
		one := `{"question":"Which?","multiSelect":` + string(m) + `,"options":[{"label":"A","description":"a"},{"label":"B","description":"b"}]}`
		appendLines(t, transcript, `{"type":"assistant","uuid":"`+uuid+`","message":{"content":[{"type":"tool_use","id":"`+call+`","name":"AskUserQuestion","input":{"questions":[`+strings.TrimSuffix(strings.Repeat(one+",", questions), ",")+`]}}]}}`)
		var item string
		holdFor(t, "the question "+uuid+" in the conversation", func() bool {
			item = newestPendingQuestion(t, w, ship.Session)
			return item != "" && item != last
		})
		last = item
		return item
	}
	answer := func(body map[string]any) int {
		code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, "/answer"), body)
		return code
	}
	question := func(reason, item string, option int) map[string]any {
		return answerReq("question", reason, map[string]any{"item": item, "option": option})
	}
	// Claude Code fires PermissionRequest for AskUserQuestion after PreToolUse, which overwrites the reason.
	askInPane := func(want string) {
		w.hookAt(pane, "PreToolUse", `{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion"}`)
		w.hookAt(pane, "PermissionRequest", `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion"}`)
		w.waitShip(ship.Session, "needs_you for the question", func(s apiShip) bool { return s.Status == "needs_you" && s.Reason == want })
	}
	const qReason = "permission:AskUserQuestion"

	item := ask("q1", "tq1", false, 1)
	askInPane(qReason)

	if code := answer(answerReq("permission", qReason, map[string]any{"choice": "allow"})); code != http.StatusConflict {
		t.Errorf("a permission answer to a question menu = %d, want 409", code)
	}
	if code := answer(question("question", item, 2)); code != http.StatusConflict {
		t.Errorf("a question answer naming the reason the hook overwrote = %d, want 409", code)
	}

	if code := answer(question(qReason, "not-the-pending-item", 2)); code != http.StatusConflict {
		t.Errorf("answer naming another item = %d, want 409", code)
	}
	if code := answer(question(qReason, item, 3)); code != http.StatusConflict {
		t.Errorf("answer with option 3 of 2 = %d, want 409", code)
	}
	w.holdStdin(ship.Session, "", "")

	if code := answer(question(qReason, item, 2)); !succeeded(code) {
		t.Fatalf("answer with option 2 = %d, want 200 or 204", code)
	}
	w.waitStdinOf(ship.Session, "", second)
	w.waitShip(ship.Session, "underway after the answer", func(s apiShip) bool { return s.Status == "underway" })

	// A multi-select question is answered in the terminal.
	multi := ask("q2", "tq2", true, 1)
	askInPane("permission:AskUserQuestion")
	if code := answer(question(qReason, multi, 1)); code != http.StatusConflict {
		t.Errorf("answer to a multi-select question = %d, want 409", code)
	}
	w.holdStdin(ship.Session, "", second)

	// A call with several questions shows the first one; the newest item is the last.
	several := ask("q3", "tq3", false, 2)
	askInPane("permission:AskUserQuestion")
	if code := answer(question(qReason, several, 1)); code != http.StatusConflict {
		t.Errorf("answer to the last of several questions in one call = %d, want 409", code)
	}
	w.holdStdin(ship.Session, "", second)

	// Before PermissionRequest lands, the reason is still the plain `question`.
	first := mustKeys(t)(keys.Question(1))
	plain := ask("q4", "tq4", false, 1)
	w.hookAt(pane, "PreToolUse", `{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion"}`)
	w.waitShip(ship.Session, "needs_you for the plain question", func(s apiShip) bool { return s.Status == "needs_you" && s.Reason == "question" })
	if code := answer(question("question", plain, 1)); !succeeded(code) {
		t.Fatalf("answer with the plain question reason = %d, want 200 or 204", code)
	}
	w.waitStdinOf(ship.Session, "", second+first)
}

func mustKeys(t *testing.T) func(string, bool) string {
	t.Helper()
	return func(k string, ok bool) string {
		t.Helper()
		if !ok || k == "" {
			t.Fatal("the key map has no keys for this answer")
		}
		return k
	}
}

func sessionIDOf(t *testing.T, argv []string) string {
	t.Helper()
	for i, a := range argv {
		if a == "--session-id" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	t.Fatalf("argv = %q has no --session-id", argv)
	return ""
}

// newestPendingQuestion is the id of the last pending question the ship's conversation serves, "" when none.
func newestPendingQuestion(t *testing.T, w *world, session string) string {
	t.Helper()
	_, body := send(t, http.MethodGet, w.in.LocalURL+"/api/v1/ships/"+url.PathEscape(session)+"/conversation", nil)
	var p struct {
		Items []struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Question *struct {
				Pending bool `json:"pending"`
			} `json:"question"`
		} `json:"items"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	for i := len(p.Items) - 1; i >= 0; i-- {
		if it := p.Items[i]; it.Kind == "question" && it.Question != nil && it.Question.Pending {
			return it.ID
		}
	}
	return ""
}

func shipPaneOf(w *world, session string) string {
	return strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", "="+session+":0", "#{pane_id}"))
}
