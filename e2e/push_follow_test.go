package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// restartLAN restarts Admiral on the same wildcard address and port as startLAN did. The paired device's token carries
// over, so a test sees whether the old token still works instead of pairing a new device.
func (w *world) restartLAN(t *testing.T) *fleetInst {
	t.Helper()
	old := w.in
	old.Stop()
	w.in = launchAdmiral(t, w.home, "0.0.0.0:"+old.Port, w.baseEnv())
	if m := lanLine.FindStringSubmatch(w.in.LANLine); m != nil {
		w.in.LANIP = m[1]
	}
	w.in.Token = old.Token
	requireLAN(t, w.in.instance)
	return w.in
}

// followMsg is a message of the follow protocol; the other /ws/v1 messages are skipped before one is returned.
type followMsg struct {
	Type      string `json:"type"`
	Boot      string `json:"boot"`
	Seq       int    `json:"seq"`
	Kind      string `json:"kind"`
	Tag       string `json:"tag"`
	Count     *int   `json:"count"`
	Attention *int   `json:"attention"`
	Fleet     string `json:"fleet"`
	Name      string `json:"name"`
	Line      string `json:"line"`
	Target    string `json:"target"`
	Session   string `json:"session"`
	Reason    string `json:"reason"`
}

type follower struct {
	t  *testing.T
	ws *websocket.Conn
}

// follow dials /ws/v1 over the LAN with the device's bearer token and sends push_follow.
func (r *pushRig) follow(device string, after any, boot string) *follower {
	r.t.Helper()
	// Every socket gets the count in its snapshot once Admiral is ready; a window shows when that is, so the follower's
	// snapshot is known to come before the reply to its push_follow.
	window, _, err := wsDialAt(r.t, r.in.LocalURL, "/ws/v1", "", nil, "")
	if err != nil {
		r.t.Fatal(err)
	}
	defer window.CloseNow()
	(&follower{t: r.t, ws: window}).expect("attention")

	ws, resp, err := wsDialAt(r.t, r.in.lanURL(), "/ws/v1", "", http.Header{"Authorization": {"Bearer " + r.tokens[device]}}, "")
	if err != nil {
		r.t.Fatalf("dial /ws/v1 with device %s's bearer: %v (response %v)", device, err, resp)
	}
	r.t.Cleanup(func() { _ = ws.CloseNow() })
	f := &follower{t: r.t, ws: ws}
	f.expect("attention")
	msg := map[string]any{"type": "push_follow", "after": after}
	if boot != "" {
		msg["boot"] = boot
	}
	b, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
		r.t.Fatal(err)
	}
	return f
}

func isFollowType(typ string) bool {
	switch typ {
	case "push", "push_clear", "push_reset", "attention", "push_refused":
		return true
	}
	return false
}

// next reads until the next follow message, skipping hello and every other /ws/v1 message, or fails at the one deadline.
func (f *follower) next(d time.Duration) followMsg {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	for {
		_, data, err := f.ws.Read(ctx)
		if err != nil {
			f.t.Fatalf("within %s the follower got no follow message: %v", d, err)
		}
		var m followMsg
		if err := json.Unmarshal(data, &m); err != nil {
			f.t.Fatalf("websocket message %q is not JSON: %v", data, err)
		}
		if isFollowType(m.Type) {
			return m
		}
	}
}

// expect reads the next follow message and requires its type.
func (f *follower) expect(typ string) followMsg {
	f.t.Helper()
	m := f.next(15 * time.Second)
	if m.Type != typ {
		f.t.Fatalf("the follower got %+v (count %v, attention %v), want a %s message", m, ptr(m.Count), ptr(m.Attention), typ)
	}
	return m
}

// silent watches for hold and fails on any follow message. A read that times out closes the socket, so it ends the follower's use.
func (f *follower) silent(what string) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hold)
	defer cancel()
	for {
		_, data, err := f.ws.Read(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			f.t.Fatalf("%s: the follower's socket failed: %v", what, err)
		}
		var m followMsg
		if json.Unmarshal(data, &m) == nil && isFollowType(m.Type) {
			f.t.Fatalf("%s: the follower got %s", what, data)
		}
	}
}

