package ships

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
)

const handIssue = "https://github.com/acme/widgets/issues/3"

// handRig is a live Claude ship, saltpaw, launched from an issue in a checkout whose origin is acme/widgets.
type handRig struct{ *nextRig }

func newHandRig(t *testing.T) *handRig {
	t.Helper()
	r := newWakeRig(t, 0)
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", r.checkout}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.RemoveAll(filepath.Join(r.checkout, ".git")); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	run("remote", "add", "origin", "https://github.com/acme/widgets.git")
	_, lerr := r.s.LaunchOrder(launcher.Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Model: "opus", Name: "Saltpaw",
		Session: "saltpaw", Prompt: "go", Skill: launcher.Skill{Kind: "none"}, Issues: []string{handIssue}, Resources: []string{"phone"}})
	if lerr != nil {
		t.Fatalf("LaunchOrder = %v", lerr)
	}
	r.started(t, 1)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	return &handRig{&nextRig{wakeRig: r, mux: mux}}
}

func (r *handRig) hand(session, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/ships/"+session+"/commodore", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.mux.ServeHTTP(rec, req)
	return rec
}

// fullPane is everything the pane has shown, wrapped lines joined, so a long path in the prompt reads whole.
func (r *handRig) fullPane(t *testing.T) string {
	t.Helper()
	out, err := exec.Command(r.s.Launcher.Tmux, "-S", r.s.Launcher.Socket, "capture-pane", "-p", "-J", "-S", "-", "-t", "saltpaw").CombinedOutput()
	if err != nil {
		t.Fatalf("capture-pane: %v: %s", err, out)
	}
	return string(out)
}

var refPathInPrompt = regexp.MustCompile(`\S*/ships/saltpaw/[\w.\-]+`)

// refPath is the file the typed prompt names, trimmed of the punctuation a sentence puts after it.
func (r *handRig) refPath(t *testing.T) string {
	t.Helper()
	m := refPathInPrompt.FindString(r.fullPane(t))
	if m == "" {
		t.Fatalf("the prompt names no ref file under ships/saltpaw/; pane:\n%s", r.fullPane(t))
	}
	return strings.TrimRight(m, ".,;:)\"'`")
}

// endTurn is what the ship's Stop hook does, then one pass of the poller.
func (r *handRig) endTurn(t *testing.T) {
	t.Helper()
	r.turn(t, "needs_you", "stop")
	r.s.advanceHandoffs()
	r.s.settleWG.Wait()
}

// queue hands the working ship and walks it to the prompt being typed, answering the file the prompt names.
func (r *handRig) queue(t *testing.T, body string) string {
	t.Helper()
	r.turn(t, "working", "")
	if rec := r.hand("saltpaw", body); rec.Code != http.StatusAccepted || strings.TrimSpace(rec.Body.String()) != `{"handing":true}` {
		t.Fatalf("hand = %d %s, want 202 {\"handing\":true}", rec.Code, rec.Body)
	}
	r.s.advanceHandoffs()
	r.s.settleWG.Wait()
	if strings.Contains(r.fullPane(t), "/ships/saltpaw/") {
		t.Fatal("the prompt reached a ship mid-turn")
	}
	r.endTurn(t)
	waitUntil(t, "the prompt at the ship's prompt", func() bool { return strings.Contains(r.fullPane(t), "/ships/saltpaw/") })
	return r.refPath(t)
}

func writeRef(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *handRig) setPaneOption(t *testing.T, name, value string) {
	t.Helper()
	if out, err := exec.Command(r.s.Launcher.Tmux, "-S", r.s.Launcher.Socket, "set-option", "-p", "-t", "saltpaw", name, value).CombinedOutput(); err != nil {
		t.Fatalf("set-option %s: %v: %s", name, err, out)
	}
	_ = r.s.Host.Sync(context.Background())
}

// wantRefusedBeforeAnySideEffect asserts the status, a JSON error and that nothing was queued, typed or logged.
func (r *handRig) wantRefusedBeforeAnySideEffect(t *testing.T, rec *httptest.ResponseRecorder, status int) string {
	t.Helper()
	msg := wantError(t, rec, status, "hand")
	r.s.advanceHandoffs()
	r.s.settleWG.Wait()
	if strings.Contains(r.fullPane(t), "/ships/saltpaw/") {
		t.Error("a refused hand still typed its prompt")
	}
	if got := outcomeLines(t, r.home, "saltpaw", "handing"); len(got) != 0 {
		t.Errorf("handing records = %v, want none for a refused hand", got)
	}
	return msg
}

