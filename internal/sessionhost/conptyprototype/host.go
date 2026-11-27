//go:build windows

package main

// PROTOTYPE host: owns pseudoconsoles, one emulator per pane, fans output out
// to attached bridges. Outlives the bridge.

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/conpty"
	"golang.org/x/sys/windows"
	"tuios.dev/vt"
	"tuios.dev/vt/snapshot"
)

func logDir() string {
	d := filepath.Join(os.Getenv("LOCALAPPDATA"), "admiral-proto-876-WIPE-ME")
	_ = os.MkdirAll(d, 0o700)
	return d
}

type host struct {
	mu    sync.Mutex
	panes map[string]*pane
	next  int
	stop  chan struct{}
}

type sub struct {
	ch   chan frame
	done chan struct{}
}

type frame struct {
	t byte
	p []byte
}

type pane struct {
	id, name string
	argv     []string
	answer   string
	started  time.Time

	mu       sync.Mutex
	cpty     *conpty.ConPty
	proc     windows.Handle
	job      windows.Handle
	pid      int
	emu      *vt.Emulator
	subs     map[*sub]struct{}
	pending  []map[string]any // events raised by emulator callbacks during Write
	exited   bool
	exitCode uint32
	cols     int
	rows     int
	tail     []byte

	evlog *os.File
	out   *os.File
	in    *os.File
}

var procReleasePseudoConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReleasePseudoConsole")

func runHost() error {
	lf, _ := os.OpenFile(filepath.Join(logDir(), "host.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if lf != nil {
		log.SetOutput(io.MultiWriter(os.Stderr, lf))
	}
	closer, accept, err := listen()
	if err != nil {
		return fmt.Errorf("listen %s: %w", pipeName(), err)
	}
	defer closer.Close()
	log.Printf("host pid %d listening on %s (ReleasePseudoConsole available: %v)", os.Getpid(), pipeName(), procReleasePseudoConsole.Find() == nil)
	h := &host{panes: map[string]*pane{}, stop: make(chan struct{})}
	go func() {
		for {
			c, err := accept()
			if err != nil {
				log.Printf("accept: %v", err)
				return
			}
			go h.serve(c)
		}
	}()
	<-h.stop
	log.Printf("host stopping")
	return nil
}

func (h *host) serve(c io.ReadWriteCloser) {
	defer c.Close()
	r := bufio.NewReaderSize(c, 1<<16)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		writeJSONLine(c, map[string]any{"error": err.Error()})
		return
	}
	switch req.Op {
	case "spawn":
		p, err := h.spawn(req)
		if err != nil {
			writeJSONLine(c, map[string]any{"error": err.Error()})
			return
		}
		writeJSONLine(c, map[string]any{"pane": p.id, "pid": p.pid})
	case "list":
		writeJSONLine(c, h.list())
	case "kill":
		if p := h.get(req.Pane); p != nil {
			p.kill()
		}
		writeJSONLine(c, map[string]any{"ok": true})
	case "dump":
		p := h.get(req.Pane)
		if p == nil {
			writeJSONLine(c, map[string]any{"error": "no pane"})
			return
		}
		writeJSONLine(c, map[string]any{"text": p.dump()})
	case "stop":
		writeJSONLine(c, map[string]any{"ok": true})
		close(h.stop)
	case "attach":
		p := h.get(req.Pane)
		if p == nil {
			writeJSONLine(c, map[string]any{"error": "no pane"})
			return
		}
		writeJSONLine(c, map[string]any{"ok": true})
		p.attach(r, c, req)
	default:
		writeJSONLine(c, map[string]any{"error": "unknown op " + req.Op})
	}
}

func writeJSONLine(w io.Writer, v any) {
	b, _ := json.Marshal(v)
	_, _ = w.Write(append(b, '\n'))
}

func (h *host) get(id string) *pane {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.panes[id]
}