func ptr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func count(t *testing.T, m followMsg) int {
	t.Helper()
	switch {
	case m.Type == "attention" && m.Count != nil:
		return *m.Count
	case m.Attention != nil:
		return *m.Attention
	}
	t.Fatalf("%+v carries no count", m)
	return 0
}

// A device follows over its bearer, hears a ship's question as a push that agrees with the Web Push it forwards, hears
// the answer as a push_clear, replays what it missed after reconnecting, and is reset by a restart.
func TestPushFollowJourneyFromQuestionToAnswerToReconnectToRestart(t *testing.T) {
	t.Parallel()
	r := newPushRig(t)
	r.subscribe("a")

	f := r.follow("a", nil, "")
	if m := f.expect("attention"); count(t, m) != 0 {
		t.Fatalf("a follow with nothing asking got %+v, want a count of 0", m)
	}

	ship := r.launch("ans")
	pushMsg := f.expect("push")
	if pushMsg.Boot == "" || pushMsg.Seq < 1 || pushMsg.Kind != "ship_question" || count(t, pushMsg) != 1 {
		t.Fatalf("question push = %+v, want a boot, a seq, kind ship_question and a count of 1", pushMsg)
	}
	if pushMsg.Session != ship.Session || pushMsg.Name != ship.Name || pushMsg.Fleet != "ans" {
		t.Fatalf("question push = %+v, want ship %s (%s) in fleet ans", pushMsg, ship.Name, ship.Session)
	}
	web := r.collect(1)[0]
	for field, got := range map[string]string{"name": pushMsg.Name, "line": pushMsg.Line, "tag": pushMsg.Tag, "session": pushMsg.Session, "fleet": pushMsg.Fleet, "target": pushMsg.Target} {
		if web.payload[field] != got {
			t.Errorf("%s: the follower's push says %q and the Web Push says %v", field, got, web.payload[field])
		}
	}

	r.w.release(ship.Session, "go")
	cleared := f.expect("push_clear")
	if cleared.Boot != pushMsg.Boot || cleared.Seq != pushMsg.Seq+1 || cleared.Tag != pushMsg.Tag || count(t, cleared) != 0 {
		t.Fatalf("answer push_clear = %+v, want boot %s, seq %d, tag %s and a count of 0", cleared, pushMsg.Boot, pushMsg.Seq+1, pushMsg.Tag)
	}
	f.silent("after the answer's push_clear, which already carries the count")

	// Away: the socket is closed (silent's read timeout closed it) and the next question happens without it.
	// Neither the Web Push nor the ship list shows the event is in the ring; a live follower receiving it does.
	observer := r.follow("a", nil, "")
	observer.expect("attention")
	missed := r.launch("q")
	missedWeb := r.collect(1)[0]
	observer.expect("push")
	if missedWeb.payload["session"] != missed.Session {
		t.Fatalf("Web Push %v, want the question of %s", missedWeb.payload, missed.Session)
	}

	back := r.follow("a", cleared.Seq, cleared.Boot)
	replayed := back.expect("push")
	if replayed.Boot != pushMsg.Boot || replayed.Seq != cleared.Seq+1 || replayed.Session != missed.Session || replayed.Tag != missedWeb.payload["tag"] {
		t.Fatalf("replayed push = %+v, want boot %s, seq %d and the question of %s", replayed, pushMsg.Boot, cleared.Seq+1, missed.Session)
	}
	if m := back.expect("attention"); count(t, m) != 1 {
		t.Fatalf("after the replay got %+v, want a count of 1", m)
	}
	back.silent("after the replay and its count")

	// Restart: the ring is in memory only, so the old boot and seq are no longer known.
	r.w.restartLAN(t)
	r.in = r.w.in
	again := r.follow("a", replayed.Seq, replayed.Boot)
	reset := again.expect("push_reset")
	if reset.Boot == "" || reset.Boot == replayed.Boot {
		t.Fatalf("push_reset = %+v, want a new boot instead of %s", reset, replayed.Boot)
	}
	counted := reset.Attention
	if counted == nil {
		counted = again.expect("attention").Count
	}
	if counted == nil || *counted != 1 {
		t.Fatalf("after the restart the count is %v, want 1: the question of %s still asks", counted, missed.Session)
	}
	again.silent("after the restart: a ship that still asks is seeded, not pushed again")
}
