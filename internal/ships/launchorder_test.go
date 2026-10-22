package ships

import (
	"net/http"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

type oneOrder struct{}

func (oneOrder) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	return "text", "title", "", "", "p", nil
}
func (oneOrder) Launched(source, id string) {}

// While the host has not listed the sessions (before its first poll, or with
// the control connection lost), the ship list is empty and says nothing about
// which orders hold a ship. A launch from a source is refused then, so a
// launch on autopilot's tick can't start an order that already runs.
func TestLaunchOrderRefusesWhileShipsAreNotKnown(t *testing.T) {
	s := &Service{
		Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: not ready
		Orders:   oneOrder{},
		Launcher: &launcher.Launcher{Config: func() *config.Config { return &config.Config{} }},
	}
	_, lerr := s.LaunchOrder(launcher.Request{Fleet: "work", Harness: "claude", Dir: t.TempDir(),
		Skill: launcher.Skill{Kind: "none"}, Source: "s", OrderID: "o1"})
	if lerr == nil || lerr.Msg != "ships are not known yet" || lerr.Status != http.StatusServiceUnavailable {
		t.Fatalf("LaunchOrder before the host is ready = %v, want a 503 \"ships are not known yet\"", lerr)
	}
}

// A ship launched but not yet listed by the host counts as running, so a
// pass that counts ships against a cap sees it.
func TestPendingListsAnUnlistedShipUntilItExpires(t *testing.T) {
	s := &Service{Host: sessionhost.New("tmux", t.TempDir()+"/tmux.sock")}
	s.holdUnlisted(OrderKey{"s", "o1"}, "ship-1", "p")
	got := s.Pending()
	if len(got) != 1 || got[0].Source != "s" || got[0].OrderID != "o1" || got[0].Panel != "p" || got[0].Status != StatusUnderway {
		t.Fatalf("Pending = %+v, want one underway ship of s/p/o1", got)
	}
	s.listing[OrderKey{"s", "o1"}] = unlisted{"ship-1", "p", time.Now().Add(-listedWithin)}
	if got := s.Pending(); len(got) != 0 {
		t.Errorf("Pending after the hold expired = %+v, want none", got)
	}
}
