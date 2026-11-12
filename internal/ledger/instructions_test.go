package ledger

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func instrFile(home, fleet string) string {
	return filepath.Join(home, "chat", fleet, "instructions.md")
}

func readInstrFile(t *testing.T, home, fleet string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(instrFile(home, fleet))
	if os.IsNotExist(err) {
		return "", false
	} else if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

func mustSet(t *testing.T, l *Ledger, fleet, text string) {
	t.Helper()
	if _, err := l.SetInstructions(fleet, text); err != nil {
		t.Fatalf("SetInstructions(%.20q) = %v, want it stored", text, err)
	}
}

func overBy(t *testing.T, err error, n string) {
	t.Helper()
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("error = %v, want a *RefusedError", err)
	}
	if !regexp.MustCompile(`\b` + n + ` characters?\b`).MatchString(refused.Msg) {
		t.Errorf("message = %q, want it to say the text is over by %s characters", refused.Msg, n)
	}
}

func TestInstructionsAreStoredTrimmedInTheFleetsDirectoryAndReadBack(t *testing.T) {
	l, home := newLedger(t)
	if got, err := l.Instructions("home"); got != "" || err != nil {
		t.Fatalf("Instructions of a fleet with none = %q, %v; want empty and no error", got, err)
	}
	stored, err := l.SetInstructions("home", "  Be terse.\n\nAsk before launching.\n ")
	if err != nil || stored != "Be terse.\n\nAsk before launching." {
		t.Fatalf("SetInstructions = %q, %v; want the trimmed text", stored, err)
	}
	if got, ok := readInstrFile(t, home, "home"); !ok || got != "Be terse.\n\nAsk before launching." {
		t.Errorf("chat/home/instructions.md = %q (exists %v), want the trimmed text exactly", got, ok)
	}
	if got, err := l.Instructions("home"); got != stored || err != nil {
		t.Errorf("Instructions = %q, %v; want %q", got, err, stored)
	}
	if got, _ := l.Instructions("work"); got != "" {
		t.Errorf("another fleet's Instructions = %q, want none", got)
	}
}

func TestInstructionsOverTheCapAreRefusedByHowMuchAndTheFileIsUnchanged(t *testing.T) {
	l, home := newLedger(t)
	mustSet(t, l, "home", "keep me")
	_, err := l.SetInstructions("home", strings.Repeat("x", MaxInstructionChars+7))
	overBy(t, err, "7")
	if got, _ := readInstrFile(t, home, "home"); got != "keep me" {
		t.Errorf("file = %q after a refusal, want it unchanged", got)
	}
	if got, _ := l.Instructions("home"); got != "keep me" {
		t.Errorf("Instructions = %q after a refusal, want it unchanged", got)
	}
}

func TestTheCapCountsCodePointsAfterTrimmingNotBytesOrUTF16Units(t *testing.T) {
	l, _ := newLedger(t)
	atCap := strings.Repeat("😀", MaxInstructionChars)
	mustSet(t, l, "home", "  "+atCap+"\n")
	if got, _ := l.Instructions("home"); got != atCap {
		t.Errorf("2000 emoji (8000 bytes) stored as %d bytes, want accepted whole", len(got))
	}
	_, err := l.SetInstructions("home", atCap+"😀😀")
	overBy(t, err, "2")
}

func TestEmptyInstructionsRemoveTheFile(t *testing.T) {
	l, home := newLedger(t)
	mustSet(t, l, "home", "something")
	if _, ok := readInstrFile(t, home, "home"); !ok {
		t.Fatal("instructions.md missing after a save, so there is nothing to remove")
	}
	stored, err := l.SetInstructions("home", " \n\t ")
	if err != nil || stored != "" {
		t.Fatalf("SetInstructions(blank) = %q, %v; want empty and no error", stored, err)
	}
	if _, ok := readInstrFile(t, home, "home"); ok {
		t.Error("instructions.md still exists, want it removed")
	}
	if got, err := l.Instructions("home"); got != "" || err != nil {
		t.Errorf("Instructions = %q, %v; want none", got, err)
	}
}

