package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// A Codex chat runs in its own CODEX_HOME: its transcripts are read there, and only the login is the engineer's.

const codexChatFleets = `{"version":1,"fleets":{
	"b":{"name":"B","claude":{},"codex":{"env":{"CODEX_HOME":"/srv/fake-codex-identity"}},"chat":{"harness":"codex","model":"gpt-6.1-sol"}}}}`

func codexRollout(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../conversation/testdata/codex-sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeAt(t *testing.T, path string, b []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func codexChatHome(t *testing.T, s *Service) string {
	t.Helper()
	h := launcher.ChatCodexHome(s.Home, "b")
	if h == "" {
		t.Fatal("launcher.ChatCodexHome is empty")
	}
	return h
}

func TestACodexChatsLoginDirIsTheEngineersAndNotItsOwnHome(t *testing.T) {
	s := harnessService(t, codexChatFleets, fakeProbes{})
	want := config.IdentityDir("codex", map[string]string{"CODEX_HOME": "/srv/fake-codex-identity"})
	got := s.Login(sessionhost.ChatSession("b"))
	if want == "" || got != want {
		t.Errorf("a codex chat's login dir = %q, want the engineer's %q", got, want)
	}
	if got == codexChatHome(t, s) {
		t.Errorf("the login dir is the chat's own home %q: the login stays with the engineer", got)
	}
}

func servedFromFile(t *testing.T, s *Service, key, path string) []string {
	t.Helper()
	fc := s.chat(key)
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.transcriptChangedLocked("1 start " + path)
	if fc.st.rd == nil {
		t.Fatal("no reader after a transcript option")
	}
	fc.followLocked()
	var out []string
	for _, m := range fc.st.served() {
		out = append(out, m.Role+": "+m.Text)
	}
	return out
}

func TestTheCodexChatReaderReadsRolloutsFromTheChatHomeOnly(t *testing.T) {
	s := harnessService(t, codexChatFleets, fakeProbes{})
	home := codexChatHome(t, s)
	own := writeAt(t, filepath.Join(home, "sessions", "2026", "11", "12", "rollout-own.jsonl"), codexRollout(t))
	if got := servedFromFile(t, s, "b", own); len(got) == 0 {
		t.Errorf("a rollout under the chat's home serves nothing")
	}

	// A path under the engineer's own sessions/ is not the chat's: it is refused, as a path outside the config dir is.
	t.Setenv("CODEX_HOME", t.TempDir())
	theirs := writeAt(t, filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "2026", "rollout-theirs.jsonl"), codexRollout(t))
	s2 := harnessService(t, codexChatFleets, fakeProbes{})
	codexChatHome(t, s2)
	if got := servedFromFile(t, s2, "b", theirs); len(got) != 0 {
		t.Errorf("a rollout in the engineer's sessions/ serves %v, want nothing", got)
	}
}

func TestACodexResetRecordsTheChatHomeRolloutsUsageUnderCodex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outcomes")
	w := outcomes.New(dir, t.Logf)
	t.Cleanup(w.Close)
	s := harnessService(t, codexChatFleets, fakeProbes{})
	s.Outcomes = w
	home := codexChatHome(t, s)
	b, err := os.ReadFile("../usage/testdata/codex-recorded.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	rollout := writeAt(t, filepath.Join(home, "sessions", "2026", "11", "12", "rollout-own.jsonl"), b)

	s.chat("b").recordReset(rollout, home, "b", "")

	var rec map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for rec == nil && time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		for _, f := range files {
			data, _ := os.ReadFile(f)
			for _, l := range strings.Split(string(data), "\n") {
				var r map[string]any
				if json.Unmarshal([]byte(l), &r) == nil && r["event"] == "usage" {
					rec = r
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec == nil {
		t.Fatal("no usage record within 5 s")
	}
	if rec["harness"] != "codex" {
		t.Errorf("usage harness = %v, want codex", rec["harness"])
	}
	raw, _ := json.Marshal(rec["tokens"])
	if !strings.Contains(string(raw), "20281") && !strings.Contains(string(raw), "20286") {
		t.Errorf("usage tokens = %s, want the rollout's token_count totals (20281 in)", raw)
	}
}
