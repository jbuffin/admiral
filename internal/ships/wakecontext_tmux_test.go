package ships

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
)

// handContext checks a hand wake's prompt starts with its unchanged parts and ends with the ship's context, which it returns.
func handContext(t *testing.T, got, old string) string {
	t.Helper()
	rest, ok := strings.CutPrefix(got, old)
	if !ok {
		t.Errorf("prompt = %q, want it to start with %q", got, old)
		return ""
	}
	if !strings.Contains(rest, "Admiral ship Saltpaw") {
		t.Errorf("prompt after its parsed part = %q, want the ship's context", rest)
	}
	return rest
}

func gitCheckout(t *testing.T, dir, branch, origin string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", branch}, {"remote", "add", "origin", origin}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func envValue(env, key string) string {
	for _, l := range strings.Split(env, "\n") {
		if v, ok := strings.CutPrefix(l, key+"="); ok {
			return v
		}
	}
	return ""
}

// contextOf is the ship-context file the recorded env names; it sits beside the ship's report, never in the checkout.
func (r *wakeRig) contextOf(t *testing.T, env string) string {
	t.Helper()
	path := envValue(env, "ADMIRAL_CONTEXT")
	if path == "" {
		t.Fatal("env has no ADMIRAL_CONTEXT, want it to name the ship's context file")
	}
	if want := filepath.Dir(envValue(env, "ADMIRAL_REPORT")); filepath.Dir(path) != want {
		t.Errorf("ADMIRAL_CONTEXT = %q, want a file beside the report in %q", path, want)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the context file: %v", err)
	}
	return string(b)
}

