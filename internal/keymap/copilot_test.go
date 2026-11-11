package keymap_test

// Copilot's key map against menus recorded from Copilot 1.0.109 in tmux.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/keymap"
)

var copilotMenuLine = regexp.MustCompile(`^[│\s]*(❯)?\s*(\d+)\. (.+?)[\s│]*$`)

func copilotCapture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "copilot", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// pickCopilotPermission plays keys against the recorded permission menu as it took them: a digit picks that item
// at once and Esc cancels. Enter and the arrows were never sent to it, so the replay does not know them.
func pickCopilotPermission(t *testing.T, keys string) string {
	t.Helper()
	var labels []string
	for _, l := range strings.Split(copilotCapture(t, "permission-menu.txt"), "\n") {
		if m := copilotMenuLine.FindStringSubmatch(l); m != nil {
			labels = append(labels, m[3])
		}
	}
	if len(labels) != 3 {
		t.Fatalf("permission-menu.txt holds %d items, want 3", len(labels))
	}
	switch {
	case keys == "\x1b":
		return cancelled
	case len(keys) == 1 && keys[0] >= '1' && keys[0] <= '3':
		return labels[keys[0]-'1']
	}
	t.Fatalf("keys %q: the recording does not show what the permission menu does with them", keys)
	return ""
}

// pickCopilotQuestion plays keys against the recorded ask_user form: Down moves the cursor, Enter accepts it, a digit
// does nothing (question-after-digit.txt), Esc cancels.
func pickCopilotQuestion(t *testing.T, keys string) string {
	t.Helper()
	var labels []string
	in := false
	for _, l := range strings.Split(copilotCapture(t, "question-menu.txt"), "\n") {
		switch {
		case strings.HasPrefix(l, " ↑/↓ select"):
			in = false
		case in:
			labels = append(labels, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "❯")))
		case strings.HasPrefix(l, " Pick your"):
			in = true
		}
	}
	if len(labels) != 4 || !strings.HasPrefix(labels[3], "Other") {
		t.Fatalf("question-menu.txt items = %q, want three options and Other", labels)
	}
	cur := 0
	for rest := keys; rest != ""; {
		switch {
		case strings.HasPrefix(rest, "\x1b[B"):
			if cur < len(labels)-1 {
				cur++
			}
			rest = rest[3:]
		case rest == "\x1b":
			return cancelled
		case rest[0] == '\r':
			return labels[cur]
		case rest[0] >= '1' && rest[0] <= '9':
			rest = rest[1:]
		default:
			t.Fatalf("keys %q: the recording does not show what the form does with %q", keys, rest[:1])
		}
	}
	t.Fatalf("keys %q accept nothing in question-menu.txt", keys)
	return ""
}

func TestCopilotKeyMapPicksTheRecordedMenusItems(t *testing.T) {
	m, ok := keymap.For("copilot")
	if !ok {
		t.Fatal("keymap.For(copilot) has no key map")
	}
	keys := func(s string, ok bool) string {
		t.Helper()
		if !ok {
			t.Fatal("the key map has no keys for this answer")
		}
		return s
	}
	checkCopilotRecordedPanes(t)
	if got := pickCopilotPermission(t, keys(m.Permission("allow"))); got != "Yes" {
		t.Errorf("Allow picks %q in the recorded permission menu, want Yes (once, never the item that saves an approval)", got)
	}
	if got := pickCopilotPermission(t, keys(m.Permission("deny"))); got != cancelled {
		t.Errorf("Deny does %q in the recorded permission menu, want Esc to cancel it", got)
	}
	for n, want := range map[int]string{1: "Red", 2: "Green", 3: "Blue"} {
		if got := pickCopilotQuestion(t, keys(m.Question(n))); got != want {
			t.Errorf("question option %d picks %q in the recorded form, want %q (not its Other item)", n, got, want)
		}
	}
}

// The recorded panes after each answer show what it did. A digit does nothing in the form, so a question's keys at an
// idle prompt are Down and Enter, which would walk history and resubmit: the route's pane-state gate is the guard.
func checkCopilotRecordedPanes(t *testing.T) {
	t.Helper()
	for file, want := range map[string]string{
		"after-permission-1.txt":   "$ Shell Create empty file named z",
		"after-permission-esc.txt": "Operation aborted by user",
		"after-question-2.txt":     "Colour: Green",
	} {
		if !strings.Contains(copilotCapture(t, file), want) {
			t.Errorf("%s does not show %q", file, want)
		}
	}
	if !strings.Contains(copilotCapture(t, "question-after-digit.txt"), "❯ Red") {
		t.Error("question-after-digit.txt: a digit moved the form's cursor")
	}
}

// One Esc at an idle prompt only hints; two within half a second open /rewind, so a late Deny must send exactly one.
func TestCopilotDenyKeyAtAnIdlePromptChangesNothing(t *testing.T) {
	m, ok := keymap.For("copilot")
	if !ok {
		t.Fatal("keymap.For(copilot) has no key map")
	}
	if deny, _ := m.Permission("deny"); deny != "\x1b" {
		t.Errorf("Deny sends %q, want one Esc", deny)
	}
	idle := copilotCapture(t, "idle-esc.txt")
	if strings.Contains(idle, "Working") || strings.Contains(idle, "rewind") || !strings.Contains(idle, "❯\n") {
		t.Errorf("the pane after one Esc at an idle prompt is not an empty idle prompt:\n%s", idle)
	}
}
