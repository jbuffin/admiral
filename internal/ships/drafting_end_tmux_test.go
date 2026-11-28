package ships

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// exitedPane ends the harness with the given exit code and returns the dead pane's session.
func (r *draftRig) exitedPane(t *testing.T, code string) sessionhost.Session {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.rec, "exit"), []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	var dead sessionhost.Session
	waitUntil(t, "the pane to die", func() bool {
		_ = r.s.Host.Sync(context.Background())
		for _, sess := range r.s.Host.Sessions() {
			if sess.Name == "saltpaw" && sess.PaneDead {
				dead = sess
				return true
			}
		}
		return false
	})
	return dead
}

func TestDismissingAnAnchoredDraftingShipThatFiledNothingPostsTheItem(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t)
	if sess := r.exitedPane(t, "1"); status(sess, time.Now()) != StatusAnchored {
		t.Fatalf("status = %s, want anchored", status(sess, time.Now()))
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/saltpaw/dismiss", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss = %d %s", rec.Code, rec.Body)
	}
	if item := r.waitEndedItem(t); !strings.Contains(item, "Saltpaw") {
		t.Errorf("item %s does not name the ship", item)
	}
}

func TestAShipThatSailsOffAfterAValidLateRefFollowsItAndPostsNoItem(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t)
	dead := r.exitedPane(t, "0")
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.s.settle(context.Background(), dead)
	r.s.settleWG.Wait()
	if got := r.followed(); len(got) != 1 || got[0][1] != campaignIssue {
		t.Errorf("follow calls = %v, want one for %s", got, campaignIssue)
	}
	if items := r.endedItems(t); len(items) != 0 {
		t.Errorf("items = %v, want none for a ship that followed its campaign", items)
	}
}

func TestAShipThatSailsOffWithNoRefPostsTheItem(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t)
	r.s.settle(context.Background(), r.exitedPane(t, "0"))
	if item := r.waitEndedItem(t); !strings.Contains(item, "Saltpaw") {
		t.Errorf("item %s does not name the ship", item)
	}
}
