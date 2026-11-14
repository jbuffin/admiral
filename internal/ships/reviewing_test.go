package ships

// A moored ship that reviews a PR serves its role, never wakes or proposes for it, and refuses Merge and Don't follow reviews.

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// moorReviewing moors a ship, a half hour ago, that carries prs and reviews the ones named.
func (f *mooredFixture) moorReviewing(t *testing.T, name, fleet string, reviewing []string, prs ...string) string {
	t.Helper()
	session := strings.ToLower(name)
	rec := moor.Record{Name: name, Session: session, Fleet: fleet, Harness: "claude", Checkout: "/src/widgets", PRs: prs, Reviewing: reviewing,
		Resume: resumeText, MooredAt: time.Now().Add(-30 * time.Minute).UTC().Truncate(time.Second)}
	if err := moor.Write(f.home, rec); err != nil {
		t.Fatal(err)
	}
	return session
}

// byAuthor is the answer for a PR someone else opened, with a bot review, a stranger's comment and a conflict.
func byAuthor() moor.Status {
	st := reviewed()
	st.Author, st.Viewer, st.Mergeable = author, viewer, "CONFLICTING"
	st.Comments = []moor.Comment{strangerComment("C1", "stranger-1", "please rename it", 30*time.Minute)}
	return st
}

func TestAMooredShipNeverWakesOrProposesForAReviewedPR(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, byAuthor())
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moorReviewing(t, "Nelson", "work", []string{prSeven}, prSeven)

	f.s.PollMoored(context.Background())

	if sh, _ := f.ship(t, "Nelson"); sh.Waiting != "" || sh.Status != "moored" {
		t.Errorf("status %q waiting %q, want moored with no wake due", sh.Status, sh.Waiting)
	}
	if got := proposals(t, f.home); len(got) != 0 {
		t.Errorf("proposals = %d, want none for a reviewed PR", len(got))
	}
	if _, err := os.Stat(wakeFile(f, "Nelson")); err == nil {
		t.Error("the reviewed PR got a wake state file, want no cursor and no first-seen time")
	}
}

func TestAReviewedPRRaisesNoWakeButTheShipsOwnPRStillDoes(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, byAuthor())
	own := reviewed()
	own.Author, own.Viewer = viewer, viewer
	g.set(prNine, own)
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moorReviewing(t, "Nelson", "work", []string{prSeven}, prSeven, prNine)

	f.s.PollMoored(context.Background())

	if sh, _ := f.ship(t, "Nelson"); sh.Waiting != "Waiting for Admiral's terminal host" {
		t.Errorf("waiting = %q, want the own PR's wake still due", sh.Waiting)
	}
	if _, ok := f.wakeState(t, "Nelson").PRs[prSeven]; ok {
		t.Errorf("wake state = %+v, want nothing for the reviewed PR", f.wakeState(t, "Nelson"))
	}
	if got := proposals(t, f.home); len(got) != 0 {
		t.Errorf("proposals = %d, want none for the reviewed PR", len(got))
	}
}

func TestAMooredShipServesTheAuthorAndReviewingOfItsPRs(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, moor.Status{State: "OPEN", Author: author, Viewer: viewer})
	own := moor.Status{State: "OPEN", Author: viewer, Viewer: viewer}
	g.set(prNine, own)
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moorReviewing(t, "Nelson", "work", []string{prSeven}, prSeven, prNine)
	f.s.PollMoored(context.Background())

	sh, _ := f.ship(t, "Nelson")
	if len(sh.PRs) != 2 {
		t.Fatalf("prs = %+v, want both", sh.PRs)
	}
	if p := sh.PRs[0]; p.URL != prSeven || p.Author != author || !p.Reviewing {
		t.Errorf("first PR = %+v, want %s by %s, reviewing", p, prSeven, author)
	}
	if p := sh.PRs[1]; p.URL != prNine || p.Author != viewer || p.Reviewing {
		t.Errorf("second PR = %+v, want %s by %s, not reviewing", p, prNine, viewer)
	}
}

func TestMergeAndDontFollowReviewsRefuseAShipWithAReviewedPR(t *testing.T) {
	for _, c := range []struct {
		name string
		prs  []string
	}{{"only reviewed", []string{prSeven}}, {"mixed with its own", []string{prSeven, prNine}}} {
		t.Run(c.name, func(t *testing.T) {
			g := &pollGitHub{}
			g.set(prSeven, moor.Status{State: "OPEN", Author: author, Viewer: viewer, Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE"})
			g.set(prNine, moor.Status{State: "OPEN", Author: viewer, Viewer: viewer, Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE"})
			f := mooredService(t, map[string]*pollGitHub{"work": g})
			session := f.moorReviewing(t, "Nelson", "work", []string{prSeven}, c.prs...)
			f.s.PollMoored(context.Background())

			for _, path := range []string{"merge", "unfollow"} {
				rec := f.post("/api/ships/" + session + "/" + path)
				if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "review") {
					t.Errorf("POST %s = %d %s, want 409 saying the ship reviews a PR", path, rec.Code, rec.Body)
				}
			}
			if got := f.records(t); len(got) != 1 {
				t.Errorf("moored records = %d after the refusals, want the ship still moored", len(got))
			}
			if got := f.events(t, session, "unmoored"); len(got) != 0 {
				t.Errorf("unmoored events = %v, want none", got)
			}
		})
	}
}