func TestInstructionsNeverFollowALinkOutOfTheFleetsDirectory(t *testing.T) {
	l, home := newLedger(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(instrFile(home, "home")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, instrFile(home, "home")); err != nil {
		t.Fatal(err)
	}
	if got, err := l.Instructions("home"); err == nil && got == "not yours" {
		t.Error("Instructions read through a link outside the directory")
	}
	if _, err := l.SetInstructions("home", "mine"); err == nil {
		if got, _ := l.Instructions("home"); got != "mine" {
			t.Errorf("a write that reported success reads back %q, want mine", got)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "not yours" {
		t.Errorf("the file the link pointed at = %q, want it untouched", b)
	}
}

func TestInstructionsRefuseAKeyThatIsNotAFleet(t *testing.T) {
	l, _ := newLedger(t)
	if _, err := l.Instructions("../x"); err == nil {
		t.Error("Instructions(../x) = no error, want one")
	}
	if _, err := l.SetInstructions("../x", "hi"); err == nil {
		t.Error("SetInstructions(../x) = no error, want one")
	}
}

func TestCancellingANoteAsTheEngineerClosesItWithNoVia(t *testing.T) {
	l, home := newLedger(t)
	keep := mustAdd(t, l, "home", "keep", "admiralbot")
	gone := mustAdd(t, l, "home", "gone", "admiralbot")
	if err := l.Cancel("home", gone.ID); err != nil {
		t.Fatalf("Cancel = %v, want nil", err)
	}
	if got, _ := l.Notes("home"); len(got) != 1 || got[0].ID != keep.ID {
		t.Errorf("open notes = %+v, want only %q", got, keep.Text)
	}
	ls := lines(t, home, "home")
	last := ls[len(ls)-1]
	if len(ls) != 3 || !strings.Contains(last, `"event":"cancelled"`) || !strings.Contains(last, gone.ID) || strings.Contains(last, `"via"`) {
		t.Errorf("log = %q, want a third line, a cancelled event for the note with no via", ls)
	}
}

func TestCancellingAnUnknownOrAlreadyCancelledNoteIsNotFoundAndWritesNothing(t *testing.T) {
	l, home := newLedger(t)
	n := mustAdd(t, l, "home", "once", "admiralbot")
	if err := l.Cancel("home", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Cancel(unknown) = %v, want ErrNotFound", err)
	}
	if err := l.Cancel("home", n.ID); err != nil {
		t.Fatalf("Cancel = %v", err)
	}
	if err := l.Cancel("home", n.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Cancel(again) = %v, want ErrNotFound", err)
	}
	if err := l.Cancel("work", n.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Cancel in another fleet = %v, want ErrNotFound", err)
	}
	if got := lines(t, home, "home"); len(got) != 2 {
		t.Errorf("log has %d lines, want 2 (added, cancelled)", len(got))
	}
}

func TestPromotingANoteAppendsItAfterABlankLineAndCancelsIt(t *testing.T) {
	l, home := newLedger(t)
	mustSet(t, l, "home", "Be terse.")
	n := mustAdd(t, l, "home", "Deploys go out on Tuesdays.", "admiralbot")
	got, err := l.Promote("home", n.ID)
	if want := "Be terse.\n\nDeploys go out on Tuesdays."; err != nil || got != want {
		t.Fatalf("Promote = %q, %v; want %q", got, err, want)
	}
	if file, _ := readInstrFile(t, home, "home"); file != got {
		t.Errorf("instructions.md = %q, want %q", file, got)
	}
	if open, _ := l.Notes("home"); len(open) != 0 {
		t.Errorf("open notes = %+v, want the promoted one cancelled", open)
	}
}

func TestPromotingIntoEmptyInstructionsIsJustTheNote(t *testing.T) {
	l, _ := newLedger(t)
	n := mustAdd(t, l, "home", "Only this.", "admiralbot")
	if got, err := l.Promote("home", n.ID); err != nil || got != "Only this." {
		t.Errorf("Promote = %q, %v; want the note alone, no separator", got, err)
	}
}

func TestPromotingCountsTheSeparatorAndRefusesPastTheCapLeavingBothUnchanged(t *testing.T) {
	l, home := newLedger(t)
	mustSet(t, l, "home", strings.Repeat("i", MaxInstructionChars-22))
	exact := mustAdd(t, l, "home", strings.Repeat("n", 20), "admiralbot")
	if got, err := l.Promote("home", exact.ID); err != nil || len([]rune(got)) != MaxInstructionChars {
		t.Fatalf("Promote to exactly the cap = %d chars, %v; want %d and no error", len([]rune(got)), err, MaxInstructionChars)
	}
	before, _ := readInstrFile(t, home, "home")
	over := mustAdd(t, l, "home", "twelve", "admiralbot")
	_, err := l.Promote("home", over.ID)
	overBy(t, err, "8")
	if after, _ := readInstrFile(t, home, "home"); after != before {
		t.Error("instructions changed on a refused promote")
	}
	if open, _ := l.Notes("home"); len(open) != 1 || open[0].ID != over.ID {
		t.Errorf("open notes = %+v, want the refused note still open", open)
	}
}

func TestPromotingANoteThatWasDeletedIsNotFoundAndChangesNothing(t *testing.T) {
	l, _ := newLedger(t)
	mustSet(t, l, "home", "Be terse.")
	n := mustAdd(t, l, "home", "gone", "admiralbot")
	if err := l.Cancel("home", n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Promote("home", n.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Promote(cancelled) = %v, want ErrNotFound", err)
	}
	if got, _ := l.Instructions("home"); got != "Be terse." {
		t.Errorf("Instructions = %q, want unchanged", got)
	}
}

func TestANoteWithInvisibleCharactersIsRefusedAtAddAndAtPromote(t *testing.T) {
	for name, hidden := range map[string]string{"a tag character": "\U000E0041", "a bidi override": "‮", "a zero-width space": "​", "a control": "\x1b"} {
		t.Run(name, func(t *testing.T) {
			l, home := newLedger(t)
			text := "Prefers tabs" + hidden + " over spaces"
			_, err := l.AddNote("home", text, "admiralbot")
			var refused *RefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("AddNote = %v, want a *RefusedError", err)
			}
			if got := lines(t, home, "home"); len(got) != 0 {
				t.Errorf("ledger = %q, want nothing written", got)
			}
			// A note written before the check is still refused at promote and stays open.
			quoted, _ := json.Marshal(text)
			line := `{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"n1","kind":"note","text":` + string(quoted) + `}` + "\n"
			if err := os.MkdirAll(filepath.Dir(file(home, "home")), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file(home, "home"), []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			mustSet(t, l, "home", "Be terse.")
			if _, err := l.Promote("home", "n1"); !errors.As(err, &refused) {
				t.Fatalf("Promote = %v, want a *RefusedError", err)
			}
			if got, _ := l.Instructions("home"); got != "Be terse." {
				t.Errorf("instructions = %q, want unchanged", got)
			}
			if notes, _ := l.Notes("home"); len(notes) != 1 {
				t.Errorf("notes = %+v, want the note still open", notes)
			}
		})
	}
}

func TestTheInstructionsTrimAByteOrderMarkLikeThePageDoes(t *testing.T) {
	l, _ := newLedger(t)
	if got, _ := l.SetInstructions("home", "\uFEFFBe terse.\uFEFF"); got != "Be terse." {
		t.Errorf("stored = %q, want the mark trimmed", got)
	}
}
