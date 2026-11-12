package server

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const writeTimeout = 5 * time.Second

// Modules push to a Client through Send and never see the WebSocket library.
type Client struct {
	id   int
	path string // how the browser reached Admiral: "local", "lan", "tailnet" or ""
	conn *websocket.Conn
	ctx  context.Context

	mu   sync.Mutex
	sent map[string][]byte // last message each key delivered

	// presence is the window's last report; it is the zero value until the window reports.
	presenceMu sync.Mutex
	visible    bool
	lastUsed   time.Time
}

func (c *Client) ID() int { return c.id }

// Path is how the browser reached Admiral: "local", "lan", "tailnet" or "" when unknown.
func (c *Client) Path() string { return c.path }

func (c *Client) Send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.write(data)
}

// A message counts as delivered only when its write succeeds. build runs under
// the client's lock, so concurrent calls deliver the newest state last.
func (c *Client) SendOnce(key string, build func() any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := json.Marshal(build())
	if err != nil {
		return err
	}
	if bytes.Equal(data, c.sent[key]) {
		return nil
	}
	if err := c.write(data); err != nil {
		return err
	}
	c.sent[key] = data
	return nil
}

func (c *Client) write(data []byte) error {
	ctx, cancel := context.WithTimeout(c.ctx, writeTimeout)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, data)
}

type Hub struct {
	// Now is the clock for presence; tests replace it.
	Now func() time.Time

	mu      sync.Mutex
	next    int
	clients map[int]*Client
}

// WatchWindow is how long a window counts as watched after the person last used it.
const WatchWindow = 2 * time.Minute

// setPresence records a window's report: whether it is visible and how long ago it was last used.
func (c *Client) setPresence(visible bool, idle time.Duration, now time.Time) {
	c.presenceMu.Lock()
	c.visible, c.lastUsed = visible, now.Add(-idle)
	c.presenceMu.Unlock()
}

func (c *Client) watching(now time.Time) bool {
	c.presenceMu.Lock()
	defer c.presenceMu.Unlock()
	return c.visible && now.Sub(c.lastUsed) < WatchWindow
}

// Watching is whether any connected window is visible and was used within WatchWindow.
func (h *Hub) Watching() bool {
	now := h.Now()
	for _, c := range h.snapshot() {
		if c.watching(now) {
			return true
		}
	}
	return false
}

// SendWatching delivers v to the windows Watching counts and to no other client.
func (h *Hub) SendWatching(v any) {
	now := h.Now()
	h.each(func(c *Client) {
		if c.watching(now) {
			_ = c.Send(v)
		}
	})
}

func NewHub() *Hub { return &Hub{Now: time.Now, clients: map[int]*Client{}} }

func (h *Hub) add(ctx context.Context, conn *websocket.Conn, path string) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	c := &Client{id: h.next, path: path, conn: conn, ctx: ctx, sent: map[string][]byte{}}
	h.clients[c.id] = c
	return c
}

func (h *Hub) remove(c *Client) {
	h.mu.Lock()
	delete(h.clients, c.id)
	h.mu.Unlock()
}

func (h *Hub) snapshot() []*Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*Client, 0, len(h.clients))
	for _, c := range h.clients {
		out = append(out, c)
	}
	return out
}

func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *Hub) SendTo(id int, v any) bool {
	h.mu.Lock()
	c := h.clients[id]
	h.mu.Unlock()
	if c == nil {
		return false
	}
	_ = c.Send(v)
	return true
}

func (h *Hub) Broadcast(v any) {
	h.each(func(c *Client) { _ = c.Send(v) })
}

func (h *Hub) BroadcastOnce(key string, build func() any) {
	h.each(func(c *Client) { _ = c.SendOnce(key, build) })
}

// Runs f for every client at once, so one slow client doesn't hold up the others.
func (h *Hub) each(f func(*Client)) {
	var wg sync.WaitGroup
	for _, c := range h.snapshot() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f(c)
		}()
	}
	wg.Wait()
}

// SendPaths delivers v to every client that came in over one of the paths and returns when each write has
// finished or the bound has passed, whichever is first.
func (h *Hub) SendPaths(paths []string, v any, bound time.Duration) {
	var wg sync.WaitGroup
	for _, c := range h.snapshot() {
		if !slices.Contains(paths, c.path) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Send(v)
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(bound):
	}
}
