//go:build windows

package main

// PROTOTYPE stub of the bridge: an HTTP server on 127.0.0.1 that serves the
// page, spawns presets in the host, and relays one WebSocket per browser to a
// host attach.

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/coder/websocket"
	"golang.org/x/sys/windows"
)

//go:embed index.html
var indexHTML []byte

//go:embed probe.js
var probeJS []byte

const addr = "127.0.0.1:7876"

func dialHost() (net.Conn, error) {
	t := 2 * time.Second
	return winio.DialPipe(pipeName(), &t)
}

func hostCall(req request) (json.RawMessage, error) {
	c, err := dialHost()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	writeJSONLine(c, req)
	line, err := bufio.NewReader(c).ReadBytes('\n')
	return json.RawMessage(line), err
}

// startHost starts `proto host` detached, so it outlives this process.
func startHost() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	const breakaway = 0x01000000 // CREATE_BREAKAWAY_FROM_JOB
	flags := uint32(windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP)
	try := func(f uint32) error {
		cmd := exec.Command(exe, "host")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: f, HideWindow: true}
		return cmd.Start()
	}
	if err := try(flags | breakaway); err != nil {
		log.Printf("host start with CREATE_BREAKAWAY_FROM_JOB failed (%v); retrying without: the host may share this process's fate", err)
		if err := try(flags); err != nil {
			return err
		}
	} else {
		log.Printf("host started with CREATE_BREAKAWAY_FROM_JOB")
	}
	for range 50 {
		if c, err := dialHost(); err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("host did not come up on %s", pipeName())
}

func shipDir() string {
	d := filepath.Join(os.Getenv("USERPROFILE"), "src", "proto-ship-876")
	if _, err := os.Stat(d); err != nil {
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "README.md"), []byte("# PROTOTYPE ship for admiral #876, wipe me\n\nA scratch repo the ConPTY prototype launches harnesses in.\n"), 0o644)
		_ = exec.Command("git", "-C", d, "init", "-q").Run()
	}
	return d
}

func slash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// preset builds the argv for a harness, with hooks that record what
// environment they receive (ADMIRAL_PANE) in hooks.jsonl.
func preset(name string) ([]string, error) {
	probe := filepath.Join(logDir(), "probe.js")
	if err := os.WriteFile(probe, probeJS, 0o644); err != nil {
		return nil, err
	}
	node, _ := exec.LookPath("node")
	switch name {
	case "claude":
		exe, err := exec.LookPath("claude")
		if err != nil {
			return nil, err
		}
		hook := func(event string) []map[string]any {
			return []map[string]any{{"hooks": []map[string]any{
				{"type": "command", "command": node, "args": []string{probe, "claude-exec", event}},
				{"type": "command", "command": fmt.Sprintf(`node "%s" claude-shell %s`, slash(probe), event)},
			}}}
		}
		settings := map[string]any{"hooks": map[string]any{
			"UserPromptSubmit": hook("UserPromptSubmit"),
			"Stop":             hook("Stop"),
			"Notification":     hook("Notification"),
		}}
		sf := filepath.Join(logDir(), "claude-settings.json")
		b, _ := json.MarshalIndent(settings, "", "  ")
		if err := os.WriteFile(sf, b, 0o644); err != nil {
			return nil, err
		}
		return []string{exe, "--settings", sf}, nil
	case "codex":
		exe := filepath.Join(os.Getenv("APPDATA"), `npm\node_modules\@openai\codex\node_modules\@openai\codex-win32-x64\vendor\x86_64-pc-windows-msvc\bin\codex.exe`)
		if _, err := os.Stat(exe); err != nil {
			if exe, err = exec.LookPath("codex.exe"); err != nil {
				return nil, fmt.Errorf("no codex.exe")
			}
		}
		h := func(event string) string {
			return fmt.Sprintf(`hooks.%s=[{hooks=[{type="command",command='node "%s" codex %s'}]}]`, event, slash(probe), event)
		}
		// This machine's config.toml names a model its ChatGPT account can't use.
		model := os.Getenv("PROTO_CODEX_MODEL")
		if model == "" {
			model = "gpt-5.6-luna"
		}
		return []string{exe, "--dangerously-bypass-hook-trust", "-m", model, "-c", `model_reasoning_effort="low"`,
			"-c", h("UserPromptSubmit"), "-c", h("Stop"), "-c", h("PermissionRequest"),
			"-c", `tui.notification_method="bel"`, // as internal/launcher codexFlags
			"-c", fmt.Sprintf(`notify=["node","%s","codex-notify"]`, slash(probe)),
		}, nil
	case "pwsh":
		if exe, err := exec.LookPath("pwsh"); err == nil {
			return []string{exe, "-NoLogo"}, nil
		}
		return []string{"powershell.exe", "-NoLogo"}, nil
	case "cmd":
		return []string{"cmd.exe"}, nil
	}
	return nil, fmt.Errorf("unknown preset %q", name)
}

