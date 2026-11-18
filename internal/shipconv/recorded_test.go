package shipconv

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// RecordedConversation reads a ship that is gone through the binding recorded when it ended, under ADR 0019's checks.

func recorded(t *testing.T, f *fixture, raw, startCmd, query string) (int, pageJSON) {
	t.Helper()
	status, body := f.svc.RecordedConversation("claude", raw, startCmd, f.cfg, query)
	var p pageJSON
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	return status, p
}

func TestRecordedConversationReadsABindingTheStartCommandBacks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	appendTo(t, f.path("zz"), line(1))
	start := "'/bin/claude' '--session-id' '" + sid + "' '--' 'go'"
	status, p := recorded(t, f, "1700000000000000000 launch "+f.path("zz"), start, "")
	if status != http.StatusOK || p.State != "ok" || len(p.Items) == 0 {
		t.Fatalf("recorded read = %d %+v, want the conversation", status, p)
	}
}

func TestRecordedConversationRefusesABindingThatFailsTheChecks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	appendTo(t, f.path("zz"), line(1))
	start := "'/bin/claude' '--session-id' '" + sid + "' '--' 'go'"
	other := "'/bin/claude' '--session-id' '11111111-2222-4333-8444-555555555555' '--' 'go'"
	for name, c := range map[string]struct{ raw, start string }{
		"relative path":        {"1700000000000000000 launch projects/zz/" + sid + ".jsonl", start},
		"not a launch source":  {"1700000000000000000 hook " + f.path("zz"), start},
		"another launched id":  {"1700000000000000000 launch " + f.path("zz"), other},
		"no start command":     {"1700000000000000000 launch " + f.path("zz"), ""},
		"too few option parts": {f.path("zz"), start},
	} {
		if status, p := recorded(t, f, c.raw, c.start, ""); status != http.StatusOK || p.State != "none" || len(p.Items) != 0 {
			t.Errorf("%s: recorded read = %d %+v, want the none page", name, status, p)
		}
	}
}

func TestRecordedConversationRefusesATranscriptThatIsASymlink(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	outside := filepath.Join(t.TempDir(), sid+".jsonl")
	if err := os.WriteFile(outside, []byte(line(1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, f.path("zz")); err != nil {
		t.Fatal(err)
	}
	start := "'/bin/claude' '--session-id' '" + sid + "' '--' 'go'"
	if status, p := recorded(t, f, "1700000000000000000 launch "+f.path("zz"), start, ""); status != http.StatusOK || p.State != "unreadable" || len(p.Items) != 0 {
		t.Errorf("recorded read of a symlink = %d %+v, want unreadable", status, p)
	}
}

func TestRecordedConversationRejectsABadBefore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	appendTo(t, f.path("zz"), line(1))
	start := "'/bin/claude' '--session-id' '" + sid + "' '--' 'go'"
	if status, _ := recorded(t, f, "1700000000000000000 launch "+f.path("zz"), start, "before=nope"); status != http.StatusBadRequest {
		t.Errorf("an unknown before = %d, want 400", status)
	}
}