func TestHandToCommodoreRefusesWhatCannotBeHanded(t *testing.T) {
	messages := map[string]string{}
	note := func(t *testing.T, name, msg string) {
		t.Helper()
		for other, m := range messages {
			if m == msg {
				t.Errorf("%s and %s refuse with the same message %q: each reason has its own", name, other, msg)
			}
		}
		messages[name] = msg
	}

	t.Run("an unknown ship is 404, and a bad body is 400", func(t *testing.T) {
		r := newHandRig(t)
		r.turn(t, "needs_you", "stop")
		wantError(t, r.hand("nosuchship", `{"kind":"issue"}`), http.StatusNotFound, "an unknown ship")
		for _, body := range map[string]string{
			"no body":               ``,
			"no kind":               `{}`,
			"an unknown kind":       `{"kind":"banana"}`,
			"other without text":    `{"kind":"other"}`,
			"other with blank text": `{"kind":"other","text":"  \n "}`,
			"a control character":   `{"kind":"other","text":"a\u0007b"}`,
			"text over 4 KiB":       `{"kind":"other","text":"` + strings.Repeat("x", 4097) + `"}`,
		} {
			r.wantRefusedBeforeAnySideEffect(t, r.hand("saltpaw", body), http.StatusBadRequest)
		}
	})

	t.Run("a ship that is returning to port", func(t *testing.T) {
		r := newHandRig(t)
		r.turn(t, "needs_you", "stop")
		if rec := r.returnToPort("saltpaw"); rec.Code != http.StatusOK {
			t.Fatalf("return = %d %s", rec.Code, rec.Body)
		}
		note(t, "returning", r.wantRefusedBeforeAnySideEffect(t, r.hand("saltpaw", `{"kind":"issue"}`), http.StatusConflict))
	})

	t.Run("a ship already being handed", func(t *testing.T) {
		r := newHandRig(t)
		r.turn(t, "working", "")
		if rec := r.hand("saltpaw", `{"kind":"issue"}`); rec.Code != http.StatusAccepted {
			t.Fatalf("first hand = %d %s, want 202", rec.Code, rec.Body)
		}
		note(t, "handing", wantError(t, r.hand("saltpaw", `{"kind":"issue"}`), http.StatusConflict, "a second hand"))
		if got := outcomeLines(t, r.home, "saltpaw", "handing"); len(got) != 1 {
			t.Errorf("handing records = %v, want one", got)
		}
	})

	t.Run("a ship that exited", func(t *testing.T) {
		r := newHandRig(t)
		if err := os.WriteFile(filepath.Join(r.rec, "exit"), []byte("0"), 0o600); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, "the pane to die", func() bool {
			_ = r.s.Host.Sync(context.Background())
			for _, sess := range r.s.Host.Sessions() {
				if sess.Name == "saltpaw" && sess.PaneDead {
					return true
				}
			}
			return false
		})
		note(t, "exited", wantError(t, r.hand("saltpaw", `{"kind":"issue"}`), http.StatusConflict, "an exited ship"))
	})

	t.Run("a moored ship", func(t *testing.T) {
		w := newWakeRig(t, 0)
		w.moored(t, 1)
		mux := http.NewServeMux()
		w.s.Routes(mux)
		r := &handRig{&nextRig{wakeRig: w, mux: mux}}
		note(t, "moored", wantError(t, r.hand("saltpaw", `{"kind":"issue"}`), http.StatusConflict, "a moored ship"))
	})

	t.Run("a ship that is not on Claude", func(t *testing.T) {
		r := newHandRig(t)
		r.turn(t, "needs_you", "stop")
		r.setPaneOption(t, "@admiral-harness", "codex")
		note(t, "harness", r.wantRefusedBeforeAnySideEffect(t, r.hand("saltpaw", `{"kind":"issue"}`), http.StatusConflict))
	})

	t.Run("a ship whose conversation id is not the one it was started with", func(t *testing.T) {
		r := newHandRig(t)
		r.turn(t, "needs_you", "stop")
		forged := filepath.Join(r.home, "claude", "projects", "zz", "11111111-2222-4333-8444-555555555555.jsonl")
		r.setPaneOption(t, "@admiral-transcript", "1 launch "+forged)
		note(t, "unbound", r.wantRefusedBeforeAnySideEffect(t, r.hand("saltpaw", `{"kind":"issue"}`), http.StatusConflict))
	})

	t.Run("a ship that is already Commodore", func(t *testing.T) {
		r := newHandRig(t)
		r.turn(t, "needs_you", "stop")
		r.setPaneOption(t, "@admiral-skill-kind", "commodore")
		note(t, "commodore", r.wantRefusedBeforeAnySideEffect(t, r.hand("saltpaw", `{"kind":"issue"}`), http.StatusConflict))
	})

	t.Run("a restricted ship", func(t *testing.T) {
		r := newHandRig(t)
		r.turn(t, "needs_you", "stop")
		r.s.EventShip = func(fleet, session string) (bool, bool) { return session == "saltpaw", false }
		note(t, "restricted", r.wantRefusedBeforeAnySideEffect(t, r.hand("saltpaw", `{"kind":"issue"}`), http.StatusConflict))
	})

	t.Run("a ship an outside event launched", func(t *testing.T) {
		r := newHandRig(t)
		r.turn(t, "needs_you", "stop")
		r.s.EventShip = func(fleet, session string) (bool, bool) { return false, session == "saltpaw" }
		note(t, "event", r.wantRefusedBeforeAnySideEffect(t, r.hand("saltpaw", `{"kind":"issue"}`), http.StatusConflict))
	})
}

