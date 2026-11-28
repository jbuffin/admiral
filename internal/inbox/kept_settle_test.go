package inbox

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jbuffin/admiral/internal/state"
)

func TestSweepKeptSettlesTheItemsAnsweredWhileAdmiralWasDown(t *testing.T) {
	home := t.TempDir()
	st, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.KeepInbox("s-work", state.KeptShip{Name: "s-work", Fleet: "work", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "inbox", "s-work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"q-1.json":        `{"version":1,"id":"q-1","kind":"question"}`,
		"q-1.answer.json": `{"version":1,"id":"q-1","kind":"question","answers":{}}`,
		"q-2.json":        `{"version":1,"id":"q-2","kind":"question"}`,
		"q-2.answer.json": `{"version":1,"id":"q-2","kind":"question","answered":true,"removed":true}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	got := map[string]bool{}
	s := &Service{Home: home, Kept: st, OnSettled: func(session, id string, removed bool) {
		mu.Lock()
		defer mu.Unlock()
		if session == "s-work" {
			got[id] = removed
		}
	}}
	s.sweepKept()

	if removed, ok := got["q-1"]; !ok || removed {
		t.Errorf("q-1 settled = %v (seen %v), want answered", removed, ok)
	}
	if removed, ok := got["q-2"]; !ok || !removed {
		t.Errorf("q-2 settled removed = %v (seen %v), want removed", removed, ok)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the kept inbox's files were not released: %v", err)
	}
}
