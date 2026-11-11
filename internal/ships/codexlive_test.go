package ships

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

	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/server"
)

// codexAsking makes tailService's ship a Codex ship whose pane echoes what is typed into it and shows the bell.
func codexAsking(t *testing.T, s *Service, harness, reason string) (typed func() string) {
	t.Helper()
	tmux, _ := exec.LookPath("tmux")
	sock := filepath.Join(os.Getenv("TMUX_TMPDIR"), "tmux.sock")
	s.Launcher.Tmux, s.Launcher.Socket = tmux, sock // the route reads and types through the launcher's tmux
	s.Hub = server.NewHub()
	run := func(args ...string) string {
		out, err := exec.Command(tmux, append([]string{"-S", sock}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("set-option", "-t", "alpha", "@admiral-harness", harness)
	run("respawn-pane", "-k", "-t", "alpha", "cat")
	pane := run("list-panes", "-t", "alpha", "-F", "#{pane_id}")
	ctx := context.Background()
	if err := s.Host.SetPaneNeedsYou(ctx, pane, reason); err != nil {
		t.Fatal(err)
	}
	if err := s.Host.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	return func() string { return run("capture-pane", "-p", "-t", "alpha") }
}

func answer(s *Service, body string) int {
	code, _ := answerWhy(s, body)
	return code
}

func answerWhy(s *Service, body string) (int, string) {
	mux := http.NewServeMux()
	s.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/alpha/answer", strings.NewReader(body)))
	return rec.Code, rec.Body.String()
}

func liveQuestion(id string, options int, multi bool) conversation.Item {
	q := &conversation.Question{Text: "Pick a colour", Pending: true, MultiSelect: multi}
	for i := 0; i < options; i++ {
		q.Options = append(q.Options, conversation.Option{Label: string(rune('A' + i))})
	}
	return conversation.Item{ID: id, Kind: conversation.KindQuestion, Question: q}
}

func TestCodexQuestionIsAnsweredOnlyWhileBellMeetsTheObserversPendingQuestion(t *testing.T) {
	const body = `{"card":"question","reason":"bell","item":"call_q1#0","option":2}`

	t.Run("a bell with the observer's matching question sends the digit", func(t *testing.T) {
		s, _ := tailService(t)
		typed := codexAsking(t, s, "codex", "bell")
		s.Questions = func(session, crew string) []conversation.Item {
			return []conversation.Item{liveQuestion("call_q1#0", 2, false)}
		}
		if code, why := answerWhy(s, body); code != http.StatusOK {
			t.Fatalf("POST answer = %d %s, want 200", code, why)
		}
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(typed(), "2") && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if !strings.Contains(typed(), "2") {
			t.Errorf("pane shows %q, want the digit 2 typed into it", typed())
		}
	})

	for name, qs := range map[string][]conversation.Item{
		"a bell and no pending question":         nil,
		"a bell and another call's question":     {liveQuestion("call_other#0", 2, false)},
		"a bell and a multi-select question":     {liveQuestion("call_q1#0", 2, true)},
		"a bell and an option past the last one": {liveQuestion("call_q1#0", 1, false)},
		"a bell and a call that holds several":   {liveQuestion("call_q1#0", 2, false), liveQuestion("call_q1#1", 2, false)},
	} {
		t.Run(name+" sends nothing", func(t *testing.T) {
			s, _ := tailService(t)
			typed := codexAsking(t, s, "codex", "bell")
			s.Questions = func(session, crew string) []conversation.Item { return qs }
			if code := answer(s, body); code != http.StatusConflict {
				t.Errorf("POST answer = %d, want 409", code)
			}
			if got := typed(); strings.ContainsAny(got, "12") {
				t.Errorf("pane shows %q, want nothing typed", got)
			}
		})
	}

	t.Run("a bell while an approval waits too sends nothing: the menu on screen may be the approval's", func(t *testing.T) {
		s, _ := tailService(t)
		typed := codexAsking(t, s, "codex", "bell")
		s.Questions = func(session, crew string) []conversation.Item {
			return []conversation.Item{liveQuestion("call_q1#0", 2, false)}
		}
		s.Approving = func(session, crew string) bool { return true }
		if code, why := answerWhy(s, body); code != http.StatusConflict || !strings.Contains(why, "approval") {
			t.Errorf("POST answer = %d %s, want 409 naming the approval", code, why)
		}
		if got := typed(); strings.Contains(got, "2") {
			t.Errorf("pane shows %q, want nothing typed", got)
		}
	})

	t.Run("a Codex pane that is not on the bell takes no question answer", func(t *testing.T) {
		s, _ := tailService(t)
		typed := codexAsking(t, s, "codex", "question")
		s.Questions = func(session, crew string) []conversation.Item {
			return []conversation.Item{liveQuestion("call_q1#0", 2, false)}
		}
		if code, why := answerWhy(s, `{"card":"question","reason":"question","item":"call_q1#0","option":2}`); code != http.StatusConflict || !strings.Contains(why, "no longer") {
			t.Errorf("POST answer with reason question = %d %s, want 409 no longer asking that: Codex asks with a bell, never a question", code, why)
		}
		if got := typed(); strings.Contains(got, "2") {
			t.Errorf("pane shows %q, want nothing typed", got)
		}
	})

	t.Run("a Claude pane on a bell takes no question answer", func(t *testing.T) {
		s, _ := tailService(t)
		typed := codexAsking(t, s, "claude", "bell")
		s.Questions = func(session, crew string) []conversation.Item {
			return []conversation.Item{liveQuestion("call_q1#0", 2, false)}
		}
		if code := answer(s, body); code != http.StatusConflict {
			t.Errorf("POST answer = %d, want 409: a bell proves nothing for Claude", code)
		}
		if got := typed(); strings.Contains(got, "2") {
			t.Errorf("pane shows %q, want nothing typed", got)
		}
	})
}
