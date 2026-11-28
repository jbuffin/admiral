package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// Interrupting the chat presses one Escape in its pane and withdraws what it asked, or does nothing.

// keyLog is a tmux that records each call, then runs the real one; a file named fail beside it makes send-keys fail.
type keyLog struct {
	log, fail string
}

// mutating are the tmux subcommands that change what a chat pane holds or whether it runs.
var mutating = []string{"send-keys", "send-prefix", "paste-buffer", "load-buffer", "set-buffer", "new-session", "new-window", "respawn-pane", "kill-session", "kill-pane"}

func (r *turnRig) recordTmux() *keyLog {
	r.t.Helper()
	dir := r.t.TempDir()
	k := &keyLog{log: filepath.Join(dir, "calls"), fail: filepath.Join(dir, "fail")}
	script := "#!/bin/sh\necho \"$*\" >> '" + k.log + "'\n" +
		"case \" $* \" in *\" send-keys \"*) if [ -e '" + k.fail + "' ]; then echo 'no server running' >&2; exit 1; fi;; esac\n" +
		"exec '" + r.svc.Tmux + "' \"$@\"\n"
	wrapper := filepath.Join(dir, "tmux")
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		r.t.Fatal(err)
	}
	r.svc.Tmux = wrapper
	return k
}

func (k *keyLog) failKeys(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(k.fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// changes are the logged calls that could change the pane or end the session, one line each.
func (k *keyLog) changes() []string {
	b, _ := os.ReadFile(k.log)
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		for _, f := range strings.Fields(line) {
			if sliceHas(mutating, f) {
				out = append(out, line)
				break
			}
		}
	}
	return out
}

func sliceHas(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// escapes asserts the pane got exactly want Escape key presses and nothing else that changes it.
func (r *turnRig) escapes(k *keyLog, want int) {
	r.t.Helper()
	sess, _ := r.fc.session()
	calls := k.changes()
	if len(calls) != want {
		r.t.Fatalf("pane changes = %q, want %d Escape presses and nothing else", calls, want)
	}
	for _, c := range calls {
		fields := strings.Fields(c)
		if !sliceHas(fields, "send-keys") || fields[len(fields)-1] != "Escape" || !strings.Contains(c, sess.PaneID) {
			r.t.Errorf("pane change %q, want one Escape sent to the chat pane %s", c, sess.PaneID)
		}
	}
}

func (r *turnRig) interrupt() *httptest.ResponseRecorder {
	r.t.Helper()
	mux := http.NewServeMux()
	r.svc.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/chat/work/interrupt", nil))
	return rec
}

func interrupted(t *testing.T, rec *httptest.ResponseRecorder, code int, did bool) {
	t.Helper()
	var got struct {
		Interrupted *bool `json:"interrupted"`
	}
	if rec.Code != code {
		t.Fatalf("interrupt = %d %s, want %d", rec.Code, rec.Body, code)
	}
	if code != http.StatusOK {
		return
	}
	if json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Interrupted == nil || *got.Interrupted != did {
		t.Fatalf("interrupt body = %s, want {\"interrupted\":%v}", rec.Body, did)
	}
}

// answerState is what the question's answer route says: open, withdrawn or answered.
func (r *turnRig) answerState(id string) string {
	r.t.Helper()
	mux := http.NewServeMux()
	r.svc.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/chat/questions/"+id+"/answer", nil).WithContext(actor.WithFleet(context.Background(), "work")))
	var got struct{ State string }
	if json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		r.t.Fatalf("answer = %d %s", rec.Code, rec.Body)
	}
	return got.State
}

func (r *turnRig) proposal() string {
	r.t.Helper()
	if err := r.svc.Propose("work", commitments.Proposal{ID: "campaign-w1-ab12-1", Title: "Start Annex doors with you?", Child: "https://github.com/acme/widgets/issues/21"}); err != nil {
		r.t.Fatalf("Propose: %v", err)
	}
	return filepath.Join(r.svc.Home, "chat", "work", "questions", "campaign-w1-ab12-1.json")
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func TestInterruptingAWorkingOrWaitingChatPressesEscapeOnceAndAnIdleOneNothing(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		for _, tc := range []struct {
			name, state, reason string
			asks, did           bool
		}{
			{"working", "working", "", false, true},
			{"waiting on permission", "needs_you", "permission", false, true},
			{"waiting on a question", "working", "", true, true},
			{"idle", "needs_you", "stop", false, false},
		} {
			t.Run(harness+" "+tc.name, func(t *testing.T) {
				r := newTurnRig(t, harness)
				k := r.recordTmux()
				r.setState(tc.state, tc.reason)
				id := ""
				if tc.asks {
					id = r.askQuestion()
				}
				interrupted(t, r.interrupt(), http.StatusOK, tc.did)
				r.escapes(k, map[bool]int{true: 1, false: 0}[tc.did])
				if tc.asks {
					if got := r.answerState(id); got != "withdrawn" {
						t.Errorf("its question reads %q, want withdrawn", got)
					}
				}
			})
		}
	}
}

func TestInterruptingKeepsTheChatRunningInItsSessionAndAnUnansweredProposalOpen(t *testing.T) {
	r := newTurnRig(t, "claude")
	k := r.recordTmux()
	r.setState("working", "")
	before, _ := r.fc.session()
	id := r.askQuestion()
	proposal := r.proposal()
	interrupted(t, r.interrupt(), http.StatusOK, true)
	r.escapes(k, 1)
	if got := r.answerState(id); got != "withdrawn" {
		t.Errorf("its question reads %q, want withdrawn", got)
	}
	if !exists(proposal) {
		t.Errorf("the unanswered proposal was withdrawn: the campaign runner owns it")
	}
	after, running := r.fc.session()
	if !running || after.PaneID != before.PaneID || after.Name != before.Name {
		t.Errorf("session after = %+v running %v, want the same running session as %+v", after, running, before)
	}
}

func TestInterruptingDoesNotRewriteTheBriefAsTheQuestionsWithdraw(t *testing.T) {
	r := newTurnRig(t, "claude")
	r.recordTmux()
	r.setState("working", "")
	r.askQuestion()
	r.fc.renderBrief(r.fc.tickInputs())
	path := filepath.Join(r.svc.Home, "chat", "work", briefFile)
	before, err := os.ReadFile(path)
	if err != nil || len(before) == 0 {
		t.Fatalf("no brief to compare: %v", err)
	}
	interrupted(t, r.interrupt(), http.StatusOK, true)
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("the brief was rewritten by the interrupt:\nbefore %q\nafter  %q", before, after)
	}
}

