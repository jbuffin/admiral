package ships

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/state"
)

// tailService runs a real tmux with one ship, "alpha", and a tail source the test changes.
func tailService(t *testing.T) (*Service, func(string)) {
	t.Helper()
	if testing.Short() {
		t.Skip("needs tmux: skipped under -short")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	dir, err := os.MkdirTemp("/tmp", "st")
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
	for _, args := range [][]string{
		{"new-session", "-d", "-s", "alpha", "sleep 300"},
		{"set-option", "-t", "alpha", "@admiral-name", "alpha"},
		{"set-option", "-t", "alpha", "@admiral-harness", "claude"},
	} {
		if out, err := exec.Command(tmux, append([]string{"-S", sock}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
	}
	if err := h.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	tails := map[string]string{}
	st, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Host: h, State: st,
		Launcher: &launcher.Launcher{Home: dir, Config: func() *config.Config { return &config.Config{} }},
		Tail:     func(session string) string { mu.Lock(); defer mu.Unlock(); return tails[session] }}
	return s, func(v string) { mu.Lock(); defer mu.Unlock(); tails["alpha"] = v }
}

func wireShips(t *testing.T, s *Service) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(s.message())
	if err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Type  string
		Ships []map[string]any
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "ships" {
		t.Fatalf("message type = %q, want ships", msg.Type)
	}
	return msg.Ships
}

func TestShipsMessageCarriesTheTailAndItChangesLive(t *testing.T) {
	s, setTail := tailService(t)

	// A ship with nothing readable still has the field, as "".
	before := s.signature()
	ships := wireShips(t, s)
	if len(ships) != 1 {
		t.Fatalf("ships message holds %d ships, want 1", len(ships))
	}
	if tail, ok := ships[0]["tail"]; !ok || tail != "" {
		t.Fatalf("a ship with no conversation has tail %v (present: %v), want the empty string", tail, ok)
	}

	setTail("Run the tests")
	if got := wireShips(t, s)[0]["tail"]; got != "Run the tests" {
		t.Errorf("ships message tail after the conversation changed = %v, want %q", got, "Run the tests")
	}
	if s.signature() == before {
		t.Errorf("the signature did not change with the tail, so no ships message would be broadcast")
	}
	for _, sh := range s.List() {
		if sh.Tail != "Run the tests" {
			t.Errorf("List tail = %q, want %q", sh.Tail, "Run the tests")
		}
	}
	if !strings.Contains(s.signature(), "Run the tests") {
		t.Errorf("signature = %q, want it to hold the tail", s.signature())
	}
}
