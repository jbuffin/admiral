package permlog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/permlog"
)

func at(sec string) string { return "2026-11-18T10:00:" + sec + "Z" }

func readBack(t *testing.T, home, ship string) []permlog.Record {
	t.Helper()
	recs, err := permlog.Read(home, ship)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestARecordCarriesTheLaunchItCameInAndItsAnswer(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir() // a checkout with no remote is named by its path
	if err := permlog.Launched(home, "alpha", "claude", "work", dir); err != nil {
		t.Fatal(err)
	}
	call := permlog.ClaudeCall("Bash", json.RawMessage(`{"command":"gh pr view 7"}`), json.RawMessage(`[{"type":"addRules"}]`))
	must(t, permlog.Append(home, "alpha",
		permlog.Event{Ev: "ask", ID: "a1", Harness: "claude", Call: &call},
		permlog.Event{Ev: "ask", ID: "b1", Crew: "scout", Harness: "codex", Call: &permlog.Call{Tool: "Bash", Kind: "shell", Command: "ls"}},
		permlog.Event{Ev: "answer", ID: "a1", Answer: permlog.Allow, By: permlog.Person},
		permlog.Event{Ev: "answer", ID: "b1", Answer: permlog.Deny, By: permlog.Unknown}))
	recs := readBack(t, home, "alpha")
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2: %+v", len(recs), recs)
	}
	a, b := recs[0], recs[1]
	if a.Ship != "alpha" || a.Harness != "claude" || a.Fleet != "work" || a.Repo != dir || a.Launched == "" {
		t.Errorf("the ship's record is %+v", a)
	}
	if a.Call.Kind != "shell" || a.Call.Command != "gh pr view 7" || string(a.Call.Suggestions) != `[{"type":"addRules"}]` {
		t.Errorf("the ship's call is %+v", a.Call)
	}
	if a.Answer != permlog.Allow || a.By != permlog.Person || a.Via != permlog.Terminal {
		t.Errorf("the ship's answer is %s by %s via %s, want a person's allow in the terminal", a.Answer, a.By, a.Via)
	}
	if b.Crew != "scout" || b.Harness != "codex" || b.Answer != permlog.Deny || b.By != permlog.Unknown || b.Via != "" {
		t.Errorf("the crew boat's record is %+v", b)
	}
}

func TestARemoteNamesTheRepoByOwnerAndName(t *testing.T) {
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o700))
	must(t, os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[remote \"upstream\"]\n\turl = https://github.com/other/fork.git\n[remote \"origin\"]\n\turl = git@github.com:Acme/API.git\n"), 0o600))
	if got := permlog.Repo(dir); got != "acme/api" {
		t.Errorf("Repo = %q, want the origin's acme/api", got)
	}
}

func TestACardAnswerBetweenThePromptAndItsAnswerIsTheCards(t *testing.T) {
	call := &permlog.Call{Tool: "Bash", Kind: "shell", Command: "make"}
	recs := permlog.Fold("alpha", []permlog.Event{
		{Ev: "ask", At: at("01"), ID: "ship", Call: call},
		{Ev: "ask", At: at("02"), ID: "boat", Crew: "scout", Call: call},
		{Ev: "card", At: at("03"), Crew: "scout", Answer: permlog.Allow},
		{Ev: "answer", At: at("04"), ID: "boat", Answer: permlog.Allow, By: permlog.Person},
		{Ev: "answer", At: at("05"), ID: "ship", Answer: permlog.Allow, By: permlog.Person},
	})
	if recs[0].Via != permlog.Terminal || recs[1].Via != permlog.Card {
		t.Errorf("the ship's answer came via %q and the boat's via %q, want terminal and card", recs[0].Via, recs[1].Via)
	}
}

func TestACardDenyAnswersAPromptNoHookClosed(t *testing.T) {
	recs := permlog.Fold("alpha", []permlog.Event{
		{Ev: "ask", At: at("01"), ID: "a", Call: &permlog.Call{Tool: "Bash", Kind: "shell"}},
		{Ev: "card", At: at("02"), Answer: permlog.Deny},
	})
	if r := recs[0]; r.Answer != permlog.Deny || r.By != permlog.Person || r.Via != permlog.Card {
		t.Errorf("got %s by %s via %s, want a person's deny via the card", r.Answer, r.By, r.Via)
	}
}

func TestALongCallIsCutToFitTheLine(t *testing.T) {
	home := t.TempDir()
	long := strings.Repeat("x", 3000)
	paths := make([]string, 20)
	for i := range paths {
		paths[i] = long
	}
	call := permlog.Call{Tool: "Bash", Kind: "shell", Command: strings.Repeat("y", 10000), Paths: paths}
	must(t, permlog.Append(home, "alpha", permlog.Event{Ev: "ask", ID: "a", Call: &call}))
	p, _ := permlog.Path(home, "alpha")
	raw, err := os.ReadFile(p)
	must(t, err)
	if len(raw) > 16<<10 {
		t.Fatalf("the line is %d bytes", len(raw))
	}
	recs := readBack(t, home, "alpha")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	c := recs[0].Call
	if c.Tool != "Bash" || len(c.Command) > 4100 || len(c.Paths) != 20 || len(c.Paths[0]) > 520 {
		t.Fatalf("the call kept a %d-byte command and %d paths, the first %d bytes", len(c.Command), len(c.Paths), len(c.Paths[0]))
	}
}

