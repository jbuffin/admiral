package ships

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// originReader fakes the origin read: it answers from slugs and counts the reads per directory.
type originReader struct {
	mu    sync.Mutex
	slugs map[string]string
	calls map[string]int
}

func (r *originReader) read(dir string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	r.calls[dir]++
	return r.slugs[dir]
}

func (r *originReader) count(dir string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[dir]
}

func liveSession(name, dir string) sessionhost.Session {
	return sessionhost.Session{Name: name, Options: map[string]string{"name": name, "fleet": "work", "dir": dir}}
}

func TestALiveShipCarriesItsCheckoutsOriginRepo(t *testing.T) {
	f := mooredService(t, nil)
	r := &originReader{slugs: map[string]string{"/src/widgets": "acme/widgets"}}
	f.s.originSlug = r.read
	if got := f.s.ship(liveSession("hood", "/src/widgets")).OriginRepo; got != "acme/widgets" {
		t.Errorf("origin_repo = %q, want acme/widgets", got)
	}
}

func TestALiveShipWithAWorktreeReadsTheOriginOfTheWorktree(t *testing.T) {
	f := mooredService(t, nil)
	made := filepath.Join(f.home, "worktrees", "hood")
	if err := os.MkdirAll(made, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &originReader{slugs: map[string]string{made: "acme/fork", "/src/widgets": "acme/widgets"}}
	f.s.originSlug = r.read
	if got := f.s.ship(liveSession("hood", "/src/widgets")).OriginRepo; got != "acme/fork" {
		t.Errorf("origin_repo = %q, want the worktree's acme/fork", got)
	}
}

func TestAMooredShipCarriesItsCheckoutsOriginRepo(t *testing.T) {
	f := mooredService(t, nil)
	if err := moor.Write(f.home, moor.Record{Name: "Hood", Session: "hood", Fleet: "work", Checkout: "/src/widgets"}); err != nil {
		t.Fatal(err)
	}
	r := &originReader{slugs: map[string]string{"/src/widgets": "acme/widgets"}}
	f.s.originSlug = r.read
	got := f.s.mooredShips(nil)
	if len(got) != 1 || got[0].OriginRepo != "acme/widgets" {
		t.Fatalf("moored ships = %+v, want one with origin_repo acme/widgets", got)
	}
}

func TestTheOriginIsReadOncePerDirectoryAcrossShipsAndLists(t *testing.T) {
	f := mooredService(t, nil)
	for _, n := range []string{"hood", "nelson"} {
		if err := moor.Write(f.home, moor.Record{Name: n, Session: n, Fleet: "work", Checkout: "/src/widgets"}); err != nil {
			t.Fatal(err)
		}
	}
	r := &originReader{slugs: map[string]string{"/src/widgets": "acme/widgets"}}
	f.s.originSlug = r.read
	for range 3 {
		f.s.ship(liveSession("rodney", "/src/widgets"))
		f.s.mooredShips(nil)
	}
	if n := r.count("/src/widgets"); n != 1 {
		t.Errorf("origin read %d times for one directory, want 1", n)
	}
}

func TestADirectoryWithNoOriginIsNotReadAgainEither(t *testing.T) {
	f := mooredService(t, nil)
	r := &originReader{}
	f.s.originSlug = r.read
	for range 3 {
		f.s.ship(liveSession("hood", "/src/none"))
	}
	if n := r.count("/src/none"); n != 1 {
		t.Errorf("origin read %d times for a directory with no origin, want 1", n)
	}
}

func TestAShipWithNoGitHubOriginOmitsOriginRepo(t *testing.T) {
	f := mooredService(t, nil)
	f.s.originSlug = (&originReader{slugs: map[string]string{"/src/widgets": "acme/widgets"}}).read
	if _, ok := shipJSON(t, f.s.ship(liveSession("rodney", "/src/widgets")))["origin_repo"]; !ok {
		t.Error("a ship with an origin has no origin_repo in the JSON")
	}
	for name, sh := range map[string]Ship{"no origin": f.s.ship(liveSession("hood", "/src/none")), "no dir": f.s.ship(liveSession("nelson", ""))} {
		if _, ok := shipJSON(t, sh)["origin_repo"]; ok {
			t.Errorf("%s ship: origin_repo is in the JSON, want it omitted", name)
		}
	}
}

func TestOriginRepoIsReadFromARealCheckoutsOriginRemote(t *testing.T) {
	f := mooredService(t, nil)
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://github.com/acme/widgets.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if got := f.s.ship(liveSession("hood", dir)).OriginRepo; got != "acme/widgets" {
		t.Errorf("origin_repo = %q, want acme/widgets", got)
	}
}
