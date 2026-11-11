package shipconv

// A Copilot ship's live conversation from its --ui-server, and the file when the server is not there.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUIServerPortIsReadFromTheStartCommand(t *testing.T) {
	for _, c := range []struct {
		name, cmd string
		port      int
		ok        bool
	}{
		{"quoted argv", "'/bin/copilot' '--session-id' '" + sid + "' '--ui-server' '--port' '65026' '--' 'go'", 65026, true},
		{"plain argv", "copilot --session-id " + sid + " --ui-server --port 4000 -- go", 4000, true},
		{"no --ui-server", "copilot --session-id " + sid + " --port 4000 -- go", 0, false},
		{"no --port", "copilot --session-id " + sid + " --ui-server -- go", 0, false},
		{"port zero", "copilot --ui-server --port 0", 0, false},
		{"port too large", "copilot --ui-server --port 70000", 0, false},
		{"not a number", "copilot --ui-server --port abc", 0, false},
		{"a port in the order's text after --", "copilot --session-id " + sid + " -- 'use --ui-server --port 4000'", 0, false},
		{"a port in the order's text as --interactive", "'/bin/copilot' '--session-id' '" + sid + "' '--interactive=use --ui-server --port 4000'", 0, false},
	} {
		port, ok := uiServerPort(c.cmd)
		if port != c.port || ok != c.ok {
			t.Errorf("%s: uiServerPort = %d, %v; want %d, %v", c.name, port, ok, c.port, c.ok)
		}
	}
}

// fakeUIServer is a scripted Copilot --ui-server on a loopback port.
type fakeUIServer struct {
	t    *testing.T
	ln   net.Listener
	conn chan net.Conn
}

func newFakeUIServer(t *testing.T) *fakeUIServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeUIServer{t: t, ln: ln, conn: make(chan net.Conn, 4)}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
			s.conn <- c
		}
	}()
	return s
}

