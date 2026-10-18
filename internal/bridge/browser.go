package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// maxQueued is how much a browser may fall behind before it is dropped; it
// reconnects and joins again.
const maxQueued = 16 << 20

type frame struct {
	binary bool
	data   []byte
}

// browser is one WebSocket on a ship's bridge. Everything bound for it goes
// through an in-order queue that a writer goroutine drains, so the control
// client's reader never waits on a slow browser. Output bytes that arrive
// while a frame is still queued are merged into it.
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

// send queues a JSON text message.
func (b *browser) send(v any) {
	data, err := json.Marshal(v)
	if err == nil {
		b.push(frame{data: data})
	}
}

// output queues the pane's output, if the browser is streaming.
func (b *browser) output(p []byte) {
	b.mu.Lock()
	on := b.streaming
	b.mu.Unlock()
	if on {
		b.push(frame{binary: true, data: p})
	}
}

// joinWith queues the join payload and starts the stream behind it. It runs on
// the control client's reader, so no output slips between the two.
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

// setHidden stops or resumes the stream; a hidden browser gets nothing.
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

// kill closes the connection; the handler's read loop then ends.
func (b *browser) kill() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	_ = b.conn.CloseNow()
}

// end closes the connection with a close frame, for a session that has ended: the page then
// sees a normal close, not a failed connection it would log as an error.
func (b *browser) end() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	go func() { _ = b.conn.Close(websocket.StatusGoingAway, "the session has ended") }()
}

// write drains the queue until the browser is closed.
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

// in is a message from the browser.
type in struct {
	Type    string `json:"type"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	Visible bool   `json:"visible"`
	Data    string `json:"data"`
}

func (s *Service) serve(rw http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	// Only a current ship (or the setup terminal) is watchable: tmux resolves a bare name to any
	// session it prefixes, so the check is on the exact name.
	if _, ok := s.get(session); !ok {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusNotFound)
		_, _ = rw.Write([]byte(`{"error":"ship not found"}` + "\n"))
		return
	}
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
		if w == nil {
			if m.Type != "join" {
				continue
			}
			b.setDims(m.Cols, m.Rows)
			if w, err = s.enter(session, b); err != nil {
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