func runBridge(autostartHost bool) error {
	if c, err := dialHost(); err == nil {
		c.Close()
		log.Printf("host already running on %s", pipeName())
	} else if autostartHost {
		if err := startHost(); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("no host on %s (run `proto host`)", pipeName())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(indexHTML)
	})
	relay := func(w http.ResponseWriter, req request) {
		res, err := hostCall(req)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(res)
	}
	mux.HandleFunc("GET /api/panes", func(w http.ResponseWriter, r *http.Request) { relay(w, request{Op: "list"}) })
	mux.HandleFunc("POST /api/spawn", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		argv, err := preset(q.Get("preset"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var cols, rows int
		fmt.Sscan(q.Get("cols"), &cols)
		fmt.Sscan(q.Get("rows"), &rows)
		relay(w, request{Op: "spawn", Name: q.Get("preset"), Argv: argv, Dir: shipDir(), Cols: cols, Rows: rows, Answer: q.Get("answer")})
	})
	mux.HandleFunc("POST /api/kill", func(w http.ResponseWriter, r *http.Request) {
		relay(w, request{Op: "kill", Pane: r.URL.Query().Get("pane")})
	})
	mux.HandleFunc("POST /api/stop-host", func(w http.ResponseWriter, r *http.Request) { relay(w, request{Op: "stop"}) })
	mux.HandleFunc("GET /api/dump", func(w http.ResponseWriter, r *http.Request) {
		res, err := hostCall(request{Op: "dump", Pane: r.URL.Query().Get("pane")})
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		var d struct{ Text, Error string }
		_ = json.Unmarshal(res, &d)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, d.Text+d.Error)
	})
	mux.HandleFunc("GET /api/hooks", func(w http.ResponseWriter, r *http.Request) {
		b, _ := os.ReadFile(filepath.Join(logDir(), "hooks.jsonl"))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /ws/bridge/{pane}", wsBridge)
	log.Printf("bridge pid %d on http://%s  (logs: %s)", os.Getpid(), addr, logDir())
	return http.ListenAndServe(addr, mux)
}

func wsBridge(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(16 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer ws.CloseNow()

	// First browser message must be join.
	_, msg, err := ws.Read(ctx)
	if err != nil {
		return
	}
	var j struct {
		Type       string
		Cols, Rows int
	}
	if json.Unmarshal(msg, &j) != nil || j.Type != "join" {
		return
	}
	c, err := dialHost()
	if err != nil {
		_ = ws.Write(ctx, websocket.MessageText, []byte(`{"type":"error","error":"host unreachable"}`))
		return
	}
	defer c.Close()
	writeJSONLine(c, request{Op: "attach", Pane: r.PathValue("pane"), Cols: j.Cols, Rows: j.Rows, Snap: r.URL.Query().Get("snap")})
	hr := bufio.NewReaderSize(c, 1<<16)
	line, err := hr.ReadBytes('\n')
	if err != nil || strings.Contains(string(line), `"error"`) {
		_ = ws.Write(ctx, websocket.MessageText, []byte(`{"type":"error","error":"attach failed"}`))
		return
	}
	fw := &frameWriter{w: c}

	go func() { // host → browser
		defer cancel()
		for {
			t, p, err := readFrame(hr)
			if err != nil {
				return
			}
			switch t {
			case frameOutput:
				err = ws.Write(ctx, websocket.MessageBinary, p)
			case 'j':
				err = ws.Write(ctx, websocket.MessageText, p)
			case frameEvent:
				var ev map[string]any
				_ = json.Unmarshal(p, &ev)
				if ev["kind"] == "size" {
					b, _ := json.Marshal(map[string]any{"type": "size", "cols": ev["cols"], "rows": ev["rows"]})
					_ = ws.Write(ctx, websocket.MessageText, b)
				}
				b, _ := json.Marshal(map[string]any{"type": "event", "event": ev})
				err = ws.Write(ctx, websocket.MessageText, b)
			}
			if err != nil {
				return
			}
		}
	}()
	for { // browser → host
		_, msg, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var m struct {
			Type       string
			Data       string
			Cols, Rows int
		}
		if json.Unmarshal(msg, &m) != nil {
			continue
		}
		switch m.Type {
		case "input":
			err = fw.write(frameInput, []byte(m.Data))
		case "resize":
			b, _ := json.Marshal(map[string]int{"cols": m.Cols, "rows": m.Rows})
			err = fw.write(frameResize, b)
		}
		if err != nil {
			return
		}
	}
}