func TestHandToCommodoreWaitsForTheTurnThenAsksForAnIssueInTheShipsRepo(t *testing.T) {
	r := newHandRig(t)
	ref := r.queue(t, `{"kind":"issue"}`)

	if !strings.HasPrefix(ref, r.home+"/ships/saltpaw/") {
		t.Errorf("ref file %q is not under $ADMIRAL_HOME/ships/saltpaw/", ref)
	}
	if strings.HasPrefix(ref, r.checkout) {
		t.Errorf("ref file %q is inside the checkout", ref)
	}
	pane := r.fullPane(t)
	if !strings.Contains(pane, handIssue) {
		t.Errorf("the prompt does not name the issue the ship was launched from (%s); pane:\n%s", handIssue, pane)
	}
	if got := outcomeLines(t, r.home, "saltpaw", "handing"); len(got) != 1 {
		t.Errorf("handing records = %v, want one", got)
	}
	if !r.afloat() {
		t.Error("the ship was stopped before its ref file was read")
	}
}

func TestHandToCommodoreOtherQuotesTheEngineersTextInThePrompt(t *testing.T) {
	r := newHandRig(t)
	r.queue(t, `{"kind":"other","text":"write the design to a doc\nand keep it short"}`)
	pane := r.fullPane(t)
	for _, want := range []string{"write the design to a doc", "and keep it short"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the prompt lacks the engineer's text %q; pane:\n%s", want, pane)
		}
	}
}

func TestHandToCommodoreStopsTheShipAndRelaunchesItAsCommodoreOnAGoodIssueRef(t *testing.T) {
	r := newHandRig(t)
	ref := r.queue(t, `{"kind":"issue"}`)
	writeRef(t, ref, "https://github.com/Acme/Widgets/issues/9\n")
	r.endTurn(t)

	r.started(t, 2)
	waitUntil(t, "the ship relaunched as Commodore", func() bool { return r.options(t, "saltpaw")["skill-kind"] == "commodore" })
	o := r.options(t, "saltpaw")
	if o["name"] != "Saltpaw" || o["dir"] != r.checkout || o["fleet"] != "work" || o["harness"] != "claude" || o["url"] != handIssue {
		t.Errorf("relaunched options = %v, want the same name, fleet, harness, dir and issue", o)
	}
	if got := outcomeLines(t, r.home, "saltpaw", "stopped"); len(got) != 1 || got[0]["handed"] != true {
		t.Errorf("stopped records = %v, want one marked handed", got)
	}
	if got := outcomeLines(t, r.home, "saltpaw", "handed"); len(got) != 1 || got[0]["ref"] != "https://github.com/Acme/Widgets/issues/9" {
		t.Errorf("handed records = %v, want one naming the ref", got)
	}
	if _, err := os.Stat(ref); err == nil {
		t.Error("the ref file is still there after it was read")
	}
}

func TestHandToCommodoreAcceptsAnOtherBriefUnderTheShipsRefDirectory(t *testing.T) {
	r := newHandRig(t)
	r.queue(t, `{"kind":"other","text":"brief it"}`)
	brief := filepath.Join(r.home, "ships", "saltpaw", "brief.md")
	writeRef(t, brief, "the design\n")
	writeRef(t, r.refPath(t), brief+"\n")
	r.endTurn(t)

	r.started(t, 2)
	waitUntil(t, "the ship relaunched as Commodore", func() bool { return r.options(t, "saltpaw")["skill-kind"] == "commodore" })
	if got := outcomeLines(t, r.home, "saltpaw", "handed"); len(got) != 1 || got[0]["ref"] != brief {
		t.Errorf("handed records = %v, want one naming %s", got, brief)
	}
}