func TestInterruptingAChatThatIsStoppedRefreshingOrNotStartedIsRefusedAndPressesNothing(t *testing.T) {
	cases := map[string]func(t *testing.T) *turnRig{
		"stopped": func(t *testing.T) *turnRig {
			r := newTurnRig(t, "claude")
			sess, _ := r.fc.session()
			if out, err := exec.Command(r.svc.Tmux, "-S", r.svc.Socket, "kill-session", "-t", "="+sess.Name+":").CombinedOutput(); err != nil {
				t.Fatalf("kill-session: %v: %s", err, out)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := r.svc.Host.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			return r
		},
		"refreshing": func(t *testing.T) *turnRig {
			r := newTurnRig(t, "claude")
			r.setState("working", "")
			r.fc.resetting.Store(true)
			t.Cleanup(func() { r.fc.resetting.Store(false) })
			return r
		},
		"not started": func(t *testing.T) *turnRig {
			svc, fc := hostedChat(t, "claude")
			record(t, svc, fc, "claude")
			return &turnRig{t: t, svc: svc, fc: fc}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			r := setup(t)
			k := r.recordTmux()
			interrupted(t, r.interrupt(), http.StatusConflict, false)
			if calls := k.changes(); len(calls) != 0 {
				t.Errorf("pane changes = %q, want none", calls)
			}
		})
	}
}

func TestInterruptingWhenTheEscapeCannotBePressedFailsAndLeavesTheQuestionsOpen(t *testing.T) {
	r := newTurnRig(t, "claude")
	k := r.recordTmux()
	r.setState("working", "")
	id := r.askQuestion()
	k.failKeys(t)
	rec := r.interrupt()
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "could not interrupt") {
		t.Fatalf("interrupt = %d %s, want 502 \"could not interrupt: …\"", rec.Code, rec.Body)
	}
	if got := r.answerState(id); got == "withdrawn" {
		t.Errorf("a question was withdrawn though no Escape was pressed")
	}
}

func interruptedLine(t *testing.T, id string) string {
	return claudeLine(t, "user", id, "[Request interrupted by user]")
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func TestAnInterruptedTurnShowsInTheChatAndItsHistoryOnce(t *testing.T) {
	r := newTurnRig(t, "claude")
	path := claudeTranscriptPath(t, r)
	appendLine(t, path, interruptedLine(t, "33333333-3333-4333-8333-333333333333"))
	for i := 0; i < 2; i++ {
		snap, err := r.fc.refresh(true)
		if err != nil {
			t.Fatal(err)
		}
		if n := countInterrupted(snap.Messages); n != 1 {
			t.Fatalf("refresh %d: %d interrupted entries in %+v, want 1", i+1, n, snap.Messages)
		}
		if last := snap.Messages[len(snap.Messages)-1]; last.Kind != "interrupted" || last.Text != "Interrupted" {
			t.Fatalf("refresh %d: last entry = %+v, want the interrupted one with the text Interrupted", i+1, last)
		}
		for _, m := range snap.Messages {
			if strings.Contains(m.Text, "Request interrupted") {
				t.Errorf("refresh %d: the harness's raw line shows as %+v", i+1, m)
			}
		}
	}
	got := r.fc.recent()
	if countInterrupted(got) != 1 || got[len(got)-1].Kind != "interrupted" {
		t.Errorf("history = %+v, want one interrupted entry, last", got)
	}
}

func countInterrupted(ms []Message) int {
	n := 0
	for _, m := range ms {
		if m.Kind == "interrupted" {
			n++
		}
	}
	return n
}

func TestAnImageOrServerNoteThatLooksBracketedStaysTheEngineersMessage(t *testing.T) {
	r := newTurnRig(t, "claude")
	path := claudeTranscriptPath(t, r)
	appendLine(t, path, claudeLine(t, "user", "44444444-4444-4444-8444-444444444444", "[Image #1] what is this?"))
	snap, err := r.fc.refresh(true)
	if err != nil {
		t.Fatal(err)
	}
	last := snap.Messages[len(snap.Messages)-1]
	if last.Kind != "message" || last.Role != "user" || last.Text != "[Image #1] what is this?" {
		t.Errorf("last = %+v, want the engineer's message unchanged", last)
	}
}

// claudeTranscriptPath is the transcript newTurnRig made the pane report.
func claudeTranscriptPath(t *testing.T, r *turnRig) string {
	t.Helper()
	out, err := exec.Command(r.svc.Tmux, "-S", r.svc.Socket, "show", "-p", "-v", "-t", "="+sessionhost.ChatSession("work")+":", sessionhost.Prefix+"transcript").CombinedOutput()
	if err != nil {
		t.Fatalf("reading the transcript option: %v: %s", err, out)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 3 {
		t.Fatalf("transcript option = %q", out)
	}
	return fields[2]
}
