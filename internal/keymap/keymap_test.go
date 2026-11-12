package keymap_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/keymap"
)

// Claude Code's cursor is ❯, Codex's is ›; Codex adds a shortcut hint like "(y)" or "(esc)" and, in questions, a description after two spaces.
var (
	menuLine     = regexp.MustCompile(`^\s*[❯›]?\s*(\d+)\. (.+)$`)
	codexTrailer = regexp.MustCompile(`\s{2,}.*$`)
	codexHint    = regexp.MustCompile(` \((?:[a-z]+|Recommended)\)$`)
)

// cancelled is what pick returns for Esc, which picks no item: the menu cancels itself.
const cancelled = "cancelled"

// pick plays keys against a recorded menu of a harness the way its TUI took them: the cursor starts on the
// first item, a digit picks that item at once, Up and Down move the cursor and wrap, Enter picks it, Esc cancels.
func pick(t *testing.T, harness, menu, keys string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", harness, menu))
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, l := range strings.Split(string(raw), "\n") {
		if m := menuLine.FindStringSubmatch(l); m != nil {
			label := strings.TrimSpace(m[2])
			if harness == "codex" {
				label = codexHint.ReplaceAllString(codexTrailer.ReplaceAllString(label, ""), "")
			}
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		t.Fatalf("%s holds no menu", menu)
	}
	cur := 0
	for rest := keys; rest != ""; {
		switch {
		case strings.HasPrefix(rest, "\x1b[A"):
			cur, rest = (cur+len(labels)-1)%len(labels), rest[3:]
		case strings.HasPrefix(rest, "\x1b[B"):
			cur, rest = (cur+1)%len(labels), rest[3:]
		case rest == "\x1b":
			return cancelled
		case rest[0] == '\r':
			return labels[cur]
		case rest[0] >= '1' && rest[0] <= '9':
			n, _ := strconv.Atoi(rest[:1])
			if n > len(labels) {
				t.Fatalf("keys %q pick item %d of a %d-item menu", keys, n, len(labels))
			}
			return labels[n-1]
		default:
			t.Fatalf("keys %q: the replay does not know %q", keys, rest[:1])
		}
	}
	t.Fatalf("keys %q pick nothing in %s", keys, menu)
	return ""
}

func TestClaudeKeyMapPicksTheRecordedMenusItems(t *testing.T) {
	m, ok := keymap.For("claude")
	if !ok {
		t.Fatal("keymap.For(claude) has no key map")
	}
	keys := func(s string, ok bool) string {
		t.Helper()
		if !ok {
			t.Fatal("the key map has no keys for this answer")
		}
		return s
	}
	if got := pick(t, "claude", "permission-menu.txt", keys(m.Permission("allow"))); got != "Yes" {
		t.Errorf("Allow once picks %q in the recorded permission menu, want %q", got, "Yes")
	}
	deny := keys(m.Permission("deny"))
	for _, menu := range []string{"permission-menu.txt", "write-menu.txt"} {
		if got := pick(t, "claude", menu, deny); got != cancelled {
			t.Errorf("Deny does %q in the recorded %s, want Esc to cancel it", got, menu)
		}
	}
	for n, want := range map[int]string{1: "Red", 2: "Green", 3: "Blue"} {
		if got := pick(t, "claude", "question-menu.txt", keys(m.Question(n))); got != want {
			t.Errorf("question option %d picks %q in the recorded question menu, want %q", n, got, want)
		}
	}
	// The pane after each recorded key shows what that key did.
	for file, want := range map[string]string{"after-permission-1.txt": "finished without errors", "after-permission-esc.txt": "Interrupted", "after-write-esc.txt": "User rejected write", "after-question-2.txt": "Green"} {
		b, err := os.ReadFile(filepath.Join("testdata", "claude", file))
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s does not show %q (read error: %v)", file, want, err)
		}
	}
}

// Esc at an idle prompt with history submits nothing, so a Deny that arrives late does no harm.
func TestDenyKeyAtAnIdlePromptSubmitsNothing(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "claude", "idle-esc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "❯ Run this exact bash command"); n != 1 {
		t.Errorf("the pane after Esc at an idle prompt holds the earlier prompt %d times, want it once (not resubmitted)", n)
	}
}

