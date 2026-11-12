package ships

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

type oneOrder struct{}

func (oneOrder) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	return "text", "title", "", "", "p", nil
}
func (oneOrder) Launched(source, id string) {}

// An empty ship list before the host's first poll says nothing about which
// orders hold a ship, so autopilot's tick could start an order that already runs.
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

func TestPendingListsAnUnlistedShipUntilItExpires(t *testing.T) {
	s := &Service{Host: sessionhost.New("tmux", t.TempDir()+"/tmux.sock")}
	s.holdUnlisted(OrderKey{"s", "o1"}, "ship-1", "p", nil)
	got := s.Pending()
	if len(got) != 1 || got[0].Source != "s" || got[0].OrderID != "o1" || got[0].Panel != "p" || got[0].Status != StatusUnderway {
		t.Fatalf("Pending = %+v, want one underway ship of s/p/o1", got)
	}
	s.listing[OrderKey{"s", "o1"}] = unlisted{session: "ship-1", panel: "p", at: time.Now().Add(-listedWithin)}
	if got := s.Pending(); len(got) != 0 {
		t.Errorf("Pending after the hold expired = %+v, want none", got)
	}
}

// sliceOrders answers the campaign source's order with the slice's first issue.
type sliceOrders struct{}

func (sliceOrders) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	return "text", "title", "", sliceA, "p", nil
}
func (sliceOrders) Launched(source, id string) {}

// sliceService is a service on a real host: LaunchOrder of an order waits for the first read of the ships.
func sliceService(t *testing.T) *mooredFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("needs tmux: skipped under -short")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	f := mooredService(t, nil)
	dir, err := os.MkdirTemp("/tmp", "sh")
	if err != nil {
		t.Fatal(err)
	}
	sock := dir + "/tmux.sock"
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", sock, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	t.Setenv("TMUX_TMPDIR", dir)
	f.s.Host = sessionhost.New(tmux, sock)
	f.s.Orders = sliceOrders{}
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go f.s.Host.Run(ctx)
	select {
	case <-f.s.Host.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("the host never read the sessions")
	}
	return f
}

func TestLaunchOrderRefusesASliceOverlappingAShipsClaim(t *testing.T) {
	f := sliceService(t)
	if err := moor.Write(f.home, moor.Record{Name: "Slice", Session: "slice", Fleet: "work", URL: sliceB, Issues: []string{sliceB}}); err != nil {
		t.Fatal(err)
	}
	const free = "https://github.com/acme/widgets/issues/30"
	_, lerr := f.s.LaunchOrder(launcher.Request{Source: "campaign", OrderID: "c/slice", Issues: []string{free, sliceB}})
	if lerr == nil || lerr.Status != http.StatusConflict || !strings.Contains(lerr.Msg, "(Slice)") {
		t.Fatalf("LaunchOrder overlapping a ship = %v, want a 409 naming Slice", lerr)
	}
	if holder, ok := f.s.lockIssue(free, "next"); !ok {
		t.Errorf("an issue the refused launch would have taken is still locked by %q", holder)
	}
}

// A slice launched again under the order id its moored ship holds is refused by the order's hold,
// which names the ship the way the issue check does.
func TestLaunchOrderNamesTheShipHoldingASlicesOrder(t *testing.T) {
	f := sliceService(t)
	if err := moor.Write(f.home, moor.Record{Name: "Slice", Session: "slice", Fleet: "work", Source: "campaign", OrderID: "c/slice", URL: sliceA, Issues: []string{sliceA, sliceB}}); err != nil {
		t.Fatal(err)
	}
	_, lerr := f.s.LaunchOrder(launcher.Request{Source: "campaign", OrderID: "c/slice", Issues: []string{sliceA, sliceB}})
	if lerr == nil || lerr.Status != http.StatusConflict || !strings.Contains(lerr.Msg, "(Slice)") {
		t.Fatalf("LaunchOrder of a slice whose order has a ship = %v, want a 409 naming Slice", lerr)
	}
}

func TestLaunchOrderNamesALaunchInFlightForARacingSlice(t *testing.T) {
	f := sliceService(t)
	key := OrderKey{"campaign", "c/slice"}
	if !f.s.lockOrder(key) {
		t.Fatal("the order was already locked")
	}
	defer f.s.unlockOrder(key)
	_, lerr := f.s.LaunchOrder(launcher.Request{Source: "campaign", OrderID: "c/slice", Issues: []string{sliceA, sliceB}})
	if lerr == nil || lerr.Status != http.StatusConflict || !strings.Contains(lerr.Msg, "a launch in flight") {
		t.Fatalf("LaunchOrder of a slice racing its twin = %v, want a 409 naming a launch in flight", lerr)
	}
}
