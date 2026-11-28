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

	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// stuckRig is a real tmux with one ship and one crew boat that were launched long ago and never ran a hook.
type stuckRig struct {
	s        *Service
	tmux     string
	sock     string
	typed    string // the file the ship's pane copies its input to
	windowID string
}

func newStuckRig(t *testing.T) *stuckRig {
	t.Helper()
	if testing.Short() {
		t.Skip("needs tmux: skipped under -short")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	tmp, err := os.MkdirTemp("/tmp", "ns")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(tmp, "tmux.sock")
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", sock, "kill-server").Run()
		_ = os.RemoveAll(tmp)
	})
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(tmux, append([]string{"-S", sock}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v\n%s", args, err, out)
		}
	}
	typed := filepath.Join(tmp, "typed")
	launched := time.Now().Add(-5 * NoHookWait).UTC().Format(time.RFC3339)
	run("new-session", "-d", "-s", "stuck", "sh", "-c", "cat > "+typed)
	run("set-option", "-t", "=stuck:", "@admiral-name", "Stuck")
	run("set-option", "-t", "=stuck:", "@admiral-launched", launched)
	run("new-window", "-t", "=stuck:", "-n", "mate", "sleep", "300")
	run("set-option", "-w", "-t", "=stuck:mate", "@admiral-crew-name", "mate")
	run("set-option", "-w", "-t", "=stuck:mate", "@admiral-harness", "claude")
	run("set-option", "-w", "-t", "=stuck:mate", "@admiral-launched", launched)

	h := sessionhost.New(tmux, sock)
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go h.Run(ctx)
	select {
	case <-h.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("the host never read the sessions")
	}
	if err := h.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	r := &stuckRig{s: &Service{Host: h, Hub: server.NewHub(), Launcher: nil}, tmux: tmux, sock: sock, typed: typed}
	boats, ok := r.s.Crew("stuck")
	if !ok || len(boats) != 1 {
		t.Fatalf("crew = %+v (%v), want one boat", boats, ok)
	}
	r.windowID = boats[0].WindowID
	return r
}

func (r *stuckRig) post(handler http.HandlerFunc, path string, body string, boat string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.SetPathValue("session", "stuck")
	if boat != "" {
		req.SetPathValue("name", boat)
	}
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestEveryCrewReaderSeesAStuckBoatAsNotStartedUntilTheEngineersKey(t *testing.T) {
	t.Parallel()
	r := newStuckRig(t)

	boats, _ := r.s.Crew("stuck")
	if boats[0].State != sessionhost.CrewNeedsYou || boats[0].Reason != "not_started" {
		t.Fatalf("Service.Crew: state %q reason %q, want needs_you not_started", boats[0].State, boats[0].Reason)
	}
	raw := r.s.Host.Sessions()[0].Crew[0]
	if st, reason := CrewStatus(raw, time.Now()); st != sessionhost.CrewNeedsYou || reason != "not_started" {
		t.Fatalf("CrewStatus: %q %q, want needs_you not_started", st, reason)
	}

	c, err := r.s.CrewInputSeen("stuck", r.windowID)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != sessionhost.CrewUnderway || c.LastActivity.IsZero() {
		t.Errorf("after the key: state %q activity %v, want underway with an activity stamp", c.State, c.LastActivity)
	}
	boats, _ = r.s.Crew("stuck")
	if boats[0].State != sessionhost.CrewUnderway {
		t.Errorf("Service.Crew after the key: state %q, want underway", boats[0].State)
	}
}

func TestMessageBoxAndInterruptSendNoKeysToANotStartedShipOrBoat(t *testing.T) {
	t.Parallel()
	r := newStuckRig(t)
	for _, tc := range []struct{ what, boat string }{{"ship", ""}, {"boat", "mate"}} {
		for name, call := range map[string]func() *httptest.ResponseRecorder{
			"send": func() *httptest.ResponseRecorder {
				return r.post(r.s.serveSendInput, "/", `{"text":"start now"}`, tc.boat)
			},
			"interrupt": func() *httptest.ResponseRecorder { return r.post(r.s.serveInterrupt, "/", `{}`, tc.boat) },
		} {
			w := call()
			if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Terminal tab") {
				t.Errorf("%s to the not-started %s: %d %s, want 409 pointing at the Terminal tab", name, tc.what, w.Code, w.Body)
			}
		}
	}
	time.Sleep(300 * time.Millisecond)
	if b, _ := os.ReadFile(r.typed); len(b) != 0 {
		t.Errorf("the ship's pane received %q, want nothing", b)
	}
}
