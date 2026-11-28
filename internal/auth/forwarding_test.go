package auth_test

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/auth"
)

// openSockets stands in for the push side's count of follower sockets per device.
type openSockets struct {
	mu   sync.Mutex
	open map[string]bool
}

func (o *openSockets) set(id string, open bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.open == nil {
		o.open = map[string]bool{}
	}
	o.open[id] = open
}

func (o *openSockets) get(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.open[id]
}

type fwdRig struct {
	*rig
	sockets *openSockets
}

// openForwarding opens the store at home with Admiral started at start and the clock at clk.
func openForwarding(t *testing.T, home string, clk *clock, start time.Time, sockets *openSockets) *fwdRig {
	t.Helper()
	rc := &remoteCfg{}
	a, err := auth.New(home, "7420", auth.WithClock(clk.Now), auth.WithStart(start), auth.WithFollowing(sockets.get), auth.WithRemote(rc.get))
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	mux := http.NewServeMux()
	a.Routes(mux)
	return &fwdRig{rig: &rig{t: t, home: home, clk: clk, a: a, h: a.Middleware(mux), remote: rc}, sockets: sockets}
}

var t0 = time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)

type forwardingView struct {
	ID           string
	Forwarding   *string
	LastFollowed *string
}

// views lists the devices as the Devices API shows them, by id.
func (g *rig) views() map[string]forwardingView {
	g.t.Helper()
	rec := g.local("GET", "/api/devices", "")
	var out struct{ Devices []forwardingView }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		g.t.Fatalf("list devices: %d %s", rec.Code, rec.Body)
	}
	m := map[string]forwardingView{}
	for _, d := range out.Devices {
		m[d.ID] = d
	}
	return m
}

// wantState fails unless the device reads the given forwarding state and lastFollowed ("" is null).
func (g *rig) wantState(id, forwarding, lastFollowed string) {
	g.t.Helper()
	v, ok := g.views()[id]
	if !ok {
		g.t.Fatalf("device %s is not listed", id)
	}
	got := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	if got(v.Forwarding) != forwarding || got(v.LastFollowed) != lastFollowed {
		g.t.Errorf("forwarding %q lastFollowed %q, want %q %q", got(v.Forwarding), got(v.LastFollowed), forwarding, lastFollowed)
	}
}

func (g *rig) pairDevice(name string) string {
	g.t.Helper()
	rec, out := g.redeem(lanReq(nil), g.mint(), name)
	if rec.Code != 200 || out.Device.ID == "" {
		g.t.Fatalf("pair: %d %s", rec.Code, rec.Body)
	}
	return out.Device.ID
}

func newForwarding(t *testing.T) (*fwdRig, *clock) {
	clk := &clock{now: t0}
	return openForwarding(t, t.TempDir(), clk, t0, &openSockets{}), clk
}

func TestAFollowingDeviceReadsOnWhileASocketIsOpenHoweverLongItHasBeen(t *testing.T) {
	g, clk := newForwarding(t)
	phone, tablet := g.pairDevice("Phone"), g.pairDevice("Tablet")
	g.sockets.set(phone, true)
	g.a.Followed(phone)
	g.wantState(phone, "on", "")
	clk.advance(3 * time.Hour)
	g.wantState(phone, "on", "")
	g.wantState(tablet, "", "")
}

func TestAClosedFollowerReadsOnForFiveMinutesThenStopped(t *testing.T) {
	g, clk := newForwarding(t)
	id := g.pairDevice("Phone")
	g.sockets.set(id, true)
	g.a.Followed(id)
	clk.advance(10 * time.Minute)
	closed := clk.now
	g.sockets.set(id, false)
	g.a.FollowEnded(id, closed)

	last := closed.Format(time.RFC3339)
	g.wantState(id, "on", last)
	clk.advance(4*time.Minute + 59*time.Second)
	g.wantState(id, "on", last)
	clk.advance(2 * time.Second)
	g.wantState(id, "stopped", last)
}

func TestTheFlagAndLastFollowedSurviveARestartAndARestartNeverFlashesStopped(t *testing.T) {
	home := t.TempDir()
	clk := &clock{now: t0}
	first := openForwarding(t, home, clk, t0, &openSockets{})
	id := first.pairDevice("Phone")
	first.a.Followed(id)
	clk.advance(time.Minute)
	closed := clk.now
	first.a.FollowEnded(id, closed)
	last := closed.Format(time.RFC3339)

	// Admiral was down for an hour; the device has not had time to follow again.
	clk.advance(time.Hour)
	second := openForwarding(t, home, clk, clk.now, &openSockets{})
	second.wantState(id, "on", last)
	clk.advance(4*time.Minute + 59*time.Second)
	second.wantState(id, "on", last)
	clk.advance(2 * time.Second)
	second.wantState(id, "stopped", last)
}

func TestARecordWithNoLastFollowedCountsFromAdmiralsStart(t *testing.T) {
	home := t.TempDir()
	clk := &clock{now: t0}
	first := openForwarding(t, home, clk, t0, &openSockets{})
	id := first.pairDevice("Phone")
	first.a.Followed(id) // Admiral died while the device still followed: no close was saved

	clk.advance(time.Hour)
	second := openForwarding(t, home, clk, clk.now, &openSockets{})
	second.wantState(id, "on", "")
	clk.advance(4*time.Minute + 59*time.Second)
	second.wantState(id, "on", "")
	clk.advance(2 * time.Second)
	second.wantState(id, "stopped", "")
}

func TestUnfollowClearsTheRecordAndALaterFollowRecordsItAgain(t *testing.T) {
	g, clk := newForwarding(t)
	id := g.pairDevice("Phone")
	g.a.Followed(id)
	g.a.FollowEnded(id, clk.now)
	g.wantState(id, "on", clk.now.Format(time.RFC3339))

	g.a.Unfollowed(id)
	clk.advance(time.Hour)
	g.wantState(id, "", "")

	g.sockets.set(id, true)
	g.a.Followed(id)
	g.wantState(id, "on", "")
}

func TestASocketClosingAfterUnfollowDoesNotBringTheRecordBack(t *testing.T) {
	home := t.TempDir()
	clk := &clock{now: t0}
	g := openForwarding(t, home, clk, t0, &openSockets{})
	id := g.pairDevice("Phone")
	g.sockets.set(id, true)
	g.a.Followed(id)
	g.wantState(id, "on", "")
	g.sockets.set(id, false)
	g.a.Unfollowed(id)
	g.a.FollowEnded(id, clk.now)
	g.wantState(id, "", "")

	clk.advance(time.Hour)
	openForwarding(t, home, clk, clk.now, &openSockets{}).wantState(id, "", "")
}

func TestARevokedDeviceStaysGoneWhenItsSocketClosesAfterwards(t *testing.T) {
	home := t.TempDir()
	clk := &clock{now: t0}
	g := openForwarding(t, home, clk, t0, &openSockets{})
	id := g.pairDevice("Phone")
	g.a.Followed(id)
	g.wantState(id, "on", "")
	if rec := g.local("DELETE", "/api/devices/"+id, ""); rec.Code != 200 && rec.Code != 204 {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	g.a.FollowEnded(id, clk.now)
	if _, listed := g.views()[id]; listed {
		t.Fatal("the revoked device is listed again after its socket closed")
	}
	if _, listed := openForwarding(t, home, clk, clk.now, &openSockets{}).views()[id]; listed {
		t.Fatal("the revoked device came back after a restart")
	}
}
