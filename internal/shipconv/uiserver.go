package shipconv

// A Copilot ship's live conversation from its --ui-server, and the file when the server is not there.

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

// splitStart splits a pane's start command into its arguments the way a shell would: tmux shows the argv
// single-quoted when it started one, and plain when it started a string.
func splitStart(cmd string) []string {
	var (
		args  []string
		cur   strings.Builder
		in    bool // a word is open, even an empty quoted one
		quote rune
	)
	flush := func() {
		if in {
			args = append(args, cur.String())
			cur.Reset()
			in = false
		}
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case quote == '"':
			switch {
			case c == '"':
				quote = 0
			case c == '\\' && i+1 < len(rs) && strings.ContainsRune(`"\$`+"`", rs[i+1]):
				i++
				cur.WriteRune(rs[i])
			default:
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			in = true
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			cur.WriteRune(c)
			in = true
		}
	}
	flush()
	return args
}

// Flags whose next argument is an order's text, so it is not read as one of ours.
var takesOrder = map[string]bool{"-i": true, "--interactive": true, "-p": true, "--prompt": true}

// startFlag finds a flag Admiral put in the start command: before the first "--", and never inside an order's text.
func startFlag(cmd, name string) (value string, present bool) {
	args := splitStart(cmd)
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if takesOrder[a] {
			i++
			continue
		}
		if a == name {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

var sessionUUID = regexp.MustCompile(`^[0-9a-f-]{36}$`)

// startedSession is the session id the launch gave the harness, read from the start command.
func startedSession(cmd string) string {
	if v, ok := startFlag(cmd, "--session-id"); ok && sessionUUID.MatchString(v) {
		return v
	}
	return ""
}

// uiServerPort is the loopback port Admiral gave the ship's Copilot with --ui-server --port, read from the start
// command, which the pane cannot rewrite.
func uiServerPort(startCmd string) (int, bool) {
	if _, on := startFlag(startCmd, "--ui-server"); !on {
		return 0, false
	}
	v, ok := startFlag(startCmd, "--port")
	n, err := strconv.Atoi(v)
	if !ok || err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return n, true
}

// observe follows the ship's server until ctx ends. A missing or refusing server is the ordinary case, so it
// retries slowly and the file serves meanwhile.
func (s *Service) observe(ctx context.Context, w *watch, port int) {
	const first, most = time.Second, 15 * time.Second
	wait := first
	for ctx.Err() == nil {
		if s.ownsPort(w.loc.ship, port) {
			began := time.Now()
			// The listener is checked again after connect, before the session id goes out.
			_ = conversation.ObserveUIServerChecked(ctx, port, w.loc.id, w.server, func() bool { return s.ownsPort(w.loc.ship, port) })
			if time.Since(began) > 10*time.Second {
				wait = first
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, most)
	}
}

func (s *Service) panePID(sh ships.Ship) (int, bool) {
	for _, sess := range s.Ships.Host.Sessions() {
		if sess.Name == sh.Session && sess.PaneID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			pid, err := sessionhost.NewServer(s.Tmux, s.Socket).ReadPanePID(ctx, sess.PaneID)
			return pid, err == nil
		}
	}
	return 0, false
}

func (s *Service) ownsPort(sh ships.Ship, port int) bool {
	if s.owns != nil {
		return s.owns(sh, port)
	}
	pid, ok := s.panePID(sh)
	return ok && listenerBelongsTo(port, pid)
}

const probeWait = 5 * time.Second

// listenerBelongsTo is true when every process listening on the port is the pane's process or one of its
// descendants. A foreign server on a taken port would answer session.resume for a session it does not own,
// so the observer attaches only to its own. Unknown counts as no.
func listenerBelongsTo(port, panePID int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), probeWait)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return false
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if n, err := strconv.Atoi(f); err == nil {
			pids = append(pids, n)
		}
	}
	return descendsFrom(pids, panePID, func(pid int) (int, bool) {
		out, err := exec.CommandContext(ctx, "ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
		n, perr := strconv.Atoi(strings.TrimSpace(string(out)))
		return n, err == nil && perr == nil
	})
}

// descendsFrom is true when pids is not empty and each is root or has it among its ancestors.
func descendsFrom(pids []int, root int, parent func(int) (int, bool)) bool {
	if len(pids) == 0 || root <= 1 {
		return false
	}
	for _, pid := range pids {
		ok := false
		for hops := 0; hops < 64 && pid > 1; hops++ {
			if pid == root {
				ok = true
				break
			}
			var found bool
			if pid, found = parent(pid); !found {
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
