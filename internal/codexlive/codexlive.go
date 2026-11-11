// Package codexlive is the client Admiral keeps on a Codex ship's own app-server: it connects over the pane
// wrapper's Unix socket, attaches to the TUI's thread and hands on what the server sends.
//
// The client only listens. It registers no handlers and never answers a server request (an approval, a
// question or a turn), because the TUI is the human's: an answer from here would decide for them.
package codexlive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

// maxFrame bounds one message: a resumed thread carries its whole history.
const maxFrame = 32 << 20

// maxQueued is how many frames may wait for Next before the connection is given up as stalled.
const maxQueued = 4096

// maxPages bounds how many pages of loaded threads are read.
const maxPages = 10

// Config is what a ship's pane says about its server.
type Config struct {
	// Socket is the pane's @admiral-codex-socket and StartCmd the command it started with; the option is only
	// trusted when the command names the same socket, since a pane can rewrite its own options.
	Socket, StartCmd string
	// DaemonDir is where Codex keeps the real socket behind the listen path.
	DaemonDir string
	// Rollout is the ship's recorded rollout, if any; its file name carries the thread id.
	Rollout string
	// Timeout bounds the connect and the handshake together.
	Timeout time.Duration
}

// Conn is a connection past its handshake.
type Conn struct {
	// Thread is the thread the client attached to and Resume the thread/resume answer, with the history.
	Thread string
	Resume []byte

	ws     *websocket.Conn
	cancel context.CancelFunc

	mu      sync.Mutex
	queue   [][]byte
	waiters map[string]chan []byte
	err     error
	wake    chan struct{} // closed and replaced when the queue grows or the connection ends
	done    chan struct{}
}

// Connect dials the server, initializes and attaches to the TUI's thread. Every step is bounded by cfg.Timeout.
func Connect(ctx context.Context, cfg Config) (*Conn, error) {
	target, err := dialTarget(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{HTTPClient: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", target)
		},
	}}})
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	ws.SetReadLimit(maxFrame)
	// A read whose context ends closes the whole connection, so the reader outlives the handshake's deadline.
	life, stop := context.WithCancel(context.Background())
	c := &Conn{ws: ws, cancel: stop, waiters: map[string]chan []byte{}, wake: make(chan struct{}), done: make(chan struct{})}
	go c.read(life)
	if err := c.handshake(ctx, cfg); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Conn) handshake(ctx context.Context, cfg Config) error {
	if _, err := c.call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "admiral", "title": "Admiral", "version": "0"},
		"capabilities": map[string]any{"experimentalApi": true}, // without it the server never sends requestUserInput
	}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if err := c.send(ctx, map[string]any{"method": "initialized"}); err != nil {
		return fmt.Errorf("initialized: %w", err)
	}
	loaded, err := c.loaded(ctx)
	if err != nil {
		return err
	}
	thread, err := c.choose(ctx, loaded, cfg.Rollout)
	if err != nil {
		return err
	}
	// Only the id: any other field would change the TUI's live thread.
	res, err := c.call(ctx, "thread/resume", map[string]any{"threadId": thread})
	if err != nil {
		return fmt.Errorf("thread/resume: %w", err)
	}
	c.Thread, c.Resume = thread, res
	return nil
}

func (c *Conn) loaded(ctx context.Context) ([]string, error) {
	var ids []string
	cursor := ""
	for i := 0; i < maxPages; i++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "thread/loaded/list", params)
		if err != nil {
			return nil, fmt.Errorf("thread/loaded/list: %w", err)
		}
		var page struct {
			Result struct {
				Data       []string `json:"data"`
				NextCursor *string  `json:"nextCursor"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &page) != nil {
			return nil, errors.New("thread/loaded/list: unreadable answer")
		}
		ids = append(ids, page.Result.Data...)
		if page.Result.NextCursor == nil || *page.Result.NextCursor == "" {
			break
		}
		cursor = *page.Result.NextCursor
	}
	if len(ids) == 0 {
		return nil, errors.New("the server has no thread loaded")
	}
	return ids, nil
}

var rolloutThread = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

// choose picks the thread the TUI shows: the rollout's, when it is loaded; the only one loaded; else the most
// recently updated of those loaded. Never one the server has not loaded, which resuming would start.
func (c *Conn) choose(ctx context.Context, loaded []string, rollout string) (string, error) {
	has := func(id string) bool {
		for _, l := range loaded {
			if l == id {
				return true
			}
		}
		return false
	}
	if m := rolloutThread.FindStringSubmatch(filepath.Base(rollout)); m != nil && has(m[1]) {
		return m[1], nil
	}
	if len(loaded) == 1 {
		return loaded[0], nil
	}
	raw, err := c.call(ctx, "thread/list", map[string]any{"limit": 50})
	if err != nil {
		return "", fmt.Errorf("thread/list: %w", err)
	}
	var list struct {
		Result struct {
			Data []struct {
				ID        string `json:"id"`
				UpdatedAt int64  `json:"updatedAt"`
			} `json:"data"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return "", errors.New("thread/list: unreadable answer")
	}
	best, at := "", int64(-1)
	for _, t := range list.Result.Data {
		if has(t.ID) && t.UpdatedAt > at {
			best, at = t.ID, t.UpdatedAt
		}
	}
	if best == "" {
		return "", errors.New("several threads are loaded and none can be told apart")
	}
	return best, nil
}

