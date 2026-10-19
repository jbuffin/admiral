package sessionhost

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var errClosed = fmt.Errorf("control connection closed")

// Client is a control-mode client of its own, attached to one session, for the
// bridge (ADR 0011). Unlike the Host's connection it never waits for a reply:
// each command carries a callback, and callbacks and notifications all run on
// one goroutine in the order tmux sent them. So a callback that ends a join
// snapshot and the %output lines after it can be told apart exactly. Neither
// may block.
type Client struct {
	cmd *exec.Cmd

	mu      sync.Mutex
	stdin   io.WriteCloser
	pending []func(reply)
	closed  bool

	done chan struct{}
}

// Attach starts `tmux -C attach-session -t target` on sock. notify gets every
// notification line (%output and the like). target must be an exact one
// ("=name:"), because tmux resolves a bare name to any session it prefixes.
func Attach(tmux, sock, target string, notify func(line string)) (*Client, error) {
	cmd := exec.Command(tmux, "-f", "/dev/null", "-S", sock, "-u", "-C", "attach-session", "-t", target)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") && !strings.HasPrefix(kv, "TMUX_PANE=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, stdin: stdin, done: make(chan struct{})}
	p := &parser{notify: notify, onReply: c.reply}
	go func() {
		_ = p.run(stdout)
		c.fail()
		_ = cmd.Wait()
		close(c.done)
	}()
	return c, nil
}

func (c *Client) reply(r reply) {
	c.mu.Lock()
	var f func(reply)
	if len(c.pending) > 0 {
		f, c.pending = c.pending[0], c.pending[1:]
	}
	c.mu.Unlock()
	if f != nil {
		f(r)
	}
}

// fail ends every command still waiting.
func (c *Client) fail() {
	c.mu.Lock()
	c.closed = true
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, f := range pending {
		f(reply{err: errClosed})
	}
}

// Do sends the commands in one write, so no other command comes between them,
// and calls done with their replies once the last one has arrived: on the
// reader goroutine, or at once with every error set if the client is closed.
// errs[i] is tmux's refusal of command i, if any.
func (c *Client) Do(lines []string, done func(out [][]string, errs []error)) {
	out := make([][]string, len(lines))
	errs := make([]error, len(lines))
	entries := make([]func(reply), len(lines))
	for i := range lines {
		entries[i] = func(r reply) {
			out[i], errs[i] = r.lines, r.err
			if i == len(lines)-1 && done != nil {
				done(out, errs)
			}
		}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		if done != nil {
			for i := range errs {
				errs[i] = errClosed
			}
			done(out, errs)
		}
		return
	}
	c.pending = append(c.pending, entries...)
	_, err := io.WriteString(c.stdin, strings.Join(lines, "\n")+"\n")
	c.mu.Unlock()
	if err != nil {
		_ = c.stdin.Close()
	}
}

// Close detaches: tmux ends the client when its input ends.
func (c *Client) Close() {
	c.mu.Lock()
	_ = c.stdin.Close()
	c.mu.Unlock()
}

// Done is closed after the client's process has ended.
func (c *Client) Done() <-chan struct{} { return c.done }
