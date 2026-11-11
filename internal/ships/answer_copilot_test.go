package ships

// Answering a Copilot pane's permission or question from the card sends Copilot's keys, replayed on a real tmux pane.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/state"
)

type answerPane struct {
	t     *testing.T
	s     *Service
	mux   *http.ServeMux
	tmux  string
	sock  string
	stdin string
	pane  string
}

// newAnswerPane runs ship "alpha" of the given harness in a raw-mode pane that records every byte it is sent.
func newAnswerPane(t *testing.T, harness string, questions []conversation.Item) *answerPane {
	t.Helper()
	if testing.Short() {
		t.Skip("needs tmux: skipped under -short")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	dir, err := os.MkdirTemp("/tmp", "sa")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "tmux.sock")
	t.Setenv("TMUX_TMPDIR", dir)
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", sock, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	h := sessionhost.New(tmux, sock)
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go h.Run(ctx)
	select {
	case <-h.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("the host never read the sessions")
	}
	stdin := filepath.Join(dir, "stdin")
	for _, args := range [][]string{
		{"new-session", "-d", "-s", "alpha", "stty raw -echo; exec cat > " + stdin},
		{"set-option", "-t", "alpha", "@admiral-name", "alpha"},
		{"set-option", "-t", "alpha", "@admiral-harness", harness},
	} {
		if out, err := exec.Command(tmux, append([]string{"-S", sock}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
	}
	if err := h.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	pane, err := exec.Command(tmux, "-S", sock, "list-panes", "-t", "alpha", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Host: h, State: st,
		Launcher:  &launcher.Launcher{Home: dir, Tmux: tmux, Socket: sock, Config: func() *config.Config { return &config.Config{} }},
		Questions: func(string, string) []conversation.Item { return questions }}
	mux := http.NewServeMux()
	s.Routes(mux)
	time.Sleep(300 * time.Millisecond) // the shell's stty must finish before keys arrive
	return &answerPane{t: t, s: s, mux: mux, tmux: tmux, sock: sock, stdin: stdin, pane: strings.TrimSpace(string(pane))}
}

func (p *answerPane) needsYou(reason string) {
	p.t.Helper()
	if err := p.s.Host.SetPaneNeedsYou(context.Background(), p.pane, reason); err != nil {
		p.t.Fatal(err)
	}
	if err := p.s.Host.Sync(context.Background()); err != nil {
		p.t.Fatal(err)
	}
}

func (p *answerPane) reason() string {
	p.t.Helper()
	out, err := exec.Command(p.tmux, "-S", p.sock, "show-options", "-p", "-v", "-t", p.pane, "@admiral-reason").Output()
	if err != nil {
		p.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func (p *answerPane) post(body string) (int, string) {
	rec := httptest.NewRecorder()
	p.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/alpha/answer", strings.NewReader(body)))
	return rec.Code, rec.Body.String()
}

// sent is what the pane has read so far, waiting for want when it is not empty.
func (p *answerPane) sent(want string) string {
	p.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, _ := os.ReadFile(p.stdin)
		if want == "" {
			time.Sleep(300 * time.Millisecond)
			b, _ = os.ReadFile(p.stdin)
			return string(b)
		}
		if string(b) == want || time.Now().After(deadline) {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func askItem(id string, pending bool, labels ...string) conversation.Item {
	q := &conversation.Question{Text: "Which one?", Pending: pending}
	for _, l := range labels {
		q.Options = append(q.Options, conversation.Option{Label: l})
	}
	return conversation.Item{ID: id, Kind: "question", Question: q}
}

const downEnter = "\x1b[B\r"

// A Claude or Codex pane gets no card for a Copilot notification reason.
func checkNotificationReasonsAreCopilotsAlone(t *testing.T) {
	t.Helper()
	for _, harness := range []string{"claude", "codex"} {
		p := newAnswerPane(t, harness, []conversation.Item{askItem("q", true, "Red")})
		p.needsYou("notification:permission_prompt")
		code, msg := p.post(`{"card":"permission","reason":"notification:permission_prompt","choice":"allow"}`)
		if code != http.StatusConflict {
			t.Errorf("a %s pane on notification:permission_prompt = %d %s, want 409", harness, code, msg)
		}
		if got := p.sent(""); got != "" {
			t.Errorf("a %s pane was sent %q, want nothing", harness, got)
		}
	}
}

func TestCopilotPermissionAllowSendsYesAndDenySendsEscThenStops(t *testing.T) {
	defer checkNotificationReasonsAreCopilotsAlone(t)
	p := newAnswerPane(t, "copilot", nil)
	p.needsYou("notification:permission_prompt")
	if code, body := p.post(`{"card":"permission","reason":"notification:permission_prompt","choice":"allow"}`); code != http.StatusOK {
		t.Fatalf("Allow on a Copilot permission prompt = %d %s, want 200", code, body)
	}
	if got := p.sent("1"); got != "1" {
		t.Errorf("Allow sent %q, want %q", got, "1")
	}
	p.needsYou("notification:permission_prompt")
	if code, body := p.post(`{"card":"permission","reason":"notification:permission_prompt","choice":"deny"}`); code != http.StatusOK {
		t.Fatalf("Deny on a Copilot permission prompt = %d %s, want 200", code, body)
	}
	if got := p.sent("1\x1b"); got != "1\x1b" {
		t.Errorf("Deny sent %q after Allow's %q, want one more Esc", got, "1")
	}
	// Copilot fires neither postToolUse nor agentStop after a deny, so no hook leaves needs_you.
	if got := p.reason(); got != "stop" {
		t.Errorf("the pane's reason after Deny = %q, want stop", got)
	}
}

func TestCopilotQuestionOptionTwoSendsDownThenEnterForTheNewestPendingQuestion(t *testing.T) {
	p := newAnswerPane(t, "copilot", []conversation.Item{
		askItem("stale", true, "Old"), askItem("new", true, "Red", "Green", "Blue")})
	p.needsYou("question")
	if code, body := p.post(`{"card":"question","reason":"question","item":"stale","option":1}`); code != http.StatusConflict {
		t.Errorf("an answer to the stale question = %d %s, want 409", code, body)
	}
	if got := p.sent(""); got != "" {
		t.Errorf("the stale answer sent %q, want nothing", got)
	}
	if code, body := p.post(`{"card":"question","reason":"question","item":"new","option":2}`); code != http.StatusOK {
		t.Fatalf("option 2 of the newest question = %d %s, want 200", code, body)
	}
	if got := p.sent(downEnter); got != downEnter {
		t.Errorf("option 2 sent %q, want Down then Enter %q", got, downEnter)
	}
}

func TestCopilotElicitationDialogTakesTheQuestionCardOnlyWithASingleChoiceBehindIt(t *testing.T) {
	const body = `{"card":"question","reason":"notification:elicitation_dialog","item":"q","option":1}`
	backed := newAnswerPane(t, "copilot", []conversation.Item{askItem("q", true, "Red", "Green")})
	backed.needsYou("notification:elicitation_dialog")
	if code, msg := backed.post(body); code != http.StatusOK {
		t.Errorf("elicitation_dialog with a pending single-choice question = %d %s, want 200", code, msg)
	}
	if got := backed.sent("\r"); got != "\r" {
		t.Errorf("option 1 sent %q, want Enter", got)
	}
	for name, qs := range map[string][]conversation.Item{
		"no question":          nil,
		"a several-field form": {askItem("q", true)},
		"an answered question": {askItem("q", false, "Red", "Green")},
	} {
		p := newAnswerPane(t, "copilot", qs)
		p.needsYou("notification:elicitation_dialog")
		if code, msg := p.post(body); code != http.StatusConflict {
			t.Errorf("elicitation_dialog with %s = %d %s, want 409", name, code, msg)
		}
		if got := p.sent(""); got != "" {
			t.Errorf("elicitation_dialog with %s sent %q, want nothing", name, got)
		}
	}
}

func TestCopilotQuestionFormWithNoOptionsGetsNoKeys(t *testing.T) {
	p := newAnswerPane(t, "copilot", []conversation.Item{askItem("q", true)})
	p.needsYou("question")
	if code, msg := p.post(`{"card":"question","reason":"question","item":"q","option":1}`); code != http.StatusConflict {
		t.Errorf("an answer to a form with no options = %d %s, want 409", code, msg)
	}
	if got := p.sent(""); got != "" {
		t.Errorf("sent %q, want nothing", got)
	}
}

func TestACodexPaneIsNotCalledAClaudeCodePane(t *testing.T) {
	p := newAnswerPane(t, "codex", nil)
	p.needsYou("permission:Bash")
	code, msg := p.post(`{"card":"permission","reason":"permission:Bash","choice":"allow"}`)
	if code != http.StatusConflict || strings.Contains(msg, "Claude Code") {
		t.Errorf("a Codex pane's answer = %d %s, want 409 with a harness-neutral reason", code, msg)
	}
}
