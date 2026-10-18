package sources

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// maxLine is the longest line a plugin may send; a longer one is dropped.
	maxLine = 16 << 20
	// callTimeout is how long a request waits for its answer.
	callTimeout = 10 * time.Second
	// stopGrace is how long a plugin gets to exit after its stdin closes.
	stopGrace = time.Second
)

var (
	errTimeout    = errors.New("the order source did not answer in time")
	errNotRunning = errors.New("the order source is not running")
)

// rpcError is a JSON-RPC error a plugin answered with.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return cleanMsg(e.Message) }

type reply struct {
	result json.RawMessage
	err    *rpcError
}

// proc is one running plugin and the JSON-RPC conversation with it: one
// message per line on its stdin and stdout.
type proc struct {
	key    string
	cmd    *exec.Cmd
	stdin  *os.File
	onNote func(method string, params json.RawMessage)

	wmu sync.Mutex // one writer at a time

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan reply

	exited  chan struct{} // closed once the process is gone and its stdout drained
	exitErr error
}

// startProc runs the plugin's command in its folder. Its stderr goes to
// Admiral's log; onNote receives the notifications it sends.
func startProc(key string, m Manifest, dir string, onNote func(string, json.RawMessage)) (*proc, error) {
	exe, err := resolveCommand(dir, m.Command)
	if err != nil {
		return nil, err
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	cmd := exec.Command(exe, m.Args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout = inR, outW
	cmd.Stderr = &linePrefixer{prefix: "source " + key + ": ", out: os.Stderr}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Start()
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return nil, fmt.Errorf("start %s: %w", exe, err)
	}
	p := &proc{key: key, cmd: cmd, stdin: inW, onNote: onNote, pending: map[int64]chan reply{}, exited: make(chan struct{})}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		p.read(outR)
		outR.Close()
	}()
	go func() {
		err := cmd.Wait()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // whatever it left behind
		<-drained
		p.mu.Lock()
		p.exitErr = err
		p.mu.Unlock()
		inW.Close()
		close(p.exited)
	}()
	return p, nil
}

// resolveCommand finds the executable: a command with a slash is relative to
// the plugin's folder, one without is looked up on Admiral's PATH.
func resolveCommand(dir, command string) (string, error) {
	if strings.Contains(command, "/") {
		if !filepath.IsAbs(command) {
			command = filepath.Join(dir, command)
		}
		return command, nil
	}
	exe, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("command %q not found on PATH", command)
	}
	return filepath.Abs(exe)
}

// exitReason says why the process is gone.
func (p *proc) exitReason() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exitErr != nil {
		return "the order source exited: " + p.exitErr.Error()
	}
	return "the order source exited"
}

func (p *proc) logf(format string, a ...any) {
	log.Printf("source %s: %s", p.key, fmt.Sprintf(format, a...))
}

// read handles what the plugin writes until its stdout closes. Anything wrong
// with one line drops that line and goes on.
func (p *proc) read(f *os.File) {
	br := bufio.NewReaderSize(f, 64<<10)
	for {
		line, tooLong, err := readLine(br)
		if err != nil {
			return
		}
		if tooLong {
			p.logf("protocol error: a line over %d MiB was dropped", maxLine>>20)
			continue
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		p.handle(line)
	}
}

// readLine reads one line of at most maxLine bytes. A longer line is read to
// its end and reported as tooLong without being kept.
func readLine(br *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		chunk, e := br.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > maxLine {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		switch {
		case e == nil:
			return line, tooLong, nil
		case errors.Is(e, bufio.ErrBufferFull):
		default:
			return nil, false, e
		}
	}
}

type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (p *proc) handle(line []byte) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		p.logf("protocol error: a line that is not a JSON object was dropped")
		return
	}
	hasID := len(m.ID) > 0 && string(m.ID) != "null"
	switch {
	case m.Method != "" && !hasID:
		p.onNote(m.Method, m.Params)
	case m.Method != "":
		p.logf("protocol error: the plugin sent a request %q, which Admiral does not answer", m.Method)
		id := m.ID
		_ = p.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": &rpcError{Code: -32601, Message: "Admiral takes no requests from plugins"}}, callTimeout)
	case hasID:
		id, err := strconv.ParseInt(string(m.ID), 10, 64)
		if err != nil {
			p.logf("protocol error: a reply with the id %s was dropped", m.ID)
			return
		}
		p.mu.Lock()
		ch := p.pending[id]
		delete(p.pending, id)
		p.mu.Unlock()
		if ch == nil {
			p.logf("protocol error: a reply to request %d, which is not waiting, was dropped", id)
			return
		}
		ch <- reply{m.Result, m.Error}
	default:
		p.logf("protocol error: a message with neither id nor method was dropped")
	}
}

func (p *proc) write(v any, timeout time.Duration) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_ = p.stdin.SetWriteDeadline(time.Now().Add(timeout))
	_, err = p.stdin.Write(append(data, '\n'))
	return err
}

// call sends a request and waits for its answer. It fails after timeout, when
// ctx ends and when the process exits.
func (p *proc) call(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	ch := make(chan reply, 1)
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	p.pending[id] = ch
	p.mu.Unlock()
	forget := func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}
	if err := p.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}, timeout); err != nil {
		forget()
		select {
		case <-p.exited:
			return nil, errors.New(p.exitReason())
		default:
		}
		return nil, fmt.Errorf("could not write to the order source: %w", err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return r.result, nil
	case <-p.exited:
		forget()
		select { // the answer may have arrived just before the exit
		case r := <-ch:
			if r.err != nil {
				return nil, r.err
			}
			return r.result, nil
		default:
		}
		return nil, errors.New(p.exitReason())
	case <-timer.C:
		forget()
		return nil, errTimeout
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}

// stop closes the plugin's stdin, gives it a moment to exit, then kills its
// process group.
func (p *proc) stop() {
	_ = p.stdin.Close()
	select {
	case <-p.exited:
		return
	case <-time.After(stopGrace):
	}
	p.kill()
}

// kill ends the plugin and everything it started, and waits for the exit.
func (p *proc) kill() {
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	select {
	case <-p.exited:
	case <-time.After(5 * time.Second):
	}
}

// linePrefixer writes whole lines to out, each starting with prefix.
type linePrefixer struct {
	prefix string
	out    io.Writer
	mu     sync.Mutex
	buf    []byte
}

func (l *linePrefixer) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, b...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		fmt.Fprintf(l.out, "%s%s\n", l.prefix, l.buf[:i])
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > 64<<10 { // a plugin that never ends its line
		fmt.Fprintf(l.out, "%s%s\n", l.prefix, l.buf)
		l.buf = nil
	}
	return len(b), nil
}