func TestCodexKeyMapPicksTheRecordedMenusItems(t *testing.T) {
	m, ok := keymap.For("codex")
	if !ok {
		t.Fatal("keymap.For(codex) has no key map")
	}
	keys := func(s string, ok bool) string {
		t.Helper()
		if !ok {
			t.Fatal("the key map has no keys for this answer")
		}
		return s
	}
	for _, menu := range []string{"permission-menu.txt", "write-menu.txt"} {
		if got := pick(t, "codex", menu, keys(m.Permission("allow"))); got != "Yes, proceed" {
			t.Errorf("Allow once picks %q in the recorded %s, want %q", got, menu, "Yes, proceed")
		}
		if got := pick(t, "codex", menu, keys(m.Permission("deny"))); got != cancelled {
			t.Errorf("Deny does %q in the recorded %s, want Esc to cancel it", got, menu)
		}
	}
	for n, want := range map[int]string{1: "Red", 2: "Green", 3: "Blue"} {
		if got := pick(t, "codex", "question-menu.txt", keys(m.Question(n))); got != want {
			t.Errorf("question option %d picks %q in the recorded question menu, want %q", n, got, want)
		}
	}
	for file, want := range map[string]string{
		"after-permission-1.txt":   "You approved codex to run touch made.txt this time",
		"after-permission-esc.txt": "You canceled the request to run touch made.txt",
		"after-question-2.txt":     "answer: Green",
	} {
		b, err := os.ReadFile(filepath.Join("testdata", "codex", file))
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s does not show %q (read error: %v)", file, want, err)
		}
	}
}

// The patch menu's cancel is the second interruption in this recording; the first is the command menu's.
func TestCodexPatchCancelInterruptsTheTurn(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "codex", "after-write-esc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "Conversation interrupted"); n != 2 {
		t.Errorf("the pane after the patch menu's Esc shows %d interruptions, want 2 (the command menu's and the patch menu's)", n)
	}
}

// Esc at an idle Codex prompt only arms "edit previous message", so a Deny that arrives late resubmits nothing.
func TestCodexDenyKeyAtAnIdlePromptSubmitsNothing(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "codex", "idle-esc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "› Run this exact shell command"); n != 2 {
		t.Errorf("the pane after Esc at an idle prompt holds the earlier prompt %d times, want the 2 it held before", n)
	}
	if !strings.Contains(string(b), "esc again to edit previous message") {
		t.Error("the pane after Esc at an idle prompt does not show the edit-previous-message hint")
	}
}

// The keys of a Claude call are the ones typed into the recorded session, whose panes show what they did.
func TestClaudeCallKeysAreTheOnesRecorded(t *testing.T) {
	m, _ := keymap.For("claude")
	const down = "\x1b[B"
	batch, ok := m.Call([]keymap.CallQuestion{
		{Options: 3, Choose: []int{3}},
		{Options: 3, Multi: true, Choose: []int{1, 3}, Other: "Pine"},
	})
	if want := "3" + "\r" + down + down + "\r" + down + "Pine" + down + "\r" + "1"; !ok || batch != want {
		t.Errorf("batch keys = %q (ok %v), want the recorded %q", batch, ok, want)
	}
	single, ok := m.Call([]keymap.CallQuestion{{Options: 3, Multi: true, Choose: []int{1, 3}}})
	if want := "\r" + down + down + "\r" + down + down + "\r" + "1"; !ok || single != want {
		t.Errorf("single multi-select keys = %q (ok %v), want the recorded %q", single, ok, want)
	}
	if got, _ := m.Call([]keymap.CallQuestion{{Options: 3, Other: "Purple"}}); got != "4Purple\r" {
		t.Errorf("a single-choice Other = %q, want the Other digit, the text and Enter, with no review step", got)
	}
	for file, wants := range map[string][]string{
		"batch-q1-menu.txt":                 {"4. Type something.", "5. Chat about this"},
		"batch-q2-multi-menu.txt":           {"1. [ ] Cheese", "4. [ ] Type something", "Submit", "5. Chat about this"},
		"batch-q2-arrows-before-submit.txt": {"[✔] Cheese", "[✔] Olives", "❯ 4. [✔] Pine"},
		"single-multi-before-submit.txt":    {"[✔] Cheese", "[✔] Olives", "❯    Submit"},
		"batch-review-arrows.txt":           {"→ Blue", "→ Cheese, Olives, Pine", "❯ 1. Submit answers", "2. Cancel"},
		"single-multi-after-submit.txt":     {"→ Cheese, Olives", "❯ 1. Submit answers"},
		"after-batch-submit.txt":            {"Which colour? → Blue", "Which toppings? → Cheese, Olives, Pine"},
	} {
		b, err := os.ReadFile(filepath.Join("testdata", "claude", file))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s does not show %q", file, want)
			}
		}
	}
	for name, qs := range map[string][]keymap.CallQuestion{
		"no answer":              {{Options: 3, Multi: true}},
		"two for a single":       {{Options: 3, Choose: []int{1, 2}}},
		"options out of order":   {{Options: 3, Multi: true, Choose: []int{2, 1}}},
		"an option past the end": {{Options: 3, Multi: true, Choose: []int{4}}},
		"five questions":         make([]keymap.CallQuestion, 5),
	} {
		if keys, ok := m.Call(qs); ok {
			t.Errorf("%s gave keys %q, want none", name, keys)
		}
	}
	for _, h := range []string{"copilot", "codex"} {
		other, _ := keymap.For(h)
		if _, ok := other.Call([]keymap.CallQuestion{{Options: 3, Choose: []int{1}}}); ok {
			t.Errorf("%s has keys for a call, which no recording backs", h)
		}
	}
}