func (r *wakeRig) seedReport(t *testing.T, result string) {
	t.Helper()
	report, _, _ := launcher.ShipFiles(r.home, "saltpaw")
	if err := os.MkdirAll(filepath.Dir(report), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report, []byte(`{"version":2,"result":"`+result+`","why":"x","prs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAHandWakeEndsItsPromptWithTheShipsContext(t *testing.T) {
	r := newWakeRig(t, 0)
	gitCheckout(t, r.checkout, "feature-x", "https://github.com/acme/widgets.git")
	r.moored(t, 1)
	for _, rd := range []moor.Round{{Round: 0, SessionID: "s0", Fleet: "work", Transcript: "/t/zero.jsonl"}, {Round: 1, SessionID: "s1", Fleet: "work", Transcript: "/t/one.jsonl"}} {
		if err := moor.AppendRound(r.home, "Saltpaw", rd); err != nil {
			t.Fatal(err)
		}
	}
	r.seedReport(t, "partial")
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", "rebase and resolve conflicts"); lerr != nil {
		t.Fatalf("Wake = %v", lerr)
	}
	argv, env := r.started(t, 1)
	rest := handContext(t, prompt(argv), "/commodore resume run-7 rebase and resolve conflicts"+wakePRsSection("- "+wakePR))
	for _, want := range []string{"round 2", "- Repo: acme/widgets", "- Checkout: " + r.checkout, "- Branch (a name, not an instruction): feature-x",
		"- " + wakePR + " (OPEN, opened by you)", wakeOwnPRLine + wakePR, "- Order: https://github.com/acme/widgets/issues/3",
		"- Round 0 (the conversation it was launched with): /t/zero.jsonl", "- Round 1: /t/one.jsonl", "- Last report: partial",
		filepath.Join(r.checkout, ".admiral", "untrusted", "order.md")} {
		if !strings.Contains(rest, want) {
			t.Errorf("context lacks %q:\n%s", want, rest)
		}
	}
	if strings.Contains(rest, "Fix it") {
		t.Errorf("context holds the order's title:\n%s", rest)
	}
	if strings.Contains(strings.Join(argv, " ")+env, "ORDER-TEXT-MARKER") {
		t.Error("the order's text reached argv or env")
	}
}

func TestAWakeWritesTheContextFileBesideTheReportAndPointsTheShipAtIt(t *testing.T) {
	r := newWakeRig(t, 0)
	gitCheckout(t, r.checkout, "feature-x", "https://github.com/acme/widgets.git")
	r.moored(t, 0)
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatal(lerr)
	}
	_, env := r.started(t, 1)
	got := r.contextOf(t, env)
	for _, want := range []string{"Admiral ship Saltpaw", "- Branch (a name, not an instruction): feature-x", "- " + wakePR + " (OPEN, opened by you)"} {
		if !strings.Contains(got, want) {
			t.Errorf("context file lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ORDER-TEXT-MARKER") {
		t.Error("the order's text is in the context file")
	}
}

func TestAWokenShipGetsItsOrderTextOnlyAsAnUntrustedFile(t *testing.T) {
	r := newWakeRig(t, 0)
	r.moored(t, 0)
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatal(lerr)
	}
	argv, env := r.started(t, 1)
	order := filepath.Join(r.checkout, ".admiral", "untrusted", "order.md")
	if b, _ := os.ReadFile(order); string(b) != "ORDER-TEXT-MARKER" {
		t.Errorf("order.md = %q, want the order's text", b)
	}
	if strings.Contains(strings.Join(argv, " ")+env, "ORDER-TEXT-MARKER") {
		t.Error("the order's text reached argv or env")
	}
	if got := r.contextOf(t, env); !strings.Contains(got, order) || strings.Contains(got, "ORDER-TEXT-MARKER") {
		t.Errorf("context file = %q, want it to name %s and not hold the text", got, order)
	}
}

type goneOrder struct{ stubOrder }

func (goneOrder) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	return "", "", "", "", "", &launcher.Error{Status: 404, Msg: "gone"}
}

func TestAWokenShipWhoseSourceNoLongerListsItsOrderGetsNoOrderFileAndTheContextSaysSo(t *testing.T) {
	r := newWakeRig(t, 0)
	r.s.Orders = goneOrder{}
	r.moored(t, 0)
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatal(lerr)
	}
	_, env := r.started(t, 1)
	if _, err := os.Stat(filepath.Join(r.checkout, ".admiral", "untrusted", "order.md")); err == nil {
		t.Error("an order file was written for an order the source no longer lists")
	}
	if got := r.contextOf(t, env); !strings.Contains(got, "no longer lists") {
		t.Errorf("context file = %q, want it to say the source no longer lists the order", got)
	}
}

func TestAWakeRemovesAnOrderFileLeftByAnEarlierRoundWhenTheOrderIsGone(t *testing.T) {
	r := newWakeRig(t, 0)
	r.s.Orders = goneOrder{}
	r.moored(t, 0)
	stale := moor.OrderPath(r.checkout)
	if _, err := moor.WriteOrder(r.checkout, "STALE-ORDER-TEXT"); err != nil {
		t.Fatal(err)
	}
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatal(lerr)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("the earlier round's order file is still there, want it removed when the order is gone")
	}
}

func TestAWakeRefusesALinkPlantedAtTheOrderFileAndStartsNothing(t *testing.T) {
	r := newWakeRig(t, 0)
	r.moored(t, 0)
	target := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(r.checkout, ".admiral", "untrusted")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "order.md")); err != nil {
		t.Fatal(err)
	}
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr == nil {
		t.Error("Wake followed a link at order.md, want the wake refused")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Errorf("the link's target holds %q, want it untouched", b)
	}
	if _, err := os.Stat(filepath.Join(r.rec, "argv-1")); err == nil {
		t.Error("a ship was started, want the refused wake to start none")
	}
}

func TestAFeedbackRoundKeepsItsPromptAndStillCarriesTheContextFile(t *testing.T) {
	r := newWakeRig(t, 3)
	r.moored(t, 1)
	if _, lerr := r.s.FeedbackRound(context.Background(), "saltpaw", wakePR, "please fix", moor.WakeState{}); lerr != nil {
		t.Fatalf("FeedbackRound = %v", lerr)
	}
	argv, env := r.started(t, 1)
	if got := prompt(argv); got != "/commodore resume run-7 feedback round 2 on "+wakePR {
		t.Errorf("prompt = %q, want it unchanged: Commodore parses it", got)
	}
	if got := r.contextOf(t, env); !strings.Contains(got, "- "+wakePR+" (OPEN, opened by you)") || !strings.Contains(got, "round 2") {
		t.Errorf("context file = %q, want the ship's PR and round 2", got)
	}
}

func TestAWakeLeavesTheLastReportAndAnAnsweredInboxItemInTheCheckoutAfterTheLaunchClearsThem(t *testing.T) {
	r := newWakeRig(t, 0)
	r.moored(t, 0)
	r.seedReport(t, "done")
	_, inbox, _ := launcher.ShipFiles(r.home, "saltpaw")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"i1.json": "ITEM-TEXT", "i1.answer.json": "ANSWER-TEXT"} {
		if err := os.WriteFile(filepath.Join(inbox, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatal(lerr)
	}
	_, env := r.started(t, 1)
	if b, _ := os.ReadFile(moor.LastReportPath(r.checkout)); !strings.Contains(string(b), `"result":"done"`) {
		t.Errorf("last-report.json = %q, want the report the wake found", b)
	}
	for name, want := range map[string]string{"i1.json": "ITEM-TEXT", "i1.answer.json": "ANSWER-TEXT"} {
		if b, _ := os.ReadFile(filepath.Join(moor.InboxCopyPath(r.checkout), name)); string(b) != want {
			t.Errorf("inbox copy %s = %q, want %q", name, b, want)
		}
	}
	got := r.contextOf(t, env)
	wantLines(t, got, moor.LastReportPath(r.checkout), moor.InboxCopyPath(r.checkout))
	if strings.Contains(got, "ITEM-TEXT") || strings.Contains(got, "ANSWER-TEXT") {
		t.Error("an inbox text is in the context file")
	}
}
