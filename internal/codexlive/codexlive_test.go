package codexlive_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jbuffin/admiral/internal/codexlive"
)

const threadA = "00000000-0000-4000-8000-0000000000a1"
const threadB = "00000000-0000-4000-8000-0000000000b2"

// fakeServer is an app-server on a Unix socket: it records each client message and answers from a script.
type fakeServer struct {
	dir, sock string
	mu        sync.Mutex
	methods   []string
	params    map[string]json.RawMessage
	raw       []string
	accepted  int
	loaded    []string
	resumeErr bool
	hang      string // a method it never answers
	push      []string
	conns     []*websocket.Conn
}

func newFake(t *testing.T, loaded ...string) *fakeServer {
	t.Helper()
	// Short: macOS caps a socket path at 104 bytes.
	dir, err := os.MkdirTemp("/tmp", "cxl")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fakeServer{dir: dir, sock: filepath.Join(dir, "s.sock"), loaded: loaded, params: map[string]json.RawMessage{}}
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(f.serve)}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return f
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.accepted++
	f.conns = append(f.conns, c)
	f.mu.Unlock()
	defer c.CloseNow()
	c.SetReadLimit(1 << 24)
	ctx := r.Context()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
			Params json.RawMessage  `json:"params"`
		}
		json.Unmarshal(data, &m)
		f.mu.Lock()
		f.raw = append(f.raw, string(data))
		if m.Method != "" {
			f.methods = append(f.methods, m.Method)
			f.params[m.Method] = m.Params
		} else {
			f.methods = append(f.methods, "<response>")
		}
		hang, loaded, rerr, push := f.hang, f.loaded, f.resumeErr, f.push
		f.mu.Unlock()
		if m.ID == nil || m.Method == hang {
			continue
		}
		reply := func(result string) {
			c.Write(ctx, websocket.MessageText, []byte(`{"id":`+string(*m.ID)+`,"result":`+result+`}`))
		}
		switch m.Method {
		case "initialize":
			reply(`{"userAgent":"codex/0.0.0","codexHome":"/home/user/.codex","platformFamily":"unix","platformOs":"macos"}`)
		case "thread/loaded/list":
			b, _ := json.Marshal(loaded)
			reply(`{"data":` + string(b) + `,"nextCursor":null}`)
		case "thread/resume":
			if rerr {
				c.Write(ctx, websocket.MessageText, []byte(`{"id":`+string(*m.ID)+`,"error":{"code":-32600,"message":"no rollout found"}}`))
				continue
			}
			var p struct {
				ThreadID string `json:"threadId"`
			}
			json.Unmarshal(m.Params, &p)
			reply(`{"thread":{"id":"` + p.ThreadID + `","turns":[]}}`)
			for _, p := range push {
				c.Write(ctx, websocket.MessageText, []byte(p))
			}
		}
	}
}

func (f *fakeServer) seen() (methods []string, accepted int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...), f.accepted
}

func (f *fakeServer) cfg() codexlive.Config {
	return codexlive.Config{
		Socket: f.sock, StartCmd: startCmd(f.sock),
		DaemonDir: f.dir, Timeout: 500 * time.Millisecond,
	}
}

// startCmd is a pane's start command as the launcher writes it: every argument single-quoted.
func startCmd(sock string) string {
	return "'/bin/admiral' 'codex-pane' '--socket' '" + sock + "' '--' '/bin/codex' '--fa-codex'"
}

func connect(t *testing.T, cfg codexlive.Config) (*codexlive.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, err := codexlive.Connect(ctx, cfg)
	if c != nil {
		t.Cleanup(c.Close)
	}
	return c, err
}

