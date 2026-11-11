package conversation_test

// The Copilot --ui-server observer's wire behaviour against a scripted peer.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/conversation"
)

const wireSession = "183f9af4-79e5-417d-8c86-bac315b5962b"

type peer struct {
	t    *testing.T
	ln   net.Listener
	conn net.Conn
	br   *bufio.Reader
}

func listen(t *testing.T) *peer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return &peer{t: t, ln: ln}
}

func (p *peer) port() int { return p.ln.Addr().(*net.TCPAddr).Port }

func (p *peer) accept() bool {
	p.t.Helper()
	p.ln.(*net.TCPListener).SetDeadline(time.Now().Add(3 * time.Second))
	c, err := p.ln.Accept()
	if err != nil {
		return false
	}
	p.conn, p.br = c, bufio.NewReader(c)
	p.t.Cleanup(func() { c.Close() })
	return true
}

// read returns the next frame, or nil when none arrives within wait.
func (p *peer) read(wait time.Duration) map[string]any {
	p.conn.SetReadDeadline(time.Now().Add(wait))
	n := -1
	for {
		line, err := p.br.ReadString('\n')
		if err != nil {
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(strings.ToLower(line), "content-length:"); ok {
			n, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	if n < 0 {
		return nil
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(p.br, body); err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return nil
	}
	return m
}

func (p *peer) send(v any) {
	p.t.Helper()
	b, _ := json.Marshal(v)
	fmt.Fprintf(p.conn, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

func (p *peer) reply(req map[string]any, result any) {
	p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": result})
}

func (p *peer) event(typ string, data map[string]any) {
	p.send(map[string]any{"jsonrpc": "2.0", "method": "session.event", "params": map[string]any{
		"sessionId": wireSession,
		"event":     map[string]any{"type": typ, "id": "e-" + typ, "timestamp": "2026-11-11T21:00:00.000Z", "parentId": nil, "data": data}}})
}

func (p *peer) request(id int, method string, params map[string]any) {
	p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
}

// handshake plays the server through connect and session.resume and returns what the observer sent.
func (p *peer) handshake() (connect, resume map[string]any) {
	p.t.Helper()
	if !p.accept() {
		p.t.Fatal("the observer never connected")
	}
	connect = p.read(3 * time.Second)
	if connect == nil || connect["method"] != "connect" {
		p.t.Fatalf("first frame = %v, want connect", connect)
	}
	p.reply(connect, map[string]any{"ok": true, "protocolVersion": 3, "version": "1.0.109", "taskKinds": []string{"agent", "shell"}})
	resume = p.read(3 * time.Second)
	if resume == nil || resume["method"] != "session.resume" {
		p.t.Fatalf("second frame = %v, want session.resume", resume)
	}
	p.reply(resume, map[string]any{"sessionId": wireSession})
	return connect, resume
}

func observe(t *testing.T, port int, r *conversation.ServerReader) (done <-chan error, cancel func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ch := make(chan error, 1)
	go func() { ch <- conversation.ObserveUIServer(ctx, port, wireSession, r) }()
	return ch, cancel
}

var allowedOut = map[string]bool{"connect": true, "session.resume": true, "session.detach": true, "ping": true}

func TestObserverAttachesWithoutATokenOrAResumeEvent(t *testing.T) {
	p := listen(t)
	r := conversation.NewServerReader(conversation.Options{Harness: "copilot"})
	observe(t, p.port(), r)
	connect, resume := p.handshake()

	if params, _ := connect["params"].(map[string]any); params["token"] != nil {
		t.Errorf("connect carries a token: %v", params)
	}
	// disableResume is the wire name; suppressResumeEvent is the Go SDK's field and the server ignores it.
	want := `{"disableResume":true,"sessionId":"` + wireSession + `"}`
	if b, _ := json.Marshal(resume["params"]); string(b) != want {
		t.Errorf("session.resume params = %s, want %s", b, want)
	}
}

func TestObserverNeverAnswersWhatTheServerAsks(t *testing.T) {
	p := listen(t)
	r := conversation.NewServerReader(conversation.Options{Harness: "copilot"})
	observe(t, p.port(), r)
	p.handshake()

	// Notifications the recording showed, then the requests the protocol also defines.
	p.event("permission.requested", map[string]any{"requestId": "r1", "permissionRequest": map[string]any{"kind": "shell", "fullCommandText": "touch x"}})
	p.event("elicitation.requested", map[string]any{"requestId": "r2", "message": "Which?", "requestedSchema": map[string]any{"type": "object"}})
	p.event("command.execute", map[string]any{"command": "/new"})
	p.request(101, "userInput.request", map[string]any{"question": "Which?", "choices": []string{"a", "b"}})
	p.request(102, "exitPlanMode.request", map[string]any{"summary": "plan"})
	p.request(103, "autoModeSwitch.request", map[string]any{"errorCode": "x"})
	p.request(104, "permission.request", map[string]any{"kind": "shell"})

	for {
		m := p.read(700 * time.Millisecond)
		if m == nil {
			break
		}
		if _, ok := m["result"]; ok {
			t.Errorf("answered a server request: %v", m)
		}
		if _, ok := m["error"]; ok {
			t.Errorf("answered a server request with an error: %v", m)
		}
		if method, _ := m["method"].(string); !allowedOut[method] {
			t.Errorf("sent %v, which an observer must not", m)
		}
	}
}

func TestObserverFeedsTheReaderFromTheWire(t *testing.T) {
	p := listen(t)
	r := conversation.NewServerReader(conversation.Options{Harness: "copilot"})
	observe(t, p.port(), r)
	p.handshake()
	p.event("user.message", map[string]any{"content": "hello"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if items := r.Items(); len(items) == 1 && items[0].Text == "hello" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("a user.message from the server never reached the reader: %v", r.Items())
}

func TestObserverGivesUpWithAnErrorSoTheFileIsServed(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		p := listen(t)
		port := p.port()
		p.ln.Close()
		r := conversation.NewServerReader(conversation.Options{Harness: "copilot"})
		done, _ := observe(t, port, r)
		select {
		case err := <-done:
			if err == nil {
				t.Error("a refused connection ended with no error")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("the observer did not give up on a refused port")
		}
	})
	t.Run("never answers connect", func(t *testing.T) {
		p := listen(t)
		r := conversation.NewServerReader(conversation.Options{Harness: "copilot"})
		done, _ := observe(t, p.port(), r)
		if !p.accept() {
			t.Fatal("the observer never connected")
		}
		if m := p.read(3 * time.Second); m == nil || m["method"] != "connect" {
			t.Fatalf("first frame = %v, want connect", m)
		}
		select {
		case err := <-done:
			if err == nil {
				t.Error("a server that never answers ended with no error")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the observer waited on a server that never answers")
		}
	})
	t.Run("dropped mid-turn", func(t *testing.T) {
		p := listen(t)
		r := conversation.NewServerReader(conversation.Options{Harness: "copilot"})
		done, _ := observe(t, p.port(), r)
		p.handshake()
		p.event("user.message", map[string]any{"content": "before the drop"})
		time.Sleep(200 * time.Millisecond)
		p.conn.Close()
		select {
		case err := <-done:
			if err == nil {
				t.Error("a dropped connection ended with no error")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("the observer did not notice the drop")
		}
		if items := r.Items(); len(items) != 1 || items[0].Text != "before the drop" {
			t.Errorf("items after the drop = %v, want the one read before it", items)
		}
	})
}
