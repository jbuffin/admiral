package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func newLedger(t *testing.T) (*Ledger, string) {
	t.Helper()
	home := t.TempDir()
	return New(home, func() time.Time { return t0 }), home
}

func file(home, fleet string) string { return filepath.Join(home, "chat", fleet, "ledger.jsonl") }

func lines(t *testing.T, home, fleet string) []string {
	t.Helper()
	b, err := os.ReadFile(file(home, fleet))
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func seed(t *testing.T, home, fleet, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file(home, fleet)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file(home, fleet), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func texts(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Text)
	}
	return out
}

func mustAdd(t *testing.T, l *Ledger, fleet, text, via string) Entry {
	t.Helper()
	e, err := l.AddNote(fleet, text, via)
	if err != nil || e.ID == "" {
		t.Fatalf("AddNote(%q) = %+v, %v; want a note", text, e, err)
	}
	return e
}

func TestANoteIsOneEventLineWithTheOutcomeLogsFieldNames(t *testing.T) {
	l, home := newLedger(t)
	e := mustAdd(t, l, "home", "  the deploy key is in 1Password  ", "admiralbot")
	got := lines(t, home, "home")
	if len(got) != 1 {
		t.Fatalf("lines = %q, want one", got)
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(got[0]), &ev); err != nil {
		t.Fatalf("line %q is not JSON: %v", got[0], err)
	}
	want := map[string]any{"v": float64(1), "ts": "2026-11-12T10:00:00Z", "event": "added", "id": e.ID, "kind": "note", "text": "the deploy key is in 1Password", "via": "admiralbot"}
	for k, w := range want {
		if ev[k] != w {
			t.Errorf("event %s = %v, want %v (line %s)", k, ev[k], w, got[0])
		}
	}
	if e.Text != "the deploy key is in 1Password" || e.Via != "admiralbot" || !e.TS.Equal(t0) {
		t.Errorf("entry = %+v, want trimmed text, via admiralbot, ts %v", e, t0)
	}
}

func TestTheEngineersNoteCarriesNoVia(t *testing.T) {
	l, home := newLedger(t)
	mustAdd(t, l, "home", "mine", "")
	if got := lines(t, home, "home"); len(got) != 1 || strings.Contains(got[0], `"via"`) {
		t.Errorf("lines = %q, want one event with no via key", got)
	}
}

func TestNotesComeOldestFirstWithUniqueURLSafeIDs(t *testing.T) {
	l, _ := newLedger(t)
	safe := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	seen := map[string]bool{}
	for _, text := range []string{"a", "b", "c"} {
		e := mustAdd(t, l, "home", text, "admiralbot")
		if !safe.MatchString(e.ID) || seen[e.ID] {
			t.Errorf("id %q is not unique and URL-safe", e.ID)
		}
		seen[e.ID] = true
	}
	got, err := l.Notes("home")
	if err != nil || strings.Join(texts(got), ",") != "a,b,c" {
		t.Errorf("Notes = %v, %v; want a,b,c", texts(got), err)
	}
}

func TestAFleetWithNoLedgerHasNoNotes(t *testing.T) {
	l, _ := newLedger(t)
	got, err := l.Notes("home")
	if err != nil || len(got) != 0 {
		t.Errorf("Notes = %v, %v; want none", got, err)
	}
}

func TestAddedThenCancelledFoldsToNoEntry(t *testing.T) {
	l, home := newLedger(t)
	seed(t, home, "home", strings.Join([]string{
		`{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"n1","kind":"note","text":"gone","via":"admiralbot"}`,
		`{"v":1,"ts":"2026-11-12T09:01:00Z","event":"added","id":"n2","kind":"note","text":"stays"}`,
		`{"v":1,"ts":"2026-11-12T09:02:00Z","event":"cancelled","id":"n1","kind":"note"}`,
	}, "\n")+"\n")
	got, err := l.Notes("home")
	if err != nil || len(got) != 1 || got[0].ID != "n2" || got[0].Text != "stays" || got[0].Via != "" {
		t.Errorf("Notes = %+v, %v; want only n2 with no via", got, err)
	}
}

func TestACancelledNoteFreesItsPlaceUnderTheLimit(t *testing.T) {
	l, home := newLedger(t)
	var sb strings.Builder
	for i := range MaxNotes {
		id := "n" + string(rune('a'+i))
		sb.WriteString(`{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"` + id + `","kind":"note","text":"t"}` + "\n")
	}
	sb.WriteString(`{"v":1,"ts":"2026-11-12T09:05:00Z","event":"cancelled","id":"na","kind":"note"}` + "\n")
	seed(t, home, "home", sb.String())
	mustAdd(t, l, "home", "fits now", "admiralbot")
}

func TestATornLastLineIsSkippedOnRead(t *testing.T) {
	l, home := newLedger(t)
	seed(t, home, "home", `{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"n1","kind":"note","text":"kept"}`+"\n"+`{"v":1,"ts":"2026-11-12T09:01:00Z","event":"add`)
	got, err := l.Notes("home")
	if err != nil || len(got) != 1 || got[0].ID != "n1" {
		t.Errorf("Notes = %+v, %v; want only n1", got, err)
	}
}

