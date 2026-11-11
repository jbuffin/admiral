package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A refusal leaves one guard_denied line under the home in ADMIRAL_REPORT,
// whatever ADMIRAL_HOME says, with nothing of the command.
func TestGuardRecordsTheRefusalUnderTheReportHome(t *testing.T) {
	home, other := t.TempDir(), t.TempDir()
	t.Setenv("ADMIRAL_AUTOPILOT", "1")
	t.Setenv("ADMIRAL_SHIP", "saltpaw-ii")
	t.Setenv("ADMIRAL_REPORT", filepath.Join(home, "reports", "saltpaw-ii.json"))
	t.Setenv("ADMIRAL_HOME", other)
	payload := `{"tool_name":"Bash","tool_input":{"command":"gh pr review 12 --approve -b xyzzy-secret"},"cwd":"/"}`
	var errb bytes.Buffer
	if err := Guard([]string{"pretooluse"}, IO{In: strings.NewReader(payload), Out: &bytes.Buffer{}, Err: &errb}); err == nil {
		t.Fatal("Guard allowed an approving review on an autopilot ship")
	}
	files, _ := filepath.Glob(filepath.Join(home, "outcomes", "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("outcome files = %v, want one month file", files)
	}
	raw, _ := os.ReadFile(files[0])
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &rec); err != nil {
		t.Fatalf("the log is not one JSON line: %s", raw)
	}
	if rec["event"] != "guard_denied" || rec["ship"] != "saltpaw-ii" || rec["tool"] != "Bash" || rec["rule"] != "gh_pr_review" {
		t.Errorf("record = %v, want guard_denied for saltpaw-ii, tool Bash, rule gh_pr_review", rec)
	}
	if strings.Contains(string(raw), "xyzzy") || strings.Contains(string(raw), "pr review") {
		t.Errorf("the log holds text of the command: %s", raw)
	}
	if others, _ := filepath.Glob(filepath.Join(other, "outcomes", "*")); len(others) != 0 {
		t.Errorf("ADMIRAL_HOME got a record: %v", others)
	}
}

// With nowhere to record, the refusal stands and stderr carries only the reason.
func TestGuardRefusesWhenItCannotRecord(t *testing.T) {
	blocked := t.TempDir()
	if err := os.WriteFile(filepath.Join(blocked, "outcomes"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	for name, env := range map[string][2]string{
		"an outcomes directory that cannot be written": {"mistral", filepath.Join(blocked, "reports", "mistral.json")},
		"no home": {"mistral", ""},
	} {
		t.Setenv("ADMIRAL_AUTOPILOT", "1")
		t.Setenv("ADMIRAL_SHIP", env[0])
		t.Setenv("ADMIRAL_REPORT", env[1])
		t.Setenv("ADMIRAL_HOME", "")
		var errb bytes.Buffer
		err := Guard([]string{"pretooluse"}, IO{In: strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"gh pr review 1 --approve"}}`), Out: &bytes.Buffer{}, Err: &errb})
		var denied *GuardDenied
		if !errors.As(err, &denied) || strings.TrimSuffix(errb.String(), "\n") != denied.Reason {
			t.Errorf("%s: Guard = %v, stderr %q, want the refusal and its reason alone", name, err, errb.String())
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".admiral")); err == nil {
		t.Error("the guard created ~/.admiral")
	}
}
