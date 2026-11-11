package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/server"
)

// A browser that falls this far behind is dropped; it reconnects and joins again.
const maxQueued = 16 << 20

type frame struct {
	binary bool
	data   []byte
}

// A writer goroutine drains the queue, so the control client's reader never waits on a slow browser.
type browser struct {
	conn *websocket.Conn

	mu        sync.Mutex
	wake      chan struct{}
	q         []frame
	queued    int
	streaming bool // output is queued for it
	joined    bool // it has its join payload
	hidden    bool
	cols      int
	rows      int
	closed    bool
}

func newBrowser(conn *websocket.Conn) *browser {
	return &browser{conn: conn, wake: make(chan struct{}, 1)}
}

func (b *browser) push(f frame) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	if n := len(b.q); f.binary && n > 0 && b.q[n-1].binary && len(b.q[n-1].data) < 64<<10 {
		b.q[n-1].data = append(b.q[n-1].data, f.data...)
	} else {
		b.q = append(b.q, f)
	}
	b.queued += len(f.data)
	over := b.queued > maxQueued
	b.mu.Unlock()
	if over {
		b.kill()
		return
	}
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *browser) send(v any) {
	data, err := json.Marshal(v)
	if err == nil {
		b.push(frame{data: data})
	}
}

func (b *browser) output(p []byte) {
	b.mu.Lock()
	on := b.streaming
	b.mu.Unlock()
	if on {
		b.push(frame{binary: true, data: p})
	}
}

// joinWith runs on the control client's reader, so no output slips between the payload and the stream.
func (b *browser) joinWith(j joinMsg) {
	b.mu.Lock()
	if b.hidden || b.closed {
		b.mu.Unlock()
		return
	}
	b.q = b.q[:0] // anything queued before a rejoin is stale
	b.queued = 0
	b.streaming, b.joined = true, true
	b.mu.Unlock()
	b.send(j)
}

func (b *browser) isJoined() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.joined && !b.hidden
}

func (b *browser) dims() (cols, rows int, hidden bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cols, b.rows, b.hidden
}

func (b *browser) setDims(cols, rows int) {
	if cols < 1 || rows < 1 || cols > 1000 || rows > 1000 {
		return
	}
	b.mu.Lock()
	b.cols, b.rows = cols, rows
	b.mu.Unlock()
}

func (b *browser) setHidden(hidden bool) {
	b.mu.Lock()
	b.hidden = hidden
	if hidden {
		b.streaming, b.joined = false, false
		b.q = b.q[:0]
		b.queued = 0
	}
	b.mu.Unlock()
}

func (b *browser) kill() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	_ = b.conn.CloseNow()
}

// With a close frame the page sees a normal close, not a failed connection it would log as an error.
func (b *browser) end() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	go func() { _ = b.conn.Close(websocket.StatusGoingAway, "the session has ended") }()
}

func (b *browser) write(ctx context.Context) {
	for {
		b.mu.Lock()
		q := b.q
		b.q, b.queued = nil, 0
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return
		}
		if len(q) == 0 {
			select {
			case <-b.wake:
			case <-ctx.Done():
				return
			}
			continue
		}
		for _, f := range q {
			typ := websocket.MessageText
			if f.binary {
				typ = websocket.MessageBinary
			}
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := b.conn.Write(wctx, typ, f.data)
			cancel()
			if err != nil {
				b.kill()
				return
			}
		}
	}
}

type in struct {
	Type    string `json:"type"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	Visible bool   `json:"visible"`
	Data    string `json:"data"`
	// Missing means 1.
	API json.RawMessage `json:"api"`
}

// 125 for the frame's payload less the 2-byte code.
const maxCloseReason = 123

func cutAtRune(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Missing or null means 1; a string or a float is unsupported.
func supportedAPI(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var v int
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	return slices.Contains(server.APIVersions, v)
}

func notFound(rw http.ResponseWriter, what string) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusNotFound)
	_, _ = rw.Write([]byte(`{"error":"` + what + ` not found"}` + "\n"))
}

func (s *Service) serve(rw http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	// Only a current ship (or the setup terminal) is watchable: tmux resolves a bare name to any
	// session it prefixes, so the check is on the exact name.
	if _, ok := s.get(session); !ok {
		notFound(rw, "ship")
		return
	}
	s.serveBridge(rw, r, session, target{})
}

// The crew name never reaches tmux; the boat's ids do.
func (s *Service) serveCrew(rw http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	c, ok := s.crewBoat(session, r.PathValue("name"))
	if !ok {
		notFound(rw, "crew boat")
		return
	}
	s.serveBridge(rw, r, session, target{crew: true, pane: c.PaneID, win: c.WindowID})
}

func (s *Service) serveBridge(rw http.ResponseWriter, r *http.Request, session string, t target) {
	conn, err := websocket.Accept(rw, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	ctx := r.Context()

	b := newBrowser(conn)
	var w *watch
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var m in
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.Type == "join" && !supportedAPI(m.API) {
			reason := "unsupported api version " + string(m.API)
			reason = cutAtRune(reason, maxCloseReason)
			ef, _ := json.Marshal(map[string]string{"type": "error", "error": reason})
			_ = conn.Write(ctx, websocket.MessageText, ef)
			_ = conn.Close(websocket.StatusPolicyViolation, reason)
			return
		}
		if w == nil {
			if m.Type != "join" {
				continue
			}
			b.setDims(m.Cols, m.Rows)
			if w, err = s.enter(session, t, b); err != nil {
				_ = conn.Close(websocket.StatusInternalError, "the ship's terminal could not be attached")
				return
			}
			defer w.leave(b)
			go b.write(ctx)
			w.join(b, true)
			continue
		}
		switch m.Type {
		case "join":
			b.setDims(m.Cols, m.Rows)
			w.join(b, true)
		case "resize":
			b.setDims(m.Cols, m.Rows)
			w.makeActive(b)
		case "active":
			w.makeActive(b)
		case "visible":
			if m.Visible {
				b.setHidden(false)
				w.join(b, false)
			} else {
				b.setHidden(true)
			}
		case "input":
			if _, _, hidden := b.dims(); !hidden {
				w.input(b, m.Data)
			}
		}
	}
}
