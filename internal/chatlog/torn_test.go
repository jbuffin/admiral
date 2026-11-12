package chatlog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestATornLastLineDoesNotSwallowTheNextEntry(t *testing.T) {
	c := &clock{}
	c.at("2026-11-11T10:00:00Z")
	l, home := newLog(t, c)
	appendOK(t, l, "harbor", msg("a", "user", "first"))
	f, err := os.OpenFile(filepath.Join(home, "chat", "harbor", "conversation.jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"torn","kind":"mess`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// A new process: the torn line is on disk when it first appends.
	l = New(home, func() time.Time { return c.now })
	appendOK(t, l, "harbor", msg("b", "user", "second"))
	got, err := New(home, func() time.Time { return c.now }).Recent("harbor", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !same(ids(got), []string{"a", "b"}) {
		t.Errorf("entries = %v, want [a b]: the torn line must not take b with it", ids(got))
	}
}
