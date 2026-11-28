package push

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/attention"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
)

const (
	// RingMax and RingMaxAge bound what a follower can catch up on.
	RingMax    = 500
	RingMaxAge = 24 * time.Hour

	defaultQueue = 1024
)

// FollowersConfig is what Followers reads from outside. Zero Now is time.Now, an empty Boot is a fresh id, nil Ready is ready.
type FollowersConfig struct {
	Now   func() time.Time
	Boot  string
	Ready func() bool
	// Queue is how many messages wait per follower before it is closed as too slow; zero is 1024.
	Queue int
	// Send writes one message to a follower; nil is Client.Send. Tests replace it to stall a writer.
	Send func(c *server.Client, msg []byte) error
	// Record hears which device follows, stops following on purpose, or loses its last follower socket; nil hears nothing.
	Record FollowRecorder
}

// FollowRecorder is where Followers reports a device's follow state, once per device and not per socket.
type FollowRecorder interface {
	Followed(device string)
	Unfollowed(device string)
	FollowEnded(device string, at time.Time)
}

type ringEntry struct {
	seq int
	at  time.Time
	msg []byte
}

type follower struct {
	c        *server.Client
	q        chan []byte
	done     chan struct{}
	lastSent int // the count last sent, -1 for none
}

// Followers sends push events to the bearer-paired sockets that follow them and keeps the ring they catch up from.
type Followers struct {
	cfg FollowersConfig

	mu        sync.Mutex
	seq       int
	ring      []ringEntry
	followers map[int]*follower
	known     bool // the Watcher has processed both lists
	count     int

	// smu guards socks and is never held across a Record call, so auth can ask Connected while it holds its own lock.
	smu   sync.Mutex
	socks map[string]map[int]struct{} // per device, the sockets that follow
}

func NewFollowers(cfg FollowersConfig) *Followers {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Boot == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		cfg.Boot = hex.EncodeToString(b)
	}
	if cfg.Queue <= 0 {
		cfg.Queue = defaultQueue
	}
	if cfg.Send == nil {
		cfg.Send = func(c *server.Client, msg []byte) error { return c.Send(json.RawMessage(msg)) }
	}
	return &Followers{cfg: cfg, followers: map[int]*follower{}, socks: map[string]map[int]struct{}{}}
}

type pushMsg struct {
	Type      string `json:"type"`
	Boot      string `json:"boot"`
	Seq       int    `json:"seq"`
	Kind      Kind   `json:"kind"`
	Attention *int   `json:"attention,omitempty"`
	Payload
}

type clearMsg struct {
	Type      string `json:"type"`
	Boot      string `json:"boot"`
	Seq       int    `json:"seq"`
	Tag       string `json:"tag"`
	Attention *int   `json:"attention,omitempty"`
}

// current is the count to send, or nil before the lists are known or the host is ready. Called with f.mu held.
func (f *Followers) current() *int {
	if !f.known || (f.cfg.Ready != nil && !f.cfg.Ready()) {
		return nil
	}
	n := f.count
	return &n
}