func (h *host) list() []map[string]any {
	h.mu.Lock()
	ps := make([]*pane, 0, len(h.panes))
	for _, p := range h.panes {
		ps = append(ps, p)
	}
	h.mu.Unlock()
	sort.Slice(ps, func(i, j int) bool { return ps[i].started.Before(ps[j].started) })
	out := []map[string]any{}
	for _, p := range ps {
		p.mu.Lock()
		out = append(out, map[string]any{"pane": p.id, "name": p.name, "pid": p.pid, "exited": p.exited, "exit_code": p.exitCode, "cols": p.cols, "rows": p.rows, "answer": p.answer, "argv": p.argv})
		p.mu.Unlock()
	}
	return out
}

func (h *host) spawn(req request) (*pane, error) {
	if len(req.Argv) == 0 {
		return nil, fmt.Errorf("no argv")
	}
	if req.Cols <= 0 {
		req.Cols, req.Rows = 120, 36
	}
	h.mu.Lock()
	h.next++
	id := fmt.Sprintf("p%d", h.next)
	h.mu.Unlock()

	// x/conpty resolves a bare name against attr.Dir, not PATH.
	if !filepath.IsAbs(req.Argv[0]) {
		if lp, err := exec.LookPath(req.Argv[0]); err == nil {
			req.Argv[0] = lp
		}
	}
	// Drop what this prototype inherits from the Claude Code session it was
	// started from; a host started at logon would not have it.
	var env []string
	for _, kv := range os.Environ() {
		k := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if strings.HasPrefix(k, "CLAUDE") || strings.HasPrefix(k, "CODEX_") {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range req.Env {
		env = append(env, k+"="+v)
	}
	env = append(env, "ADMIRAL_PANE="+id)

	cp, err := conpty.New(req.Cols, req.Rows, 0)
	if err != nil {
		return nil, fmt.Errorf("conpty: %w", err)
	}
	job, err := newKillJob()
	if err != nil {
		cp.Close()
		return nil, fmt.Errorf("job: %w", err)
	}
	pid, handle, err := cp.Spawn(req.Argv[0], req.Argv, &syscall.ProcAttr{Dir: req.Dir, Env: env, Sys: &syscall.SysProcAttr{}})
	if err != nil {
		cp.Close()
		windows.CloseHandle(job)
		return nil, fmt.Errorf("spawn: %w", err)
	}
	// PROTOTYPE: assigned after creation (a race the real host closes with
	// PROC_THREAD_ATTRIBUTE_JOB_LIST).
	if err := windows.AssignProcessToJobObject(job, windows.Handle(handle)); err != nil {
		log.Printf("%s: assign job: %v", id, err)
	}
	if procReleasePseudoConsole.Find() == nil {
		r, _, _ := procReleasePseudoConsole.Call(cp.Fd())
		log.Printf("%s: ReleasePseudoConsole hr=%#x", id, r)
	}

	name := req.Name
	if name == "" {
		name = filepath.Base(req.Argv[0])
	}
	answer := req.Answer
	if answer == "" {
		answer = "host"
	}
	p := &pane{id: id, name: name, argv: req.Argv, answer: answer, started: time.Now(), cpty: cp, proc: windows.Handle(handle), job: job, pid: pid,
		emu: vt.NewEmulator(req.Cols, req.Rows), subs: map[*sub]struct{}{}, cols: req.Cols, rows: req.Rows}
	p.emu.SetScrollbackMaxLines(10000)
	base := filepath.Join(logDir(), fmt.Sprintf("%s-%s-%s", time.Now().Format("150405"), id, sanitize(name)))
	p.evlog, _ = os.Create(base + ".events.jsonl")
	p.out, _ = os.Create(base + ".out.bin")
	p.in, _ = os.Create(base + ".in.jsonl")
	p.emu.SetCallbacks(vt.Callbacks{
		Bell:      func() { p.pending = append(p.pending, map[string]any{"kind": "bell"}) },
		Title:     func(s string) { p.pending = append(p.pending, map[string]any{"kind": "title", "title": s}) },
		AltScreen: func(on bool) { p.pending = append(p.pending, map[string]any{"kind": "altscreen", "on": on}) },
		EnableMode: func(m ansi.Mode) {
			p.pending = append(p.pending, map[string]any{"kind": "mode", "mode": m.Mode(), "on": true})
		},
		DisableMode: func(m ansi.Mode) {
			p.pending = append(p.pending, map[string]any{"kind": "mode", "mode": m.Mode(), "on": false})
		},
		Notify: func(t, b string) {
			p.pending = append(p.pending, map[string]any{"kind": "notify", "title": t, "body": b})
		},
	})

	h.mu.Lock()
	h.panes[id] = p
	h.mu.Unlock()
	log.Printf("%s: spawned %q pid %d %dx%d answer=%s", id, req.Argv, pid, req.Cols, req.Rows, answer)
	go p.readLoop()
	go p.waitLoop()
	// A harness assumes it is focused until told otherwise, and Codex rings only
	// when unfocused. Nobody is looking at a new pane until a browser focuses it.
	go func() {
		time.Sleep(2 * time.Second)
		p.mu.Lock()
		idle := len(p.subs) == 0 && !p.exited
		p.mu.Unlock()
		if idle {
			p.focusOut("spawned with no browser")
		}
	}()
	return p, nil
}

func newKillJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return job, err
}

func (p *pane) since() int64 { return time.Since(p.started).Milliseconds() }

// logEvent writes to the pane's event log and returns the event with a timestamp.
func (p *pane) logEvent(ev map[string]any) map[string]any {
	ev["t"] = p.since()
	if p.evlog != nil {
		b, _ := json.Marshal(ev)
		_, _ = p.evlog.Write(append(b, '\n'))
	}
	return ev
}

type seqPat struct {
	name string
	re   *regexp.Regexp
}

var seqPats = []seqPat{
	{"query:DA1", regexp.MustCompile(`\x1b\[0?c`)},
	{"query:DA2", regexp.MustCompile(`\x1b\[>0?c`)},
	{"query:kitty-flags", regexp.MustCompile(`\x1b\[\?u`)},
	{"kitty:push", regexp.MustCompile(`\x1b\[>\d*u`)},
	{"kitty:pop", regexp.MustCompile(`\x1b\[<\d*u`)},
	{"query:CPR", regexp.MustCompile(`\x1b\[\??6n`)},
	{"query:XTVERSION", regexp.MustCompile(`\x1b\[>0?q`)},
	{"query:DECRQM", regexp.MustCompile(`\x1b\[\?\d+\$p`)},
	{"query:osc-color", regexp.MustCompile(`\x1b\](?:10|11|12|4;\d+);\?(?:\x07|\x1b\\)`)},
	{"query:modifyOtherKeys", regexp.MustCompile(`\x1b\[>4;?\d*m`)},
	{"decmode", regexp.MustCompile(`\x1b\[\?[\d;]+[hl]`)},
	{"osc:title", regexp.MustCompile(`\x1b\][012];[^\x07\x1b]*(?:\x07|\x1b\\)`)},
	{"osc:notify", regexp.MustCompile(`\x1b\](?:9|777|99);[^\x07\x1b]*(?:\x07|\x1b\\)`)},
	{"osc:other", regexp.MustCompile(`\x1b\](?:[3-9]|[1-9]\d{1,2});[^\x07\x1b]{0,80}`)},
	{"dcs", regexp.MustCompile(`\x1bP[^\x1b]{0,40}`)},
	{"apc", regexp.MustCompile(`\x1b_[^\x1b]{0,40}`)},
	{"bel-byte", regexp.MustCompile(`\x07`)},
}

// scan logs interesting sequences in a chunk (with a short tail from the last
// chunk so split sequences are still seen; matches wholly in the tail are skipped).
func (p *pane) scan(chunk []byte) []map[string]any {
	buf := append(append([]byte{}, p.tail...), chunk...)
	off := len(p.tail)
	var evs []map[string]any
	for _, sp := range seqPats {
		for _, m := range sp.re.FindAllIndex(buf, -1) {
			if m[1] <= off {
				continue
			}
			evs = append(evs, map[string]any{"kind": "seq", "seq": sp.name, "raw": fmt.Sprintf("%q", buf[m[0]:m[1]])})
		}
	}
	if len(buf) > 48 {
		p.tail = append([]byte{}, buf[len(buf)-48:]...)
	} else {
		p.tail = buf
	}
	return evs
}

func (p *pane) readLoop() {
	buf := make([]byte, 64<<10)
	for {
		n, err := p.cpty.Read(buf)
		if n > 0 {
			chunk := append([]byte{}, buf[:n]...)
			if p.out != nil {
				_, _ = p.out.Write(chunk)
			}
			p.mu.Lock()
			evs := p.scan(chunk)
			_, _ = p.emu.Write(chunk)
			resp := p.emu.TakeResponses()
			evs = append(evs, p.pending...)
			p.pending = nil
			answer := p.answer
			if len(resp) > 0 {
				evs = append(evs, map[string]any{"kind": "reply", "by": answer, "sent": answer == "host", "raw": fmt.Sprintf("%q", resp)})
			}
			for i := range evs {
				p.logEvent(evs[i])
			}
			p.broadcast(frame{frameOutput, chunk})
			for _, ev := range evs {
				if ev["kind"] == "seq" && ev["seq"] == "decmode" {
					continue // noisy; it is in the log
				}
				b, _ := json.Marshal(ev)
				p.broadcast(frame{frameEvent, b})
			}
			p.mu.Unlock()
			if answer == "host" && len(resp) > 0 {
				_, _ = p.cpty.Write(resp)
			}
		}
		if err != nil {
			p.mu.Lock()
			b, _ := json.Marshal(p.logEvent(map[string]any{"kind": "eof", "err": err.Error()}))
			p.broadcast(frame{frameEvent, b})
			p.mu.Unlock()
			return
		}
	}
}

func (p *pane) waitLoop() {
	_, _ = windows.WaitForSingleObject(p.proc, windows.INFINITE)
	var code uint32
	_ = windows.GetExitCodeProcess(p.proc, &code)
	p.mu.Lock()
	p.exited, p.exitCode = true, code
	b, _ := json.Marshal(p.logEvent(map[string]any{"kind": "exit", "code": code, "hex": fmt.Sprintf("%#x", code)}))
	p.broadcast(frame{frameEvent, b})
	p.mu.Unlock()
	log.Printf("%s: exited %#x", p.id, code)
	// remain-on-exit: the emulator stays; the pseudoconsole goes.
	time.Sleep(500 * time.Millisecond)
	_ = p.cpty.Close()
}

func (p *pane) kill() {
	log.Printf("%s: kill", p.id)
	_ = p.cpty.Close()
	go func() {
		time.Sleep(2 * time.Second)
		_ = windows.TerminateJobObject(p.job, 1)
	}()
}

// broadcast must be called with p.mu held.
func (p *pane) broadcast(f frame) {
	for s := range p.subs {
		select {
		case s.ch <- f:
		default:
			log.Printf("%s: subscriber too slow, dropping it", p.id)
			delete(p.subs, s)
			close(s.done)
		}
	}
}

func trimLine(s string) string { return strings.TrimRight(strings.TrimRight(s, "\r"), " ") }

func (p *pane) snapshotLocked(mode string) map[string]any {
	e := p.emu
	screen := strings.Split(e.Render(), "\n")
	for i := range screen {
		screen[i] = trimLine(screen[i])
	}
	var sb []string
	if !e.IsAltScreen() {
		n := e.ScrollbackLen()
		from := max(0, n-2000)
		for i := from; i < n; i++ {
			sb = append(sb, trimLine(e.ScrollbackLine(i).Render()))
		}
	}
	modes := e.GetModes()
	mouse := "none"
	switch {
	case modes[1003]:
		mouse = "any"
	case modes[1002]:
		mouse = "button"
	case modes[1000]:
		mouse = "press"
	}
	cur := e.CursorPosition()
	j := map[string]any{
		"type":       "join",
		"screen":     strings.Join(screen, "\n"),
		"scrollback": strings.Join(sb, "\n"),
		"cursor":     map[string]any{"x": cur.X, "y": cur.Y, "visible": !e.IsCursorHidden()},
		"modes":      map[string]any{"alternate": e.IsAltScreen(), "application_cursor": e.ApplicationCursorKeys(), "bracketed_paste": e.BracketedPasteEnabled(), "mouse": mouse, "sgr_mouse": modes[1006], "focus": modes[1004]},
		"size":       map[string]any{"cols": p.cols, "rows": p.rows},
		"ship":       map[string]any{"status": map[bool]string{true: "anchored", false: "underway"}[p.exited], "exit_code": p.exitCode},
		"snap":       mode,
	}
	if mode == "replay" {
		st := snapshot.Capture(e, p.cols, p.rows, 2000, 0)
		j["replay"] = base64.StdEncoding.EncodeToString(snapshot.Replay(st))
	}
	return j
}

func (p *pane) dump() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var b strings.Builder
	n := p.emu.ScrollbackLen()
	fmt.Fprintf(&b, "== %s %s %dx%d scrollback=%d alt=%v ==\n", p.id, p.name, p.cols, p.rows, n, p.emu.IsAltScreen())
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "S%05d|%s\n", i, strings.TrimRight(p.emu.ScrollbackLine(i).String(), " "))
	}
	for i, l := range strings.Split(ansi.Strip(p.emu.Render()), "\n") {
		fmt.Fprintf(&b, "V%05d|%s\n", i, trimLine(l))
	}
	return b.String()
}

