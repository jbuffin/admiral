package server

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const writeTimeout = 5 * time.Second

// Client is one connected WebSocket. Modules push to it through Send and never
// see the WebSocket library.
type Client struct {
	id   int
	conn *websocket.Conn
	ctx  context.Context

	mu   sync.Mutex
	sent map[string][]byte // last message each key delivered
}

// ID identifies the client for as long as it is connected.
func (c *Client) ID() int { return c.id }

// Send writes v as one JSON text message.
func (c *Client) Send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.write(data)
}

// SendOnce sends what build returns unless this client already has exactly
// that under key. A message counts as delivered only when its write succeeds,
// so a failed one is retried by the next call. build runs under the client's
// lock, so concurrent calls deliver the newest state last.
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

// Hub tracks the connected clients and pushes to one or all of them.
type Hub struct {
	mu      sync.Mutex
	next    int
	clients map[int]*Client
}

func NewHub() *Hub { return &Hub{clients: map[int]*Client{}} }

func (h *Hub) add(ctx context.Context, conn *websocket.Conn) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	c := &Client{id: h.next, conn: conn, ctx: ctx, sent: map[string][]byte{}}
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

// Len is how many clients are connected.
func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// SendTo pushes v to one client; it reports whether that client is connected.
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

// Broadcast pushes v to every client.
func (h *Hub) Broadcast(v any) {
	h.each(func(c *Client) { _ = c.Send(v) })
}

// BroadcastOnce is SendOnce to every client.
func (h *Hub) BroadcastOnce(key string, build func() any) {
	h.each(func(c *Client) { _ = c.SendOnce(key, build) })
}

// each runs f for every client at once, so one slow client doesn't hold up
// the others, and waits for all of them.
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
