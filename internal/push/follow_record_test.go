package push_test

import (
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type recorded struct {
	call, device string
	at           time.Time
}

// recorder is the fake record the Followers report to; ended carries each close so a test can wait on it.
type recorder struct {
	mu    sync.Mutex
	calls []recorded
	ended chan recorded
}

func newRecorder() *recorder { return &recorder{ended: make(chan recorded, 16)} }

func (rc *recorder) add(c recorded) {
	rc.mu.Lock()
	rc.calls = append(rc.calls, c)
	rc.mu.Unlock()
}
func (rc *recorder) Followed(d string)   { rc.add(recorded{call: "followed", device: d}) }
func (rc *recorder) Unfollowed(d string) { rc.add(recorded{call: "unfollowed", device: d}) }
func (rc *recorder) FollowEnded(d string, at time.Time) {
	c := recorded{"ended", d, at}
	rc.add(c)
	rc.ended <- c
}

func (rc *recorder) all() []recorded {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]recorded(nil), rc.calls...)
}

func (r *followRig) handledOne() {
	r.t.Helper()
	select {
	case <-r.handled:
	case <-time.After(5 * time.Second):
		r.t.Fatal("the server never handled the message")
	}
}

func (r *followRig) closeAndWaitGone(conn *websocket.Conn) {
	r.t.Helper()
	before := r.hub.Len()
	_ = conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	for r.hub.Len() >= before {
		if time.Now().After(deadline) {
			r.t.Fatal("the hub never dropped the closed socket")
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *followRig) wantEnded(rc *recorder) recorded {
	r.t.Helper()
	select {
	case e := <-rc.ended:
		return e
	case <-time.After(5 * time.Second):
		r.t.Fatal("the last socket closed and nothing was recorded")
		return recorded{}
	}
}

func TestAFollowReportsItsDeviceToTheRecord(t *testing.T) {
	rc := newRecorder()
	r := newFollowRig(t, followOpts{record: rc})
	conn := r.dialAs("d-phone", true)
	r.follow(conn, nil, "")
	r.wantNext(conn, "attention")
	got := rc.all()
	if len(got) == 0 || got[0].call != "followed" || got[0].device != "d-phone" {
		t.Errorf("record heard %+v, want followed d-phone", got)
	}
}

func TestOnlyTheLastSocketOfADeviceToCloseReportsTheEnd(t *testing.T) {
	rc := newRecorder()
	r := newFollowRig(t, followOpts{record: rc})
	a, b := r.dialAs("d-phone", true), r.dialAs("d-phone", true)
	for _, c := range []*websocket.Conn{a, b} {
		r.follow(c, nil, "")
		r.wantNext(c, "attention")
	}

	r.closeAndWaitGone(a)
	r.clock.advance(time.Minute)
	closedAt := r.clock.Now()
	r.closeAndWaitGone(b)

	e := r.wantEnded(rc)
	if e.device != "d-phone" || !e.at.Equal(closedAt) {
		t.Errorf("ended %+v, want d-phone at %v, the time the last socket closed", e, closedAt)
	}
	ends := 0
	for _, c := range rc.all() {
		if c.call == "ended" {
			ends++
		}
	}
	if ends != 1 {
		t.Errorf("%d ends reported in %+v, want 1: the first socket closing left another following", ends, rc.all())
	}
}

func TestAnUnfollowReportsTheDeviceAndEndsThatSocketsFollow(t *testing.T) {
	rc := newRecorder()
	r := newFollowRig(t, followOpts{record: rc})
	conn := r.dialAs("d-phone", true)
	r.follow(conn, nil, "")
	r.wantNext(conn, "attention")

	r.write(conn, map[string]any{"type": "push_unfollow"})
	r.handledOne()
	r.handledOne()
	got := rc.all()
	if len(got) < 2 || got[len(got)-1].call != "unfollowed" || got[len(got)-1].device != "d-phone" {
		t.Errorf("record heard %+v, want followed then unfollowed d-phone", got)
	}
	r.ask("Dory")
	r.quiet(conn)
}

func TestACookieOnlySocketsFollowAndUnfollowNeverReachTheRecord(t *testing.T) {
	rc := newRecorder()
	r := newFollowRig(t, followOpts{record: rc})
	conn := r.dialAs("d-phone", false)
	r.follow(conn, nil, "")
	r.wantNext(conn, "push_refused")
	r.write(conn, map[string]any{"type": "push_unfollow"})
	r.handledOne()
	r.handledOne()
	r.closeAndWaitGone(conn)

	tablet := r.dialAs("d-tablet", true)
	r.follow(tablet, nil, "")
	r.wantNext(tablet, "attention")
	got := rc.all()
	if len(got) != 1 || got[0].call != "followed" || got[0].device != "d-tablet" {
		t.Errorf("record heard %+v, want only the bearer socket's follow of d-tablet", got)
	}
}
