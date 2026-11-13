package ships

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

func wakeFile(f *mooredFixture, name string) string {
	return filepath.Join(f.home, "moored", "wake", name+".json")
}

// seedWake writes the file by hand so these tests depend on the ships' behavior, not on WriteWake.
func seedWake(t *testing.T, f *mooredFixture, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(wakeFile(f, name)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wakeFile(f, name), []byte(`{"prs":{}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAWakeKeepsTheShipsWakeState(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, moor.Status{State: "OPEN"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven)
	seedWake(t, f, "Nelson")

	f.s.dropMoored(session, "")
	if len(f.records(t)) != 0 {
		t.Fatal("dropMoored left the record")
	}
	if _, err := os.Stat(wakeFile(f, "Nelson")); err != nil {
		t.Errorf("a wake took the wake state with the record: %v; the next round needs its cursor", err)
	}
}

func TestEndingAMooringForGoodRemovesTheWakeState(t *testing.T) {
	t.Run("dismiss", func(t *testing.T) {
		g := &pollGitHub{}
		g.set(prSeven, moor.Status{State: "OPEN"})
		f := mooredService(t, map[string]*pollGitHub{"work": g})
		session := f.moor(t, "Nelson", "work", prSeven)
		seedWake(t, f, "Nelson")
		if rec := f.post("/api/ships/" + session + "/dismiss"); rec.Code != 200 {
			t.Fatalf("Dismiss = %d %s", rec.Code, rec.Body)
		}
		if _, err := os.Stat(wakeFile(f, "Nelson")); !os.IsNotExist(err) {
			t.Errorf("Dismiss left the wake state: %v", err)
		}
	})
	t.Run("the PR merges", func(t *testing.T) {
		g := &pollGitHub{}
		g.set(prSeven, moor.Status{State: "MERGED"})
		f := mooredService(t, map[string]*pollGitHub{"work": g})
		f.moor(t, "Nelson", "work", prSeven)
		seedWake(t, f, "Nelson")
		f.s.PollMoored(context.Background())
		if len(f.records(t)) != 0 {
			t.Fatal("the poll left the record of a merged PR")
		}
		if _, err := os.Stat(wakeFile(f, "Nelson")); !os.IsNotExist(err) {
			t.Errorf("the poll left the wake state of a ship that sailed off: %v", err)
		}
	})
}
