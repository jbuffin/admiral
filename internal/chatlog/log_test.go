package chatlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type clock struct{ now time.Time }

func (c *clock) at(s string) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	c.now = t
}

func newLog(t *testing.T, c *clock) (*Log, string) {
	t.Helper()
	home := t.TempDir()
	return New(home, func() time.Time { return c.now }), home
}

func msg(id, role, text string) Entry { return Entry{ID: id, Kind: "message", Role: role, Text: text} }

func ids(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func same(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func appendOK(t *testing.T, l *Log, fleet string, e Entry) bool {
	t.Helper()
	added, err := l.Append(fleet, e)
	if err != nil {
		t.Fatalf("Append(%s, %s): %v", fleet, e.ID, err)
	}
	return added
}

func recent(t *testing.T, l *Log, fleet string, n int) []Entry {
	t.Helper()
	es, err := l.Recent(fleet, n)
	if err != nil {
		t.Fatalf("Recent(%s, %d): %v", fleet, n, err)
	}
	return es
}

func TestAppendedEntriesComeBackOldestFirstFromTheFleetsFile(t *testing.T) {
	c := &clock{}
	c.at("2026-11-03T10:00:00Z")
	l, home := newLog(t, c)
	appendOK(t, l, "work", msg("u-1", "user", "hello"))
	appendOK(t, l, "work", msg("a-1", "assistant", "hi"))
	appendOK(t, l, "work", Entry{ID: "reset-1", Kind: "reset", Text: "New chat"})

	got := recent(t, l, "work", 50)
	if !same(ids(got), []string{"u-1", "a-1", "reset-1"}) || got[0].Text != "hello" || got[1].Role != "assistant" || got[2].Kind != "reset" {
		t.Errorf("Recent = %+v, want the three entries oldest first with their fields", got)
	}
	if raw, err := os.ReadFile(filepath.Join(home, "chat", "work", "conversation.jsonl")); err != nil || strings.Count(string(raw), "\n") != 3 {
		t.Errorf("chat/work/conversation.jsonl = %q, %v, want one line per entry", raw, err)
	}
}

func TestAnEntryWithTheSameIDIsAddedOnceEvenAfterARestart(t *testing.T) {
	c := &clock{}
	c.at("2026-11-03T10:00:00Z")
	l, home := newLog(t, c)
	if !appendOK(t, l, "work", msg("u-1", "user", "hello")) {
		t.Fatalf("the first Append of u-1 reported it was not added")
	}
	if appendOK(t, l, "work", msg("u-1", "user", "hello")) {
		t.Errorf("a second Append of u-1 reported it was added")
	}
	again := New(home, func() time.Time { return c.now })
	if appendOK(t, again, "work", msg("u-1", "user", "hello")) {
		t.Errorf("an Append of u-1 through a new Log (an Admiral restart) reported it was added")
	}
	if got := recent(t, again, "work", 50); !same(ids(got), []string{"u-1"}) {
		t.Errorf("Recent = %v, want u-1 once", ids(got))
	}
}

func TestTheFileRotatesMonthlyAndOldMonthsStay(t *testing.T) {
	c := &clock{}
	c.at("2026-10-31T23:59:59Z")
	l, home := newLog(t, c)
	appendOK(t, l, "work", msg("oct", "user", "in October"))
	c.at("2026-11-01T00:00:00Z")
	appendOK(t, l, "work", msg("nov", "user", "in November"))
	c.at("2026-12-15T09:00:00Z")
	appendOK(t, l, "work", msg("dec", "user", "in December"))

	dir := filepath.Join(home, "chat", "work")
	for file, want := range map[string]string{
		"conversation-2026-10.jsonl": "oct",
		"conversation-2026-11.jsonl": "nov",
		"conversation.jsonl":         "dec",
	} {
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Errorf("%s: %v, want the month's file kept", file, err)
			continue
		}
		if !strings.Contains(string(raw), `"`+want+`"`) || strings.Count(string(raw), "\n") != 1 {
			t.Errorf("%s = %q, want only %s", file, raw, want)
		}
	}
	if got := recent(t, l, "work", 50); !same(ids(got), []string{"oct", "nov", "dec"}) {
		t.Errorf("Recent = %v, want all three months oldest first", ids(got))
	}
	// A re-read transcript offers October's entry again after the rotations.
	again := New(home, func() time.Time { return c.now })
	if appendOK(t, again, "work", msg("oct", "user", "in October")) {
		t.Errorf("an entry of a rotated month was added a second time")
	}
}

func TestRecentIsBoundedToTheLastEntriesAcrossMonths(t *testing.T) {
	c := &clock{}
	c.at("2026-10-30T10:00:00Z")
	l, _ := newLog(t, c)
	appendOK(t, l, "work", msg("1", "user", "one"))
	appendOK(t, l, "work", msg("2", "user", "two"))
	c.at("2026-11-02T10:00:00Z")
	appendOK(t, l, "work", msg("3", "user", "three"))
	appendOK(t, l, "work", msg("4", "user", "four"))

	if got := recent(t, l, "work", 3); !same(ids(got), []string{"2", "3", "4"}) {
		t.Errorf("Recent(3) = %v, want the last three, from both months", ids(got))
	}
	if got := recent(t, l, "work", 0); len(got) != 0 {
		t.Errorf("Recent(0) = %v, want none", ids(got))
	}
}

func TestFleetsKeepSeparateLogs(t *testing.T) {
	c := &clock{}
	c.at("2026-11-03T10:00:00Z")
	l, home := newLog(t, c)
	appendOK(t, l, "alpha", msg("same-id", "user", "for alpha"))
	if !appendOK(t, l, "beta", msg("same-id", "user", "for beta")) {
		t.Errorf("beta's entry with alpha's id was taken for a duplicate")
	}
	if got := recent(t, l, "alpha", 50); len(got) != 1 || got[0].Text != "for alpha" {
		t.Errorf("alpha's Recent = %+v, want only its own entry", got)
	}
	if got := recent(t, l, "beta", 50); len(got) != 1 || got[0].Text != "for beta" {
		t.Errorf("beta's Recent = %+v, want only its own entry", got)
	}
	if got := recent(t, l, "gamma", 50); len(got) != 0 {
		t.Errorf("a fleet with no log has %+v, want none", got)
	}
	if _, err := os.Stat(filepath.Join(home, "chat", "gamma")); err == nil {
		t.Errorf("reading a fleet with no log created chat/gamma")
	}
}

func TestNothingIsWrittenOutsideTheFleetsDirectory(t *testing.T) {
	c := &clock{}
	c.at("2026-11-03T10:00:00Z")
	l, home := newLog(t, c)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "chat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "chat", "linked")); err != nil {
		t.Fatal(err)
	}

	for _, fleet := range []string{"linked", "../escape", "a/b", ""} {
		if _, err := l.Append(fleet, msg("x", "user", "nope")); err == nil {
			t.Errorf("Append to fleet %q succeeded, want an error", fleet)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("the link's target holds %d files, want none", len(entries))
	}
	if _, err := os.Stat(filepath.Join(home, "escape")); err == nil {
		t.Errorf("a fleet key with .. wrote outside chat/")
	}
}
