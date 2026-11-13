package digest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func texts(f File) []string {
	var out []string
	for _, l := range f.Lines {
		out = append(out, l.Text)
	}
	return out
}

func read(t *testing.T, home, fleet string) File {
	t.Helper()
	f, err := Read(home, fleet)
	if err != nil {
		t.Fatalf("Read(%s): %v", fleet, err)
	}
	return f
}

func seqAt(t *testing.T, f File, i int) uint64 {
	t.Helper()
	if i >= len(f.Lines) {
		t.Fatalf("digest has %d lines, want one at index %d: %+v", len(f.Lines), i, f)
	}
	return f.Lines[i].Seq
}

func TestADigestNumbersItsLinesInIncreasingOrder(t *testing.T) {
	home := t.TempDir()
	if f := read(t, home, "home"); len(f.Lines) != 0 || f.NextSeq != 0 {
		t.Fatalf("a fleet with no digest reads %+v, want none", f)
	}
	if err := Add(home, "home", "ship a moored", "ship b anchored"); err != nil {
		t.Fatal(err)
	}
	if err := Add(home, "home", "inbox item appeared"); err != nil {
		t.Fatal(err)
	}
	f := read(t, home, "home")
	if got := texts(f); strings.Join(got, "|") != "ship a moored|ship b anchored|inbox item appeared" {
		t.Fatalf("lines = %q, want all three in order", got)
	}
	for i := 1; i < len(f.Lines); i++ {
		if f.Lines[i].Seq <= f.Lines[i-1].Seq {
			t.Errorf("sequence %d follows %d, want increasing", f.Lines[i].Seq, f.Lines[i-1].Seq)
		}
	}
	if seqAt(t, f, 0) == 0 {
		t.Errorf("the first line has sequence 0, which a pane with none recorded already counts as read")
	}
	if got := read(t, home, "work"); len(got.Lines) != 0 {
		t.Errorf("work's digest = %+v, want none: fleets are separate", got)
	}
}

func TestAckDropsOnlyTheLinesAtOrBelowItsSequence(t *testing.T) {
	home := t.TempDir()
	_ = Add(home, "home", "one", "two")
	seen := seqAt(t, read(t, home, "home"), 1)
	// A line the server wrote after the hook read.
	_ = Add(home, "home", "three")
	if err := Ack(home, "home", seen); err != nil {
		t.Fatal(err)
	}
	if got := texts(read(t, home, "home")); strings.Join(got, "|") != "three" {
		t.Errorf("lines after Ack = %q, want only the line written after the read", got)
	}
}

func TestSequencesNeverGoBackwardsAcrossAnEmptiedDigest(t *testing.T) {
	home := t.TempDir()
	_ = Add(home, "home", "one", "two")
	last := seqAt(t, read(t, home, "home"), 1)
	if err := Ack(home, "home", last); err != nil {
		t.Fatal(err)
	}
	f := read(t, home, "home")
	if len(f.Lines) != 0 || f.NextSeq <= last {
		t.Fatalf("emptied digest = %+v, want no lines and next_seq above %d", f, last)
	}
	raw, err := os.ReadFile(filepath.Join(home, "chat", "home", "digest.json"))
	if err != nil || !strings.Contains(string(raw), `"next_seq"`) {
		t.Errorf("digest.json = %q (%v), want the file kept with next_seq", raw, err)
	}
	_ = Add(home, "home", "three")
	if got := seqAt(t, read(t, home, "home"), 0); got <= last {
		t.Errorf("a new line has sequence %d after %d was acknowledged, want a higher one", got, last)
	}
}

func TestADigestHoldsAtMostTwentyLines(t *testing.T) {
	home := t.TempDir()
	for i := 1; i <= 25; i++ {
		_ = Add(home, "home", fmt.Sprintf("event %02d", i))
	}
	f := read(t, home, "home")
	got := texts(f)
	if len(got) != MaxLines || got[0] != "event 06" || got[len(got)-1] != "event 25" {
		t.Errorf("lines = %q, want the newest %d, event 06 to event 25", got, MaxLines)
	}
	if f.Dropped != 5 {
		t.Errorf("dropped = %d, want 5", f.Dropped)
	}
}

func TestADigestHoldsAtMostTwoThousandBytesOfText(t *testing.T) {
	home := t.TempDir()
	for i := 1; i <= 10; i++ {
		_ = Add(home, "home", fmt.Sprintf("event %02d %s", i, strings.Repeat("x", 380)))
	}
	f := read(t, home, "home")
	total := 0
	for _, l := range f.Lines {
		total += len(l.Text)
	}
	if total > MaxBytes {
		t.Errorf("lines hold %d bytes, want at most %d", total, MaxBytes)
	}
	got := texts(f)
	if len(got) == 0 || !strings.HasPrefix(got[len(got)-1], "event 10") {
		t.Errorf("lines = %d, want the newest kept", len(got))
	}
	if f.Dropped != 10-len(got) || f.Dropped == 0 {
		t.Errorf("dropped = %d with %d lines kept, want %d", f.Dropped, len(got), 10-len(got))
	}
}