func (p *pane) attach(r *bufio.Reader, c io.ReadWriteCloser, req request) {
	fw := &frameWriter{w: c}
	s := &sub{ch: make(chan frame, 8192), done: make(chan struct{})}
	if req.Cols > 0 && req.Rows > 0 {
		p.resize(req.Cols, req.Rows)
	}
	p.mu.Lock()
	j := p.snapshotLocked(req.Snap)
	p.subs[s] = struct{}{}
	p.mu.Unlock()
	b, _ := json.Marshal(j)
	if err := fw.write('j', b); err != nil {
		return
	}
	defer func() {
		p.mu.Lock()
		if _, ok := p.subs[s]; ok {
			delete(p.subs, s)
			close(s.done)
		}
		last := len(p.subs) == 0 && !p.exited
		p.mu.Unlock()
		if last {
			p.focusOut("last browser left")
		}
	}()
	go func() {
		for {
			select {
			case f := <-s.ch:
				if fw.write(f.t, f.p) != nil {
					c.Close()
					return
				}
			case <-s.done:
				c.Close()
				return
			}
		}
	}()
	for {
		t, payload, err := readFrame(r)
		if err != nil {
			return
		}
		switch t {
		case frameInput:
			if p.in != nil {
				b, _ := json.Marshal(map[string]any{"t": p.since(), "raw": fmt.Sprintf("%q", payload)})
				_, _ = p.in.Write(append(b, '\n'))
			}
			p.mu.Lock()
			exited := p.exited
			p.mu.Unlock()
			if !exited {
				_, _ = p.cpty.Write(payload)
			}
		case frameResize:
			var sz struct{ Cols, Rows int }
			if json.Unmarshal(payload, &sz) == nil && sz.Cols > 0 && sz.Rows > 0 {
				p.resize(sz.Cols, sz.Rows)
			}
		}
	}
}

func (p *pane) resize(cols, rows int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cols == p.cols && rows == p.rows {
		return
	}
	p.cols, p.rows = cols, rows
	if !p.exited {
		_ = p.cpty.Resize(cols, rows)
	}
	p.emu.Resize(cols, rows)
	b, _ := json.Marshal(p.logEvent(map[string]any{"kind": "size", "cols": cols, "rows": rows, "scrollback": p.emu.ScrollbackLen()}))
	p.broadcast(frame{frameEvent, b})
}

// focusOut tells the pane's program that no one is looking (a focus-out
// report, which conhost turns into a FOCUS_EVENT record).
func (p *pane) focusOut(why string) {
	_, _ = p.cpty.Write([]byte("\x1b[O"))
	p.mu.Lock()
	b, _ := json.Marshal(p.logEvent(map[string]any{"kind": "focus-out", "why": why}))
	p.broadcast(frame{frameEvent, b})
	p.mu.Unlock()
}