func (s *fakeUIServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// accepted waits for the observer's connection.
func (s *fakeUIServer) accepted(wait time.Duration) (*wirePeer, bool) {
	select {
	case c := <-s.conn:
		return &wirePeer{t: s.t, c: c, br: bufio.NewReader(c)}, true
	case <-time.After(wait):
		return nil, false
	}
}

type wirePeer struct {
	t  *testing.T
	c  net.Conn
	br *bufio.Reader
}

func (p *wirePeer) read() map[string]any {
	p.c.SetReadDeadline(time.Now().Add(5 * time.Second))
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
	body := make([]byte, n)
	if n < 0 {
		return nil
	}
	if _, err := io.ReadFull(p.br, body); err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	return m
}

func (p *wirePeer) send(v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(p.c, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

// attach answers connect and session.resume the way the recorded server did.
func (p *wirePeer) attach() {
	p.t.Helper()
	for _, method := range []string{"connect", "session.resume"} {
		m := p.read()
		if m == nil || m["method"] != method {
			p.t.Fatalf("observer sent %v, want %s", m, method)
		}
		p.send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": map[string]any{"ok": true, "sessionId": sid}})
	}
}

func (p *wirePeer) delta(messageID, text string) {
	p.send(map[string]any{"jsonrpc": "2.0", "method": "session.event", "params": map[string]any{"sessionId": sid, "event": map[string]any{
		"type": "assistant.message_delta", "ephemeral": true, "id": "d-" + text, "timestamp": "2026-01-02T03:04:05.500Z", "parentId": nil,
		"data": map[string]any{"messageId": messageID, "deltaContent": text}}}})
}

func (f *fixture) uiServerShip(name, path string, port int) {
	f.copilotShip(name, path)
	f.mu.Lock()
	f.starts[name] = fmt.Sprintf("'/bin/copilot' '--session-id' '%s' '--ui-server' '--port' '%d' '--' 'go'", sid, port)
	f.mu.Unlock()
}

func (f *fixture) seedCopilot() string {
	f.t.Helper()
	path := f.copilotPath(sid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	appendTo(f.t, path, copilotUser(1))
	return path
}

// pushed is every item the subscriber is pushed within window, by id.
func (s *socket) pushed(window time.Duration) map[string]string {
	s.t.Helper()
	out := map[string]string{}
	end := time.After(window)
	for {
		select {
		case m := <-s.in:
			items, _ := m["items"].([]any)
			for _, it := range items {
				o := it.(map[string]any)
				out[o["id"].(string)], _ = o["text"].(string)
			}
		case <-end:
			return out
		}
	}
}

// idOf finds the id of the item with text in what pushed returned.
func idOf(m map[string]string, text string) string {
	for id, tx := range m {
		if tx == text {
			return id
		}
	}
	return ""
}

// nextWith reads pushes until one carries want among its texts.
func (s *socket) nextWith(want string) []string {
	s.t.Helper()
	for i := 0; i < 20; i++ {
		got := texts(s.next("a push with " + want))
		for _, g := range got {
			if g == want {
				return got
			}
		}
	}
	s.t.Fatalf("no push carried %q", want)
	return nil
}

func TestCopilotUIServerShowsTheReplyBeforeTheFileHasIt(t *testing.T) {
	f := newFixture(t)
	path := f.seedCopilot()
	srv := newFakeUIServer(t)
	f.uiServerShip("pilot", path, srv.port())
	sub := dial(t, serve(t, f))

	sub.send("conversation_open", "pilot")
	peer, ok := srv.accepted(5 * time.Second)
	if !ok {
		t.Fatal("opening the ship's conversation never dialed its --ui-server")
	}
	peer.attach()
	sub.opened("pilot")
	peer.delta("m2", "Hel")
	peer.delta("m2", "lo")
	sub.nextWith("Hello")
	// The file still has only the user's message: the text came from the server.
	if _, p, _ := f.get("pilot", ""); len(p.Items) != 1 {
		t.Errorf("the file's page has %d items, want 1", len(p.Items))
	}
}

func TestCopilotUIServerDroppedMidTurnFallsBackToTheFile(t *testing.T) {
	f := newFixture(t)
	path := f.seedCopilot()
	srv := newFakeUIServer(t)
	f.uiServerShip("pilot", path, srv.port())
	sub := dial(t, serve(t, f))

	sub.send("conversation_open", "pilot")
	peer, ok := srv.accepted(5 * time.Second)
	if !ok {
		t.Fatal("opening the ship's conversation never dialed its --ui-server")
	}
	peer.attach()
	sub.opened("pilot")
	peer.delta("m2", "Hello")
	live := idOf(sub.pushed(2*time.Second), "Hello")
	if live == "" {
		t.Fatal("the live reply was never pushed")
	}

	peer.c.Close()
	appendTo(t, path, copilotAssistant(2, "Hello"))
	// The file's copy of the reply is the live item, not a second one.
	for id, text := range sub.pushed(3 * time.Second) {
		if text == "Hello" && id != live {
			t.Errorf("the file's reply arrived as %q after the live one %q", id, live)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, p, body := f.get("pilot", "")
		if code != 200 || p.State != "ok" {
			t.Fatalf("GET after the drop = %d %s, want 200 ok", code, body)
		}
		if len(p.Items) == 2 && p.Items[0].Text == "message 001" && p.Items[1].Text == "Hello" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the drop the page is %s, want the user's message and one Hello", body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCopilotUIServerThatNeverAnswersLeavesTheFileServed(t *testing.T) {
	f := newFixture(t)
	path := f.seedCopilot()
	srv := newFakeUIServer(t)
	f.uiServerShip("pilot", path, srv.port())
	sub := dial(t, serve(t, f))

	sub.send("conversation_open", "pilot")
	peer, ok := srv.accepted(5 * time.Second)
	if !ok {
		t.Fatal("opening the ship's conversation never dialed its --ui-server")
	}
	if m := peer.read(); m == nil || m["method"] != "connect" {
		t.Fatalf("observer sent %v, want connect", m)
	}
	// No answer, ever.
	sub.opened("pilot")
	f.wantState("pilot", "ok")
	appendTo(t, path, copilotAssistant(2, "From the file."))
	if got := sub.nextWith("From the file."); len(got) != 1 {
		t.Errorf("pushed %v, want only the file's reply", got)
	}
}

func TestCopilotOnlyAShipStartedWithAUIServerIsObserved(t *testing.T) {
	f := newFixture(t)
	path := f.seedCopilot()
	with, without := newFakeUIServer(t), newFakeUIServer(t)
	f.uiServerShip("with", path, with.port())
	f.copilotShip("without", path)
	f.mu.Lock()
	f.starts["without"] = fmt.Sprintf("'/bin/copilot' '--session-id' '%s' '--port' '%d' '--' 'go'", sid, without.port())
	f.mu.Unlock()
	sub := dial(t, serve(t, f))

	sub.send("conversation_open", "without")
	sub.opened("without")
	sub.send("conversation_open", "with")
	if _, ok := with.accepted(5 * time.Second); !ok {
		t.Error("a ship started with --ui-server --port was never dialed")
	}
	if _, ok := without.accepted(time.Second); ok {
		t.Error("a ship started without --ui-server was dialed on its --port")
	}
}
