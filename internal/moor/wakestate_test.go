package moor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func sampleWake() WakeState {
	return WakeState{PRs: map[string]PRWake{wpr1: {
		Cursor:     Cursor{At: wEv, IDs: []string{"R1", "C2"}},
		RoundStart: wNow,
		Snapshot:   Snapshot{Head: "abc", Failing: []string{"CI / test"}, Conflicting: true},
		Seen:       map[string]time.Time{"conflict": wNow},
	}}}
}

func TestWakeStateRoundTripsAtItsOwnPath(t *testing.T) {
	home := t.TempDir()
	want := sampleWake()
	if err := WriteWake(home, "Nelson", want); err != nil {
		t.Fatalf("WriteWake: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "moored", "wake", "Nelson.json")); err != nil {
		t.Errorf("the state is not at <home>/moored/wake/<name>.json: %v", err)
	}
	got, err := ReadWake(home, "Nelson")
	if err != nil {
		t.Fatalf("ReadWake: %v", err)
	}
	g := got.PRs[wpr1]
	w := want.PRs[wpr1]
	if !g.Cursor.At.Equal(w.Cursor.At) || !reflect.DeepEqual(g.Cursor.IDs, w.Cursor.IDs) || !g.RoundStart.Equal(w.RoundStart) || !reflect.DeepEqual(g.Snapshot, w.Snapshot) || !g.Seen["conflict"].Equal(w.Seen["conflict"]) {
		t.Errorf("read back %+v, want %+v", g, w)
	}
}

func TestAShipWithNoWakeStateReadsAsZero(t *testing.T) {
	got, err := ReadWake(t.TempDir(), "Nelson")
	if err != nil || len(got.PRs) != 0 {
		t.Errorf("ReadWake = %+v, %v; want the zero state and no error", got, err)
	}
}

func TestTheWakeStateIsNotAMooringRecord(t *testing.T) {
	home := t.TempDir()
	if err := Write(home, Record{Name: "Nelson", Session: "nelson"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteWake(home, "Nelson", sampleWake()); err != nil {
		t.Fatalf("WriteWake: %v", err)
	}
	recs, err := List(home)
	if err != nil || len(recs) != 1 || recs[0].Name != "Nelson" {
		t.Errorf("List = %+v, %v; want the one record", recs, err)
	}
	if err := Remove(home, "Nelson"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "moored", "wake", "Nelson.json")); err != nil {
		t.Errorf("Remove took the wake state with it: %v; a wake drops the record and the state must outlive it", err)
	}
}

func TestWritingTheWakeStateReplacesItWholeAndLeavesNoTempFile(t *testing.T) {
	home := t.TempDir()
	if err := WriteWake(home, "Nelson", sampleWake()); err != nil {
		t.Fatalf("WriteWake: %v", err)
	}
	if err := WriteWake(home, "Nelson", WakeState{}); err != nil {
		t.Fatalf("WriteWake: %v", err)
	}
	got, err := ReadWake(home, "Nelson")
	if err != nil || len(got.PRs) != 0 {
		t.Errorf("after an overwrite: %+v, %v; want it replaced, not merged", got, err)
	}
	entries, _ := os.ReadDir(filepath.Join(home, "moored", "wake"))
	if len(entries) != 1 {
		t.Errorf("the wake directory holds %d entries, want only Nelson.json", len(entries))
	}
}

func TestWriteWakeRefusesASymlink(t *testing.T) {
	home := t.TempDir()
	dst := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(dst, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "moored", "wake"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dst, filepath.Join(home, "moored", "wake", "Nelson.json")); err != nil {
		t.Fatal(err)
	}
	if err := WriteWake(home, "Nelson", sampleWake()); err == nil {
		t.Error("WriteWake through a symlinked file succeeded")
	}
	if b, _ := os.ReadFile(dst); string(b) != "untouched" {
		t.Errorf("the symlink's target was written: %q", b)
	}
	if _, err := ReadWake(home, "Nelson"); err == nil {
		t.Error("ReadWake followed a symlink")
	}
}

func TestWriteWakeRefusesASymlinkedDirectory(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "moored"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(home, "moored", "wake")); err != nil {
		t.Fatal(err)
	}
	if err := WriteWake(home, "Nelson", sampleWake()); err == nil {
		t.Error("WriteWake through a symlinked wake directory succeeded")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("something was written through the symlinked directory: %v", entries)
	}
}

func TestAWakeStateNameIsAShipNameNeverAPath(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"", "..", "../x", "a/b"} {
		if err := WriteWake(home, name, sampleWake()); err == nil {
			t.Errorf("WriteWake accepted the name %q", name)
		}
		if _, err := ReadWake(home, name); err == nil {
			t.Errorf("ReadWake accepted the name %q", name)
		}
		if err := RemoveWake(home, name); err == nil {
			t.Errorf("RemoveWake accepted the name %q", name)
		}
	}
}

func TestRemoveWakeDeletesTheStateAndIsFineWithoutOne(t *testing.T) {
	home := t.TempDir()
	if err := RemoveWake(home, "Nelson"); err != nil {
		t.Errorf("RemoveWake with no state: %v", err)
	}
	if err := WriteWake(home, "Nelson", sampleWake()); err != nil {
		t.Fatalf("WriteWake: %v", err)
	}
	if err := RemoveWake(home, "Nelson"); err != nil {
		t.Fatalf("RemoveWake: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "moored", "wake", "Nelson.json")); !os.IsNotExist(err) {
		t.Errorf("the state is still there: %v", err)
	}
}
