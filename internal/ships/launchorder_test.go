package ships

import (
	"net/http"
	"testing"

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