var nextID = struct {
	sync.Mutex
	n int
}{}

// call sends a request and waits for its answer; an error answer is an error.
func (c *Conn) call(ctx context.Context, method string, params any) ([]byte, error) {
	nextID.Lock()
	nextID.n++
	id := fmt.Sprint(nextID.n)
	nextID.Unlock()
	ch := make(chan []byte, 1)
	c.mu.Lock()
	c.waiters[id] = ch
	c.mu.Unlock()
	if err := c.send(ctx, map[string]any{"id": json.Number(id), "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case raw := <-ch:
		var r struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &r) == nil && r.Error != nil {
			return nil, errors.New(r.Error.Message)
		}
		return raw, nil
	case <-c.done:
		return nil, c.failure()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Conn) send(ctx context.Context, msg map[string]any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageText, b)
}

// read files each frame: an answer to a waiting call goes to it and the rest wait for Next.
func (c *Conn) read(ctx context.Context) {
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			c.end(err)
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		c.mu.Lock()
		if m.Method == "" && len(m.ID) > 0 {
			if ch, ok := c.waiters[string(m.ID)]; ok {
				delete(c.waiters, string(m.ID))
				ch <- data
			}
			c.mu.Unlock()
			continue
		}
		if len(c.queue) >= maxQueued {
			c.mu.Unlock()
			c.end(errors.New("frames are not being read"))
			return
		}
		c.queue = append(c.queue, data)
		c.signalLocked()
		c.mu.Unlock()
	}
}

func (c *Conn) signalLocked() {
	close(c.wake)
	c.wake = make(chan struct{})
}

func (c *Conn) end(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return
	default:
	}
	c.err = err
	close(c.done)
	c.signalLocked()
	c.ws.CloseNow()
}

func (c *Conn) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		return errors.New("connection closed")
	}
	return c.err
}

// Next is the next frame the server sent that is not an answer to the client's own calls. It returns an error
// once the connection has ended and what was queued is read; ctx ending does not close the connection.
func (c *Conn) Next(ctx context.Context) ([]byte, error) {
	for {
		c.mu.Lock()
		if len(c.queue) > 0 {
			f := c.queue[0]
			c.queue = c.queue[1:]
			c.mu.Unlock()
			return f, nil
		}
		wake := c.wake
		c.mu.Unlock()
		select {
		case <-wake:
		case <-c.done:
			c.mu.Lock()
			more := len(c.queue) > 0
			c.mu.Unlock()
			if !more {
				return nil, c.failure()
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Close ends the connection; Next then returns an error.
func (c *Conn) Close() {
	c.cancel()
	c.end(errors.New("closed"))
}

// dialTarget is the socket to dial, after the checks that stand between the pane's options and a connect: the
// start command names the same socket, and what it resolves to is a socket of this user's in Codex's own directory.
func dialTarget(cfg Config) (string, error) {
	if cfg.Socket == "" || !namesSocket(cfg.StartCmd, cfg.Socket) {
		return "", errors.New("the pane's start command does not name the recorded socket")
	}
	target, err := filepath.EvalSymlinks(cfg.Socket)
	if err != nil {
		return "", fmt.Errorf("socket: %w", err)
	}
	dir, err := filepath.EvalSymlinks(cfg.DaemonDir)
	if err != nil {
		return "", fmt.Errorf("socket directory: %w", err)
	}
	if filepath.Dir(target) != dir {
		return "", errors.New("the socket is not in Codex's socket directory")
	}
	fi, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); fi.Mode()&os.ModeSocket == 0 || !ok || int(st.Uid) != os.Getuid() {
		return "", errors.New("the socket is not a socket of this user's")
	}
	return target, nil
}

// namesSocket is whether the start command's argument after --socket is sock; the launcher single-quotes each
// argument.
func namesSocket(startCmd, sock string) bool {
	words := shellWords(startCmd)
	for i := 0; i+1 < len(words); i++ {
		if words[i] == "--" {
			return false // what follows is the harness's own command line
		}
		if words[i] == "--socket" {
			return words[i+1] == sock
		}
	}
	return false
}

// shellWords splits on spaces, reading single and double quotes as the shell does.
func shellWords(s string) []string {
	var words []string
	var cur strings.Builder
	in, quote := false, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			in = true
		case c == ' ' || c == '\t' || c == '\n':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if in {
		words = append(words, cur.String())
	}
	return words
}