func TestHandToCommodoreLeavesTheShipRunningOnABadRef(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	for _, c := range []struct {
		name  string
		write func(t *testing.T, r *handRig, ref string)
	}{
		{"an issue in another repo", func(t *testing.T, r *handRig, ref string) {
			writeRef(t, ref, "https://github.com/other/widgets/issues/9\n")
		}},
		{"a file outside both roots", func(t *testing.T, r *handRig, ref string) {
			writeRef(t, outside, "the design\n")
			writeRef(t, ref, outside+"\n")
		}},
		{"a symlink in the checkout that escapes it", func(t *testing.T, r *handRig, ref string) {
			writeRef(t, outside, "the design\n")
			link := filepath.Join(r.checkout, "design.md")
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			writeRef(t, ref, link+"\n")
		}},
		{"an empty file under the checkout", func(t *testing.T, r *handRig, ref string) {
			empty := filepath.Join(r.checkout, "empty.md")
			writeRef(t, empty, "")
			writeRef(t, ref, empty+"\n")
		}},
		{"an empty ref file", func(t *testing.T, r *handRig, ref string) { writeRef(t, ref, "") }},
		{"no ref file", func(t *testing.T, r *handRig, ref string) {}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newHandRig(t)
			ref := r.queue(t, `{"kind":"issue"}`)
			c.write(t, r, ref)
			r.endTurn(t)

			if !r.afloat() {
				t.Fatal("the ship was stopped on a bad ref")
			}
			if _, err := os.Stat(filepath.Join(r.rec, "argv-2")); err == nil {
				t.Error("the ship was relaunched on a bad ref")
			}
			got := outcomeLines(t, r.home, "saltpaw", "handing_failed")
			if len(got) != 1 || got[0]["reason"] == nil || got[0]["reason"] == "" {
				t.Errorf("handing_failed records = %v, want one with a reason", got)
			}
			if got := outcomeLines(t, r.home, "saltpaw", "stopped"); len(got) != 0 {
				t.Errorf("stopped records = %v, want none", got)
			}
			if _, err := os.Stat(ref); err == nil {
				t.Error("the ref file is still there after it was read")
			}
			if n := strings.Count(r.fullPane(t), "/ships/saltpaw/"); n != 1 {
				t.Errorf("the prompt was typed %d times, want once: Admiral never retries on its own", n)
			}
			if rec := r.hand("saltpaw", `{"kind":"issue"}`); rec.Code != http.StatusAccepted {
				t.Errorf("a second hand after the bad ref = %d %s, want 202: the mark is cleared", rec.Code, rec.Body)
			}
		})
	}
}

func TestHandToCommodoreRecordsWhyAndLeavesTheShipStoppedWhenTheRelaunchFails(t *testing.T) {
	r := newHandRig(t)
	ref := r.queue(t, `{"kind":"issue"}`)
	sessionID := sessionIDOf(t, mustArgv(t, r, 1))
	writeRef(t, ref, "https://github.com/acme/widgets/issues/9\n")
	r.mu.Lock()
	r.refuseName, r.refuseWhy = "phone", "busy"
	r.mu.Unlock()
	r.endTurn(t)

	waitUntil(t, "the ship to be stopped", func() bool { return !r.afloat() })
	var got []map[string]any
	waitUntil(t, "the failure to be recorded", func() bool { got = outcomeLines(t, r.home, "saltpaw", "handing_failed"); return len(got) > 0 })
	waitUntil(t, "the stopped ship's usage record, which would otherwise outlive the test", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "usage")) > 0 })
	if len(got) != 1 || !strings.Contains(fmt.Sprint(got[0]["reason"]), "phone") || got[0]["conversation"] != sessionID {
		t.Errorf("handing_failed records = %v, want the reason and conversation %s", got, sessionID)
	}
	if _, err := os.Stat(filepath.Join(r.rec, "argv-2")); err == nil {
		t.Error("the relaunch went ahead")
	}
}

func mustArgv(t *testing.T, r *handRig, n int) []string {
	t.Helper()
	argv, _ := r.started(t, n)
	return argv
}
