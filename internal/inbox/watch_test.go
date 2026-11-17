package inbox_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/server"
)

// A ship's first item is listed however it lands against the watcher's start: the session directory can appear
// while the first refresh is adding watches, and nothing is raised for what is written into it afterwards.
func TestAnItemInANewSessionDirectoryIsListedWhateverItsTimingAgainstTheFirstRefresh(t *testing.T) {
	for i := range 40 {
		home := t.TempDir()
		s := &inbox.Service{Home: home, Hub: server.NewHub()}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		time.Sleep(time.Duration(i%6) * 500 * time.Microsecond)
		id := fmt.Sprintf("q-%d", i)
		dir := filepath.Join(home, "inbox", "some-ship")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(i%4) * 200 * time.Microsecond)
		writeItem(t, dir, id+".json", question(id, "2026-10-22T10:00:00Z"))
		deadline := time.Now().Add(3 * time.Second)
		for len(s.List()) == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("round %d: %s was never listed", i, id)
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		<-done
	}
}