func TestHandshakeIsInitializeInitializedListResumeAndNothingElse(t *testing.T) {
	f := newFake(t, threadA)
	f.push = []string{
		`{"method":"item/tool/requestUserInput","id":0,"params":{"threadId":"` + threadA + `","itemId":"call_q1","questions":[]}}`,
		`{"method":"item/commandExecution/requestApproval","id":1,"params":{"threadId":"` + threadA + `","itemId":"exec-1"}}`,
	}
	c, err := connect(t, f.cfg())
	if err != nil {
		t.Fatal(err)
	}
	if c.Thread != threadA {
		t.Errorf("thread = %q, want the one loaded thread", c.Thread)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, want := range []string{"item/tool/requestUserInput", "item/commandExecution/requestApproval"} {
		frame, err := c.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m struct{ Method string }
		json.Unmarshal(frame, &m)
		if m.Method != want {
			t.Errorf("frame = %s, want %s handed to the caller", frame, want)
		}
	}
	time.Sleep(100 * time.Millisecond) // a wrong answer to a server request would arrive about now

	methods, _ := f.seen()
	want := []string{"initialize", "initialized", "thread/loaded/list", "thread/resume"}
	if len(methods) != len(want) {
		t.Fatalf("client sent %v, want exactly %v and no response to a server request", methods, want)
	}
	for i := range want {
		if methods[i] != want[i] {
			t.Fatalf("client sent %v, want %v", methods, want)
		}
	}
	var init struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	json.Unmarshal(f.params["initialize"], &init)
	if init.Capabilities["experimentalApi"] != true {
		t.Errorf("initialize params = %s, want capabilities.experimentalApi true", f.params["initialize"])
	}
	for _, banned := range []string{"requiredHumanApproval", "requestAttestation", "explicitGatewayOauth"} {
		if _, ok := init.Capabilities[banned]; ok {
			t.Errorf("initialize sets %s; the observer must not claim a human", banned)
		}
	}
	var resume map[string]any
	json.Unmarshal(f.params["thread/resume"], &resume)
	if len(resume) != 1 || resume["threadId"] != threadA {
		t.Errorf("thread/resume params = %s, want only threadId %s", f.params["thread/resume"], threadA)
	}
	for _, r := range f.raw {
		if json.Valid([]byte(r)) && containsKey(r, "jsonrpc") {
			t.Errorf("message %s carries a jsonrpc field; the server speaks without it", r)
		}
	}
	if len(c.Resume) == 0 {
		t.Error("Resume is empty, want the thread/resume response so history comes from it")
	}
}

func containsKey(raw, key string) bool {
	var m map[string]json.RawMessage
	json.Unmarshal([]byte(raw), &m)
	_, ok := m[key]
	return ok
}

func TestThreadComesFromTheRolloutNameWhenSeveralAreLoaded(t *testing.T) {
	f := newFake(t, threadA, threadB)
	cfg := f.cfg()
	cfg.Rollout = "/home/user/.codex/sessions/2026/11/11/rollout-2026-11-11T16-35-51-" + threadB + ".jsonl"
	c, err := connect(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Thread != threadB {
		t.Errorf("thread = %q, want %q from the rollout file name", c.Thread, threadB)
	}
}

func TestShipWithNoTranscriptStillConnects(t *testing.T) {
	f := newFake(t, threadA)
	c, err := connect(t, f.cfg())
	if err != nil || c.Thread != threadA {
		t.Fatalf("connect = %v, %v, want thread %s with no rollout named", c, err, threadA)
	}
}

func TestEveryFailureReturnsWithinTheTimeout(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(f *fakeServer)
	}{
		"initialize never answered": {func(f *fakeServer) { f.hang = "initialize" }},
		"list never answered":       {func(f *fakeServer) { f.hang = "thread/loaded/list" }},
		"resume never answered":     {func(f *fakeServer) { f.hang = "thread/resume" }},
		"resume refused":            {func(f *fakeServer) { f.resumeErr = true }},
		"no thread loaded":          {func(f *fakeServer) { f.loaded = nil }},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, threadA)
			tc.setup(f)
			start := time.Now()
			c, err := connect(t, f.cfg())
			if err == nil {
				t.Fatalf("connect succeeded with %v, want an error so the rollout is served", c)
			}
			if took := time.Since(start); took > 2*time.Second {
				t.Errorf("took %v, want about the 500ms timeout", took)
			}
		})
	}
}

func TestAServerThatDoesNotSpeakWebSocketFailsWithinTheTimeout(t *testing.T) {
	short, err := os.MkdirTemp("/tmp", "cxl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	sock := filepath.Join(short, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() { // accepts and says nothing, as `codex app-server --listen` does for a WebSocket client
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	resolved, _ := filepath.EvalSymlinks(short)
	start := time.Now()
	_, err = connect(t, codexlive.Config{Socket: sock, StartCmd: startCmd(sock), DaemonDir: resolved, Timeout: 500 * time.Millisecond})
	if err == nil {
		t.Fatal("connect succeeded, want an error")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %v, want about the 500ms timeout", took)
	}
}

func TestNextEndsWhenTheServerGoesAway(t *testing.T) {
	f := newFake(t, threadA)
	c, err := connect(t, f.cfg())
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	for _, conn := range f.conns {
		conn.CloseNow() // the server dying
	}
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.Next(ctx); err == nil {
		t.Error("Next returned a frame after the connection closed, want an error so the view falls back to the rollout")
	}
}

func TestOnlyASocketInTheDaemonDirectoryIsDialed(t *testing.T) {
	f := newFake(t, threadA)
	elsewhere, _ := filepath.EvalSymlinks(t.TempDir())
	notSocket := filepath.Join(f.dir, "plain")
	os.WriteFile(notSocket, nil, 0o600)
	link := filepath.Join(f.dir, "link.sock")
	os.Symlink(f.sock, link)
	outside := filepath.Join(elsewhere, "o.sock")
	os.Symlink(f.sock, outside)

	for name, tc := range map[string]struct {
		cfg     func() codexlive.Config
		dialled bool
	}{
		"a socket in the directory": {func() codexlive.Config { return f.cfg() }, true},
		"a symlink to it inside the directory": {func() codexlive.Config {
			c := f.cfg()
			c.Socket, c.StartCmd = link, startCmd(link)
			return c
		}, true},
		"a symlink from outside the directory whose target is inside": {func() codexlive.Config {
			c := f.cfg()
			c.Socket, c.StartCmd = outside, startCmd(outside)
			return c
		}, true},
		"a socket outside the directory": {func() codexlive.Config {
			c := f.cfg()
			c.DaemonDir = elsewhere
			return c
		}, false},
		"a regular file": {func() codexlive.Config {
			c := f.cfg()
			c.Socket, c.StartCmd = notSocket, startCmd(notSocket)
			return c
		}, false},
		"a path that is not there": {func() codexlive.Config {
			c := f.cfg()
			c.Socket, c.StartCmd = filepath.Join(f.dir, "gone.sock"), startCmd(filepath.Join(f.dir, "gone.sock"))
			return c
		}, false},
		"an option the start command does not carry": {func() codexlive.Config {
			c := f.cfg()
			c.StartCmd = startCmd(filepath.Join(f.dir, "other.sock"))
			return c
		}, false},
		"a start command with no --socket": {func() codexlive.Config {
			c := f.cfg()
			c.StartCmd = "'/bin/codex' '--fa-codex'"
			return c
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			_, before := f.seen()
			c, err := connect(t, tc.cfg())
			_, after := f.seen()
			if tc.dialled && (err != nil || c == nil) {
				t.Fatalf("connect failed: %v, want a connection", err)
			}
			if !tc.dialled && (err == nil || after != before) {
				t.Errorf("connect = %v, %v with %d new connections, want an error and no dial", c, err, after-before)
			}
		})
	}
}