func TestCopilotAnswersSayWhoAnswered(t *testing.T) {
	for _, c := range []struct {
		data string
		ans  permlog.Answer
		by   permlog.By
	}{
		{`{"requestId":"r","result":{"kind":"approved"},"decisionSource":"human_response"}`, permlog.Allow, permlog.Person},
		{`{"requestId":"r","result":{"kind":"denied-interactively-by-user"},"decisionSource":"human_response"}`, permlog.Deny, permlog.Person},
		{`{"requestId":"r","result":{"kind":"approved"},"decisionSource":"assisted_approval"}`, permlog.Allow, permlog.Reviewer},
		{`{"requestId":"r","result":{"kind":"denied-no-approval-rule-and-could-not-request-from-user"},"decisionSource":"unattended_fallback"}`, permlog.Deny, permlog.Unattended},
		// Before Copilot named its sources.
		{`{"requestId":"r","result":{"kind":"approved-for-session","approval":{"kind":"write"}}}`, permlog.Allow, permlog.Person},
		{`{"requestId":"r","result":{"kind":"approved"}}`, permlog.Allow, permlog.Unknown},
	} {
		ev, ok := permlog.CopilotEvent("permission.completed", at("01"), json.RawMessage(c.data))
		if !ok || ev.Answer != c.ans || ev.By != c.by {
			t.Errorf("%s: got %s by %s, want %s by %s", c.data, ev.Answer, ev.By, c.ans, c.by)
		}
	}
}

func TestACopilotShellRequestKeepsItsCommandIdentifiers(t *testing.T) {
	data := `{"requestId":"r1","permissionRequest":{"kind":"shell","toolCallId":"t","fullCommandText":"sleep 2 && gh pr checks 1686","commands":[{"identifier":"sleep"},{"identifier":"gh pr"}],"commandSegments":[{"workingDirectory":"/w"}],"possiblePaths":[],"possibleUrls":[]}}`
	ev, ok := permlog.CopilotEvent("permission.requested", at("01"), json.RawMessage(data))
	if !ok || ev.Ev != "ask" || ev.ID != "r1" || ev.Harness != "copilot" {
		t.Fatalf("got %+v", ev)
	}
	if c := ev.Call; c.Kind != "shell" || c.Command != "sleep 2 && gh pr checks 1686" || strings.Join(c.Identifiers, ",") != "sleep,gh pr" || c.Cwd != "/w" {
		t.Errorf("call %+v", c)
	}
}

func frames(t *testing.T, tr *permlog.CodexTracker, lines ...string) []permlog.Event {
	t.Helper()
	var out []permlog.Event
	for _, l := range lines {
		out = append(out, tr.Frame([]byte(l))...)
	}
	return out
}

func TestACodexApprovalIsAPersonsAnswerAndTheItemSaysWhich(t *testing.T) {
	tr := permlog.NewCodexTracker()
	evs := frames(t, tr,
		`{"id":7,"method":"item/commandExecution/requestApproval","params":{"threadId":"th","itemId":"i1","command":"gh pr create","cwd":"/w","proposedExecpolicyAmendment":["gh","pr","create"]}}`,
		`{"id":8,"method":"item/commandExecution/requestApproval","params":{"threadId":"th","itemId":"i2","command":"rm -rf x","cwd":"/w"}}`,
		`{"method":"item/completed","params":{"threadId":"th","item":{"type":"commandExecution","id":"i1","status":"failed"}}}`,
		`{"method":"item/completed","params":{"threadId":"th","item":{"type":"commandExecution","id":"i2","status":"declined"}}}`,
	)
	recs := permlog.Fold("alpha", evs)
	if len(recs) != 2 {
		t.Fatalf("got %+v", recs)
	}
	if r := recs[0]; r.Answer != permlog.Allow || r.By != permlog.Person || strings.Join(r.Call.Amendment, " ") != "gh pr create" || r.Call.Cwd != "/w" {
		t.Errorf("a command that ran and failed: %+v", r)
	}
	if r := recs[1]; r.Answer != permlog.Deny || r.By != permlog.Person {
		t.Errorf("a declined command: %+v", r)
	}
}

func TestACodexTurnThatEndsOnAnOpenApprovalDeniedIt(t *testing.T) {
	tr := permlog.NewCodexTracker()
	recs := permlog.Fold("alpha", frames(t, tr,
		`{"id":1,"method":"item/fileChange/requestApproval","params":{"threadId":"th","itemId":"p1"}}`,
		`{"method":"turn/completed","params":{"threadId":"th","turn":{"id":"t1","status":"interrupted"}}}`,
	))
	if len(recs) != 1 || recs[0].Call.Kind != "write" || recs[0].Answer != permlog.Deny || recs[0].By != permlog.Person {
		t.Fatalf("got %+v", recs)
	}
}

func TestCodexReviewerVerdictsAreTheReviewersOrATimeout(t *testing.T) {
	review := func(id, status string) string {
		return `{"method":"item/autoApprovalReview/completed","params":{"threadId":"th","reviewId":"` + id + `","review":{"status":"` + status +
			`"},"action":{"type":"command","command":"curl example.com","cwd":"/w","source":"shell"}}}`
	}
	recs := permlog.Fold("alpha", frames(t, permlog.NewCodexTracker(), review("r1", "approved"), review("r2", "denied"), review("r3", "timedOut"), review("r4", "inProgress")))
	want := []struct {
		ans permlog.Answer
		by  permlog.By
	}{{permlog.Allow, permlog.Reviewer}, {permlog.Deny, permlog.Reviewer}, {permlog.Deny, permlog.Timeout}}
	if len(recs) != len(want) {
		t.Fatalf("got %+v", recs)
	}
	for i, w := range want {
		if r := recs[i]; r.Answer != w.ans || r.By != w.by || r.Call.Command != "curl example.com" {
			t.Errorf("review %d: %+v, want %s by %s", i, r, w.ans, w.by)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
