package findings

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// What the store keeps of a ship's findings after it sailed off (issue 735, unit u1).

const (
	first1 = "2026-11-18T09:30:00Z"
	first2 = "2026-11-19T14:05:07+02:00"
)

type rig struct {
	t     *testing.T
	home  string
	store *Store
	n     int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	home := t.TempDir()
	return &rig{t: t, home: home, store: Open(home)}
}

// file writes a findings file as a ship would, outside the store.
func (r *rig) file(body string) string {
	r.t.Helper()
	r.n++
	p := filepath.Join(r.home, "reports", "ship"+string(rune('a'+r.n))+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
	return p
}

func (r *rig) keep(ship, first, fleet, path string) bool {
	r.t.Helper()
	kept, err := r.store.Keep(ship, first, fleet, path)
	if err != nil {
		r.t.Fatalf("Keep(%q, %q): %v", ship, first, err)
	}
	return kept
}

func (r *rig) mustKeep(ship, first, fleet, body string) {
	r.t.Helper()
	if !r.keep(ship, first, fleet, r.file(body)) {
		r.t.Fatalf("Keep(%q, %q) kept nothing", ship, first)
	}
}

func TestAKeptVoyageIsReadBackWithItsMarkdownAndFleetUnderItsOwnFirstLaunch(t *testing.T) {
	r := newRig(t)
	r.mustKeep("dory", first1, "work", "# Found\n\nthe race is in the cache\n")
	got, ok := r.store.Voyage("dory", first1)
	want := Voyage{Ship: "dory", FirstLaunched: first1, Fleet: "work", Markdown: "# Found\n\nthe race is in the cache\n"}
	if !ok || got != want {
		t.Errorf("Voyage = %+v, %v, want %+v", got, ok, want)
	}
	if _, ok := r.store.Voyage("dory", first2); ok {
		t.Error("another first launch of the same name read this voyage's findings")
	}
	if _, ok := r.store.Voyage("kestrel", first1); ok {
		t.Error("another ship read this voyage's findings")
	}
}

func TestFindingsAreKeptIndefinitelyAcrossAnAdmiralRestart(t *testing.T) {
	r := newRig(t)
	r.mustKeep("dory", first1, "work", "kept\n")
	if got, ok := Open(r.home).Voyage("dory", first1); !ok || got.Markdown != "kept\n" {
		t.Errorf("a new store on the same home read %+v, %v, want the kept findings", got, ok)
	}
}

func TestAReusedShipNameKeepsEachVoyagesFindingsSeparateAndLatestIsTheNewestFirstLaunch(t *testing.T) {
	r := newRig(t)
	// The newer voyage is kept first, so the newest first launch wins over the order of keeping.
	r.mustKeep("dory", first2, "play", "second voyage\n")
	r.mustKeep("dory", first1, "work", "first voyage\n")
	if got, ok := r.store.Voyage("dory", first1); !ok || got.Markdown != "first voyage\n" || got.Fleet != "work" {
		t.Errorf("old voyage = %+v, %v, want its own findings and fleet", got, ok)
	}
	latest, ok := r.store.Latest("dory")
	if !ok || latest.FirstLaunched != first2 || latest.Markdown != "second voyage\n" || latest.Fleet != "play" {
		t.Errorf("Latest = %+v, %v, want the voyage first launched %s", latest, ok, first2)
	}
	if _, ok := r.store.Latest("kestrel"); ok {
		t.Error("Latest found findings for a ship that left none")
	}
}

func TestALaterRoundThatWritesFindingsReplacesTheVoyagesCopyAndOneThatWritesNoneLeavesIt(t *testing.T) {
	r := newRig(t)
	r.mustKeep("dory", first1, "work", "round one\n")
	if r.keep("dory", first1, "work", filepath.Join(r.home, "reports", "gone.md")) {
		t.Error("Keep reported keeping a file that does not exist")
	}
	if got, _ := r.store.Voyage("dory", first1); got.Markdown != "round one\n" {
		t.Errorf("a round with no findings file left %q, want round one's findings", got.Markdown)
	}
	if r.keep("dory", first1, "work", r.file("")) {
		t.Error("Keep reported keeping an empty file")
	}
	if got, _ := r.store.Voyage("dory", first1); got.Markdown != "round one\n" {
		t.Errorf("a round with an empty findings file left %q, want round one's findings", got.Markdown)
	}
	r.mustKeep("dory", first1, "work", "round two\n")
	if got, _ := r.store.Voyage("dory", first1); got.Markdown != "round two\n" {
		t.Errorf("after round two the voyage holds %q, want round two's findings", got.Markdown)
	}
}

func TestAFileIsKeptUpToOneMebibyteAndNotAByteOver(t *testing.T) {
	r := newRig(t)
	if !r.keep("dory", first1, "work", r.file(strings.Repeat("x", 1<<20))) {
		t.Error("a file of exactly 1 MiB was not kept")
	}
	if r.keep("kestrel", first1, "work", r.file(strings.Repeat("x", 1<<20+1))) {
		t.Error("a file of 1 MiB and a byte was kept")
	}
	if _, ok := r.store.Voyage("kestrel", first1); ok {
		t.Error("the store holds findings for a file over the cap")
	}
}

func TestAFileThatIsASymlinkOrAPipeIsNotKeptAndKeepReturns(t *testing.T) {
	r := newRig(t)
	r.mustKeep("control", first1, "work", "a regular file is kept\n")
	secret := r.file("the engineer's secret\n")
	link := filepath.Join(r.home, "reports", "link.md")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(r.home, "reports", "pipe.md")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"symlink": link, "FIFO": fifo} {
		done := make(chan bool, 1)
		go func() {
			kept, err := r.store.Keep("dory-"+name, first1, "work", path)
			done <- kept && err == nil
		}()
		select {
		case kept := <-done:
			if kept {
				t.Errorf("a %s was kept", name)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Keep blocked on a %s", name)
		}
		if _, ok := r.store.Voyage("dory-"+name, first1); ok {
			t.Errorf("the store holds findings read through a %s", name)
		}
	}
}

func TestAHostileShipNameCannotWriteOrReadOutsideTheStoreAndNamesThatLookAlikeStayApart(t *testing.T) {
	r := newRig(t)
	outside := filepath.Join(r.home, "outside.md")
	if err := os.WriteFile(outside, []byte("not findings\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ship := range []string{"../evil", "../../evil", "a/b", `a\b`, "/abs", ".", "..", "", "x\x00y"} {
		kept, _ := r.store.Keep(ship, first1, "work", r.file("hostile\n"))
		if got, ok := r.store.Voyage(ship, first1); ok && !kept {
			t.Errorf("Voyage(%q) found %+v though Keep kept nothing", ship, got)
		}
	}
	for _, first := range []string{"../evil", "a/b", ""} {
		r.store.Keep("dory", first, "work", r.file("hostile\n"))
	}
	err := filepath.WalkDir(r.home, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(r.home, p)
		if strings.HasPrefix(rel, "reports"+string(filepath.Separator)) || rel == "outside.md" || strings.HasPrefix(rel, "findings"+string(filepath.Separator)) {
			return nil
		}
		t.Errorf("Keep left %s outside the store", rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "not findings\n" {
		t.Errorf("a hostile name overwrote a file outside the store: %q", b)
	}
	// Names that a naive encoding would fold together are different voyages.
	r.mustKeep("a/b", first1, "work", "slash\n")
	r.mustKeep("a_b", first1, "work", "underscore\n")
	r.mustKeep("a%2Fb", first1, "work", "percent\n")
	for ship, want := range map[string]string{"a/b": "slash\n", "a_b": "underscore\n", "a%2Fb": "percent\n"} {
		if got, ok := r.store.Voyage(ship, first1); !ok || got.Markdown != want {
			t.Errorf("Voyage(%q) = %+v, %v, want %q", ship, got, ok, want)
		}
	}
}

func TestTheStoreKeepsFilesPrivateLikeTheOtherHomeStores(t *testing.T) {
	r := newRig(t)
	r.mustKeep("dory", first1, "work", "private\n")
	var files, dirs int
	err := filepath.WalkDir(filepath.Join(r.home, "findings"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir() && fi.Mode().Perm() != 0o700:
			t.Errorf("directory %s has mode %v, want 0700", p, fi.Mode().Perm())
		case !d.IsDir() && fi.Mode().Perm() != 0o600:
			t.Errorf("file %s has mode %v, want 0600", p, fi.Mode().Perm())
		}
		if d.IsDir() {
			dirs++
		} else {
			files++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the store has no findings directory under the home: %v", err)
	}
	if files == 0 {
		t.Error("the store wrote no file under findings/")
	}
	_ = dirs
}
