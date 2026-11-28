package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSetDefaultFleetRefusesAFileAnotherProcessChangedUnderIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	const start = `{"version":1,"fleets":{"alpha":{"name":"Alpha","claude":{}},"bravo":{"name":"Bravo","claude":{}}}}`
	const theirs = `{"version":1,"x_theirs":true,"fleets":{"alpha":{"name":"Alpha","claude":{}},"bravo":{"name":"Bravo","claude":{}}}}`
	if err := os.WriteFile(path, []byte(start), 0o600); err != nil {
		t.Fatal(err)
	}
	testBeforeDefaultFleetWrite = func() {
		if err := os.WriteFile(path, []byte(theirs), 0o600); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { testBeforeDefaultFleetWrite = nil })

	err := SetDefaultFleet(dir, "bravo")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if got, _ := os.ReadFile(path); string(got) != theirs {
		t.Errorf("the other writer's file was overwritten:\n%s", got)
	}
}

func TestSetDefaultFleetOnAnUnreadableFileIsNotAConflict(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	err := SetDefaultFleet(dir, "alpha")
	if err == nil || errors.Is(err, ErrConflict) || errors.Is(err, ErrNoFleet) {
		t.Errorf("err = %v, want a plain read error", err)
	}
}