func TestAGarbageLineInTheMiddleIsSkipped(t *testing.T) {
	l, home := newLedger(t)
	seed(t, home, "home", `{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"n1","kind":"note","text":"a"}`+"\nnot json\n"+`{"v":1,"ts":"2026-11-12T09:01:00Z","event":"added","id":"n2","kind":"note","text":"b"}`+"\n")
	got, err := l.Notes("home")
	if err != nil || strings.Join(texts(got), ",") != "a,b" {
		t.Errorf("Notes = %v, %v; want a,b", texts(got), err)
	}
}

func TestAnAppendAfterATornLastLineSurvives(t *testing.T) {
	_, home := newLedger(t)
	seed(t, home, "home", `{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"n1","kind":"note","text":"kept"}`+"\n"+`{"v":1,"ts":"2026-11-12T09:01:00Z","event":"add`)
	// A new process: the torn line is on disk when it first appends.
	l := New(home, func() time.Time { return t0 })
	mustAdd(t, l, "home", "after the tear", "admiralbot")
	got, err := New(home, func() time.Time { return t0 }).Notes("home")
	if err != nil || strings.Join(texts(got), ",") != "kept,after the tear" {
		t.Errorf("Notes = %v, %v; want kept,after the tear: the torn line must not take the new event with it", texts(got), err)
	}
}

func TestTwoFleetsLedgersStaySeparate(t *testing.T) {
	l, home := newLedger(t)
	mustAdd(t, l, "home", "home's", "admiralbot")
	mustAdd(t, l, "work", "work's", "admiralbot")
	for fleet, want := range map[string]string{"home": "home's", "work": "work's"} {
		got, err := l.Notes(fleet)
		if err != nil || strings.Join(texts(got), ",") != want {
			t.Errorf("%s notes = %v, %v; want %s", fleet, texts(got), err, want)
		}
		if n := len(lines(t, home, fleet)); n != 1 {
			t.Errorf("%s ledger has %d lines, want 1", fleet, n)
		}
	}
}

func TestTheThirtyFirstNoteIsRefusedAndNothingIsWritten(t *testing.T) {
	l, home := newLedger(t)
	for i := range MaxNotes {
		mustAdd(t, l, "home", strings.Repeat("n", i+1), "admiralbot")
	}
	if e, err := l.AddNote("home", "one too many", "admiralbot"); err == nil || e.ID != "" {
		t.Fatalf("31st AddNote = %+v, %v; want a refusal", e, err)
	}
	if n := len(lines(t, home, "home")); n != MaxNotes {
		t.Errorf("ledger has %d lines, want %d", n, MaxNotes)
	}
	// Another fleet's ledger has its own room.
	mustAdd(t, l, "work", "fits", "admiralbot")
}

func TestAnEmptyOrTooLongNoteIsRefusedAndNothingIsWritten(t *testing.T) {
	l, home := newLedger(t)
	for _, text := range []string{"", "   \n\t ", strings.Repeat("x", MaxNoteChars+1), " " + strings.Repeat("é", MaxNoteChars+1) + " "} {
		if _, err := l.AddNote("home", text, "admiralbot"); err == nil {
			t.Errorf("AddNote(%.20q, %d chars) accepted, want a refusal", text, len([]rune(text)))
		}
	}
	if got := lines(t, home, "home"); len(got) != 0 {
		t.Errorf("ledger = %q, want nothing written", got)
	}
}

func TestANoteOfExactlyTheLimitAfterTrimmingIsAccepted(t *testing.T) {
	l, _ := newLedger(t)
	text := strings.Repeat("é", MaxNoteChars)
	if e := mustAdd(t, l, "home", "  "+text+"\n", "admiralbot"); e.Text != text {
		t.Errorf("text = %d chars, want %d", len([]rune(e.Text)), MaxNoteChars)
	}
}

func TestAConcurrentBurstAtTwentyNineLeavesExactlyThirty(t *testing.T) {
	l, home := newLedger(t)
	for i := range MaxNotes - 1 {
		mustAdd(t, l, "home", strings.Repeat("n", i+1), "admiralbot")
	}
	var wg sync.WaitGroup
	var ok atomic.Int32
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.AddNote("home", "burst "+string(rune('a'+i)), "admiralbot"); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Errorf("%d burst writes accepted, want exactly 1", ok.Load())
	}
	if n := len(lines(t, home, "home")); n != MaxNotes {
		t.Errorf("ledger has %d lines, want %d", n, MaxNotes)
	}
	if got, _ := New(home, time.Now).Notes("home"); len(got) != MaxNotes {
		t.Errorf("%d notes after a re-read, want %d", len(got), MaxNotes)
	}
}

func TestALedgerDirectoryThatIsALinkIsNotFollowed(t *testing.T) {
	l, home := newLedger(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "chat"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "chat", "home")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.AddNote("home", "escape", "admiralbot"); err == nil {
		t.Error("AddNote through a linked fleet directory succeeded, want a refusal")
	}
	if _, err := os.Stat(filepath.Join(outside, "ledger.jsonl")); err == nil {
		t.Error("a ledger was written through the link")
	}
}

func TestAFleetKeyThatIsNotOneIsRefused(t *testing.T) {
	l, home := newLedger(t)
	for _, fleet := range []string{"../escape", "a/b", "", "has space"} {
		if _, err := l.AddNote(fleet, "x", "admiralbot"); err == nil {
			t.Errorf("AddNote(fleet %q) succeeded, want a refusal", fleet)
		}
		if _, err := l.Notes(fleet); err == nil {
			t.Errorf("Notes(fleet %q) succeeded, want a refusal", fleet)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(home), "escape")); err == nil {
		t.Error("a directory was made outside home")
	}
}
