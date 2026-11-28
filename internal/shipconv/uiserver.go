package shipconv

// A Copilot ship's live conversation from its --ui-server, and the file when the server is not there.

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/transcriptfs"
)

// uiServerPort is the loopback port Admiral gave the ship's Copilot with --ui-server --port, read from the start
// command, which the pane cannot rewrite.
func uiServerPort(startCmd string) (int, bool) {
	if _, on := transcriptfs.StartFlag(startCmd, "--ui-server"); !on {
		return 0, false
	}
	v, ok := transcriptfs.StartFlag(startCmd, "--port")
	n, err := strconv.Atoi(v)
	if !ok || err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return n, true
}

// observe follows the ship's server until ctx ends. A missing or refusing server is the ordinary case, so it
// retries slowly and the file serves meanwhile.
func (s *Service) observe(ctx context.Context, w *watch, port int) {
	first, most := orDefault(s.uiRetry, time.Second), 15*time.Second
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
