package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloseWritesThePendingChangeAndNothingAfterIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.SetLastDir("work", "claude", "/src/widgets")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "/src/widgets") {
		t.Fatalf("state.json after Close = %q, %v; want the change written", raw, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s.SetLastDir("work", "claude", "/src/gadgets")
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("state.json was written after Close: %v", err)
	}
}
