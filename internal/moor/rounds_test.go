package moor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func round(n int) Round {
	at := time.Date(2026, 11, 1, 10, n, 0, 0, time.UTC)
	return Round{Round: n, SessionID: "0f8fad5b-d9cb-469f-a165-70867728950e", Fleet: "work",
		Transcript: "/cfg/projects/zz/0f8fad5b-d9cb-469f-a165-70867728950e.jsonl", Started: at, Ended: at.Add(time.Minute)}
}

func roundsFile(home, name string) string {
	return filepath.Join(home, "moored", "rounds", name+".json")
}

func TestAppendedRoundsReadBackOldestFirstFromTheShipsOwnFile(t *testing.T) {
	home := t.TempDir()
	for _, n := range []int{0, 1} {
		if err := AppendRound(home, "Nelson", round(n)); err != nil {
			t.Fatalf("AppendRound %d: %v", n, err)
		}
	}
	if _, err := os.Stat(roundsFile(home, "Nelson")); err != nil {
		t.Errorf("the history is not at <home>/moored/rounds/<name>.json: %v", err)
	}
	got, err := ReadRounds(home, "Nelson")
	if err != nil {
		t.Fatalf("ReadRounds: %v", err)
	}
	want := []Round{round(0), round(1)}
	if len(got) != 2 || !reflect.DeepEqual(got, want) {
		t.Errorf("ReadRounds = %+v, want %+v", got, want)
	}
}

func TestARoundAlreadyInTheHistoryIsNotAppendedAgain(t *testing.T) {
	home := t.TempDir()
	if err := AppendRound(home, "Nelson", round(0)); err != nil {
		t.Fatal(err)
	}
	if err := AppendRound(home, "Nelson", round(0)); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadRounds(home, "Nelson"); len(got) != 1 {
		t.Errorf("ReadRounds = %+v after appending round 0 twice, want one round", got)
	}
}

func TestTheHistoryKeepsTheLatestFiftyRounds(t *testing.T) {
	home := t.TempDir()
	for n := 0; n < MaxRounds+3; n++ {
		if err := AppendRound(home, "Nelson", round(n)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadRounds(home, "Nelson")
	if err != nil || len(got) != MaxRounds || got[0].Round != 3 || got[MaxRounds-1].Round != MaxRounds+2 {
		t.Errorf("ReadRounds = %d rounds from %d to %d (%v), want %d rounds from 3 to %d",
			len(got), first(got), last(got), err, MaxRounds, MaxRounds+2)
	}
}

func first(rs []Round) int {
	if len(rs) == 0 {
		return -1
	}
	return rs[0].Round
}

func last(rs []Round) int {
	if len(rs) == 0 {
		return -1
	}
	return rs[len(rs)-1].Round
}

func TestRemovingTheHistoryLeavesNoFileAndAMissingOneIsNoError(t *testing.T) {
	home := t.TempDir()
	if err := AppendRound(home, "Nelson", round(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(roundsFile(home, "Nelson")); err != nil {
		t.Fatalf("no history to remove: %v", err)
	}
	if err := RemoveRounds(home, "Nelson"); err != nil {
		t.Fatalf("RemoveRounds: %v", err)
	}
	if got, _ := ReadRounds(home, "Nelson"); len(got) != 0 {
		t.Errorf("ReadRounds = %+v after RemoveRounds, want none", got)
	}
	if err := RemoveRounds(home, "Nelson"); err != nil {
		t.Errorf("RemoveRounds of a missing history = %v, want no error", err)
	}
}

func TestAHistoryThatIsASymlinkIsRefused(t *testing.T) {
	home := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "x.json")
	if err := os.WriteFile(elsewhere, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "moored", "rounds"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, roundsFile(home, "Nelson")); err != nil {
		t.Fatal(err)
	}
	if err := AppendRound(home, "Nelson", round(0)); err == nil {
		t.Error("AppendRound through a symlinked history = nil, want an error")
	}
	if _, err := ReadRounds(home, "Nelson"); err == nil {
		t.Error("ReadRounds of a symlinked history = nil error, want one")
	}
	if b, _ := os.ReadFile(elsewhere); string(b) != "[]" {
		t.Errorf("the symlink's target now holds %q, want it untouched", b)
	}
}

func TestTheRoundsDirectoryIsNotAMooringRecord(t *testing.T) {
	home := t.TempDir()
	if err := Write(home, Record{Name: "Nelson", Session: "nelson"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendRound(home, "Nelson", round(0)); err != nil {
		t.Fatalf("AppendRound: %v", err)
	}
	if _, err := os.Stat(roundsFile(home, "Nelson")); err != nil {
		t.Fatalf("no history written: %v", err)
	}
	recs, err := List(home)
	if err != nil || len(recs) != 1 || recs[0].Name != "Nelson" {
		t.Errorf("List = %+v, %v; want the one record", recs, err)
	}
	if err := Remove(home, "Nelson"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadRounds(home, "Nelson"); len(got) != 1 {
		t.Errorf("Remove took the history with it: %+v; a wake drops the record and the history must outlive it", got)
	}
}

func TestARoundOfAnotherSessionReplacesAnOrphanedHistory(t *testing.T) {
	home := t.TempDir()
	for _, n := range []int{0, 1} {
		if err := AppendRound(home, "Nelson", round(n)); err != nil {
			t.Fatal(err)
		}
	}
	fresh := round(0)
	fresh.SessionID = "1f8fad5b-d9cb-469f-a165-70867728950e"
	if err := AppendRound(home, "Nelson", fresh); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadRounds(home, "Nelson"); !reflect.DeepEqual(got, []Round{fresh}) {
		t.Errorf("ReadRounds = %+v, want only the new ship's round 0", got)
	}
}
