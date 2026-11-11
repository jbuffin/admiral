package follow

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/github"
)

const parentURL = "https://github.com/acme/widgets/issues/20"

func openStore(t *testing.T, home string) *Store {
	t.Helper()
	s, err := OpenStore(home)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return s
}

func followed(t *testing.T, s *Store) Followed {
	t.Helper()
	f, created, err := s.Follow(Followed{URL: parentURL, Fleet: "work", FollowedAt: time.Unix(1000, 0).UTC()})
	if err != nil || !created || f.ID == "" {
		t.Fatalf("Follow = %+v, created %v, err %v; want a new campaign with an id", f, created, err)
	}
	return f
}

func TestStoreKeepsFollowedCampaignsAcrossRestarts(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	f := followed(t, s)
	override := campaigns.Override{Mode: campaigns.WithYou, Skill: &campaigns.Skill{Kind: "none"}}
	tree := &github.TreeNode{Number: 20, Title: "Campaign", URL: parentURL, Repo: "acme/widgets"}
	readAt := time.Unix(2000, 0).UTC()
	for _, err := range []error{
		s.SetOverride(f.ID, "https://github.com/acme/widgets/issues/21", override),
		s.SetWorkRepo(f.ID, "github.com/acme/work"),
		s.SetRead(f.ID, tree, readAt, ""),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	got, ok := openStore(t, home).Get(f.ID)
	if !ok {
		t.Fatalf("a reopened store lost campaign %s", f.ID)
	}
	o := got.Overrides["https://github.com/acme/widgets/issues/21"]
	if got.URL != parentURL || got.Fleet != "work" || got.WorkRepo != "github.com/acme/work" || o.Mode != campaigns.WithYou || o.Skill == nil || o.Skill.Kind != "none" {
		t.Errorf("reopened campaign = %+v, want its url, fleet, work repo and override kept", got)
	}
	if got.Tree == nil || got.Tree.Number != 20 || !got.ReadAt.Equal(readAt) || got.Ended() {
		t.Errorf("reopened campaign tree/read/ended = %+v %v %v, want the last read kept and not ended", got.Tree, got.ReadAt, got.Ended())
	}
}

func TestStoreWritesAtomically(t *testing.T) {
	home := t.TempDir()
	followed(t, openStore(t, home))
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("home holds %d entries after a write, want only the store file (no temp file left behind)", len(entries))
	}
	for _, e := range entries {
		if b, _ := os.ReadFile(filepath.Join(home, e.Name())); len(b) == 0 {
			t.Errorf("%s is empty", e.Name())
		}
	}
}

func TestFollowingTheSameIssueTwiceReturnsTheFirst(t *testing.T) {
	s := openStore(t, t.TempDir())
	first := followed(t, s)
	again, created, err := s.Follow(Followed{URL: "https://GitHub.com/Acme/Widgets/issues/20/", Fleet: "work"})
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("second Follow = %+v, created %v, err %v; want the first campaign back, not created", again, created, err)
	}
	if n := len(s.List()); n != 1 {
		t.Errorf("List holds %d campaigns, want 1", n)
	}
}

func TestEndedCampaignStaysListedWithItsEndTime(t *testing.T) {
	s := openStore(t, t.TempDir())
	f := followed(t, s)
	at := time.Unix(3000, 0).UTC()
	if err := s.End(f.ID, at); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get(f.ID)
	if !ok || !got.Ended() || !got.EndedAt.Equal(at) {
		t.Errorf("after End: %+v, found %v; want an ended campaign stamped %v", got, ok, at)
	}
	if list := s.List(); len(list) != 1 || !list[0].Ended() {
		t.Errorf("List = %+v, want the ended campaign kept", list)
	}
	// Following an ended campaign's issue again returns it rather than starting a second.
	if again, created, _ := s.Follow(Followed{URL: parentURL, Fleet: "work"}); created && again.ID != f.ID {
		t.Errorf("an ended campaign's issue followed again made %s, want the ended one", again.ID)
	}
}

func TestStoreRefusesWhatIsNotFollowed(t *testing.T) {
	s := openStore(t, t.TempDir())
	if err := s.End("nope", time.Now()); err == nil {
		t.Error("End of an unknown campaign succeeded")
	}
	if err := s.SetWorkRepo("nope", "github.com/a/b"); err == nil {
		t.Error("SetWorkRepo of an unknown campaign succeeded")
	}
}

func TestStoreMovesAnUnreadableFileAsideAndStartsEmpty(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "campaigns.json"), []byte(`{"Campaigns": [`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := openStore(t, home)
	if got := s.List(); len(got) != 0 {
		t.Errorf("List = %v, want none from an unreadable file", got)
	}
	if _, err := os.Stat(filepath.Join(home, "campaigns.json.bad")); err != nil {
		t.Errorf("the unreadable file was not kept as campaigns.json.bad: %v", err)
	}
	followed(t, s)
	if got := openStore(t, home).List(); len(got) != 1 {
		t.Errorf("after a restart List = %d campaigns, want the one followed after the reset", len(got))
	}
}