// Deliver is the Sink side: a Push or a Clear becomes a ring entry and goes to every follower.
func (f *Followers) Deliver(d Decided) {
	if d.Outcome != Push && d.Outcome != Clear {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	att := f.current()
	f.seq++
	var msg any
	if d.Outcome == Push {
		msg = pushMsg{Type: "push", Boot: f.cfg.Boot, Seq: f.seq, Kind: d.Event.Kind, Attention: att, Payload: PayloadOf(d.Event)}
	} else {
		msg = clearMsg{Type: "push_clear", Boot: f.cfg.Boot, Seq: f.seq, Tag: d.Event.Key, Attention: att}
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	now := f.cfg.Now()
	f.prune(now)
	f.ring = append(f.ring, ringEntry{f.seq, now, b})
	if len(f.ring) > RingMax {
		f.ring = append([]ringEntry(nil), f.ring[len(f.ring)-RingMax:]...)
	}
	for _, fw := range f.followers {
		f.enqueue(fw, b)
		if att != nil {
			fw.lastSent = *att
		}
	}
}

// prune drops what is older than RingMaxAge. Called with f.mu held.
func (f *Followers) prune(now time.Time) {
	i := 0
	for i < len(f.ring) && now.Sub(f.ring[i].at) > RingMaxAge {
		i++
	}
	if i > 0 {
		f.ring = append([]ringEntry(nil), f.ring[i:]...)
	}
}

// enqueue never blocks: a follower whose queue is full is closed and catches up on reconnecting, so it never has a gap.
func (f *Followers) enqueue(fw *follower, b []byte) {
	select {
	case fw.q <- b:
	default:
		f.drop(fw)
		go fw.c.Close()
	}
}

// drop is called with f.mu held.
func (f *Followers) drop(fw *follower) {
	if f.followers[fw.c.ID()] == fw {
		delete(f.followers, fw.c.ID())
		close(fw.done)
	}
}

// Snapshotter is a Sink that follows the attention count: Snapshot hands it the lists the Watcher has processed, before the
// events they produced are delivered, and Settle runs after them.
type Snapshotter interface {
	Snapshot(shipList []ships.Ship, items []inbox.Item)
	Settle()
}

func (f *Followers) Snapshot(shipList []ships.Ship, items []inbox.Item) {
	n := attention.Count(shipList, items)
	f.mu.Lock()
	f.known, f.count = true, n
	f.mu.Unlock()
}

// Settle sends the count to each follower that has not had it.
func (f *Followers) Settle() {
	f.mu.Lock()
	defer f.mu.Unlock()
	att := f.current()
	if att == nil {
		return
	}
	b, _ := json.Marshal(map[string]any{"type": "attention", "count": *att})
	for _, fw := range f.followers {
		if fw.lastSent != *att {
			f.enqueue(fw, b)
			fw.lastSent = *att
		}
	}
}

// Handle is the message handler for "push_follow".
func (f *Followers) Handle(ctx context.Context, c *server.Client, raw json.RawMessage) {
	if !c.Bearer() {
		_ = c.Send(map[string]string{"type": "push_refused", "reason": "bearer_required"})
		return
	}
	var req struct {
		After json.RawMessage `json:"after"`
		Boot  string          `json:"boot"`
	}
	if json.Unmarshal(raw, &req) != nil {
		return
	}
	f.joined(ctx, c)
	haveAfter := len(req.After) > 0 && string(req.After) != "null"
	var after int
	validAfter := haveAfter && json.Unmarshal(req.After, &after) == nil && after >= 0

	f.mu.Lock()
	defer f.mu.Unlock()
	fw := f.followers[c.ID()]
	if fw == nil {
		fw = &follower{c: c, q: make(chan []byte, f.cfg.Queue), done: make(chan struct{}), lastSent: -1}
		f.followers[c.ID()] = fw
		go f.run(ctx, fw)
	}
	// A fresh catch-up replaces whatever waited for the socket.
drain:
	for {
		select {
		case <-fw.q:
		default:
			break drain
		}
	}
	c.SetFollowing(true)
	fw.lastSent = -1
	att := f.current()
	switch {
	case !haveAfter:
	case !validAfter || !f.inRing(after, req.Boot):
		b, _ := json.Marshal(struct {
			Type      string `json:"type"`
			Boot      string `json:"boot"`
			Attention *int   `json:"attention,omitempty"`
		}{"push_reset", f.cfg.Boot, att})
		f.enqueue(fw, b)
		if att != nil {
			fw.lastSent = *att
		}
		return
	default:
		for _, e := range f.ring {
			if e.seq > after {
				f.enqueue(fw, e.msg)
				if f.followers[c.ID()] != fw {
					return
				}
			}
		}
	}
	if att != nil {
		b, _ := json.Marshal(map[string]any{"type": "attention", "count": *att})
		f.enqueue(fw, b)
		fw.lastSent = *att
	}
}

// HandleUnfollow is the message handler for "push_unfollow": the device's record is cleared and this socket stops following. A socket
// without a bearer token is ignored.
func (f *Followers) HandleUnfollow(ctx context.Context, c *server.Client, raw json.RawMessage) {
	if !c.Bearer() {
		return
	}
	f.leave(c)
	f.mu.Lock()
	if fw := f.followers[c.ID()]; fw != nil {
		f.drop(fw)
	}
	c.SetFollowing(false)
	f.mu.Unlock()
	if d := c.Device(); d != "" && f.cfg.Record != nil {
		f.cfg.Record.Unfollowed(d)
	}
}

// Connected is whether the device has a follower socket open.
func (f *Followers) Connected(device string) bool {
	f.smu.Lock()
	defer f.smu.Unlock()
	return len(f.socks[device]) > 0
}

// joined counts the socket among its device's followers and tells the record; the socket's end, not a send failure, ends the count.
func (f *Followers) joined(ctx context.Context, c *server.Client) {
	d := c.Device()
	if d == "" {
		return
	}
	f.smu.Lock()
	set := f.socks[d]
	if set == nil {
		set = map[int]struct{}{}
		f.socks[d] = set
	}
	_, had := set[c.ID()]
	set[c.ID()] = struct{}{}
	f.smu.Unlock()
	if !had {
		go func() {
			<-ctx.Done()
			if f.leave(c) && f.cfg.Record != nil {
				f.cfg.Record.FollowEnded(d, f.cfg.Now())
			}
		}()
	}
	if f.cfg.Record != nil {
		f.cfg.Record.Followed(d)
	}
}

// leave stops counting the socket and reports whether it was the device's last.
func (f *Followers) leave(c *server.Client) bool {
	d := c.Device()
	f.smu.Lock()
	defer f.smu.Unlock()
	set := f.socks[d]
	if _, ok := set[c.ID()]; !ok {
		return false
	}
	delete(set, c.ID())
	if len(set) > 0 {
		return false
	}
	delete(f.socks, d)
	return true
}

// inRing is whether every event after seq is still held. Called with f.mu held.
func (f *Followers) inRing(after int, boot string) bool {
	if boot != f.cfg.Boot {
		return false
	}
	f.prune(f.cfg.Now())
	if after > f.seq {
		return false
	}
	if after == f.seq {
		return true
	}
	return len(f.ring) > 0 && f.ring[0].seq <= after+1
}

// run writes a follower's queue to its socket, off the sink's path, until the socket or the follow ends.
func (f *Followers) run(ctx context.Context, fw *follower) {
	for {
		select {
		case b := <-fw.q:
			if f.cfg.Send(fw.c, b) != nil {
				f.mu.Lock()
				f.drop(fw)
				f.mu.Unlock()
				fw.c.Close()
				return
			}
		case <-fw.done:
			return
		case <-ctx.Done():
			f.mu.Lock()
			f.drop(fw)
			f.mu.Unlock()
			return
		}
	}
}
