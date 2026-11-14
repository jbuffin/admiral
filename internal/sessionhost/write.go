package sessionhost

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StateOption is needs_you or working.
const StateOption = Prefix + "state"

// ReasonOption is set with needs_you and unset with working.
const ReasonOption = Prefix + "reason"

// tmux accepts the write on a dead pane without storing anything, so callers check PaneDead first.
func (h *Host) SetNeedsYou(ctx context.Context, session, reason string) error {
	return h.setPane(ctx, session, "needs_you", reason)
}

func (h *Host) SetWorking(ctx context.Context, session string) error {
	return h.setPane(ctx, session, "working", "")
}

// The reason goes first, in the same command list, so a poll never sees needs_you without it.
func (h *Host) setPane(ctx context.Context, session, state, reason string) error {
	return h.setPaneAt(ctx, h.shipPane(session), state, reason)
}

// SetPaneWorking names the pane by its id, so a crew boat's write never lands on the ship's pane.
func (h *Host) SetPaneWorking(ctx context.Context, paneID string) error {
	return h.setPaneAt(ctx, paneID, "working", "")
}

// SetPaneNeedsYou names the pane by its id, like SetPaneWorking.
func (h *Host) SetPaneNeedsYou(ctx context.Context, paneID, reason string) error {
	return h.setPaneAt(ctx, paneID, "needs_you", reason)
}

func (h *Host) setPaneAt(ctx context.Context, pane, state, reason string) error {
	target := tmuxQuote(pane)
	setReason := "set-option -p -u -t " + target + " " + ReasonOption
	if reason != "" {
		setReason = "set-option -p -t " + target + " " + ReasonOption + " " + tmuxQuote(reason)
	}
	_, err := h.commandList(ctx, setReason, "set-option -p -t "+target+" "+StateOption+" "+state)
	return err
}

// "=session:" would be the current window's active pane, which can be a crew boat's.
func (h *Host) shipPane(session string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id := h.sessions[session].PaneID; id != "" {
		return id
	}
	return "=" + session + ":^"
}

// An empty value unsets the option. Unlike a pane option, a session option is stored while
// the pane is dead.
func (h *Host) SetOptions(ctx context.Context, session string, opts map[string]string) error {
	names := make([]string, 0, len(opts))
	for n := range opts {
		names = append(names, n)
	}
	sort.Strings(names)
	target := tmuxQuote("=" + session + ":")
	cmds := make([]string, 0, len(names))
	for _, n := range names {
		if opts[n] == "" {
			cmds = append(cmds, "set-option -u -t "+target+" "+Prefix+n)
		} else {
			cmds = append(cmds, "set-option -t "+target+" "+Prefix+n+" "+tmuxQuote(opts[n]))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	_, err := h.commandList(ctx, cmds...)
	return err
}

func IsNoSession(err error) bool {
	return err != nil && strings.Contains(err.Error(), "can't find session")
}

func (h *Host) KillSession(ctx context.Context, session string) error {
	_, err := h.command(ctx, "kill-session -t "+tmuxQuote("="+session+":"))
	return err
}

// ErrPaneDead is what EndHarness answers for a pane whose process had already ended.
var ErrPaneDead = errors.New("the pane's process had already ended")

// EndHarness notes why, then signals the pane's process group and every descendant it can see (TERM, then KILL after grace, or as soon
// as the leader is gone) and keeps the pane. remain-on-exit leaves the dead pane, so the ship reads anchored with the signal.
func (h *Host) EndHarness(ctx context.Context, session, reason string, grace time.Duration) error {
	pane := h.shipPane(session)
	pid, dead, err := h.panePID(ctx, pane)
	if err != nil {
		return err
	}
	if dead {
		return ErrPaneDead
	}
	if pid <= 1 {
		return fmt.Errorf("pane %s has pid %d", pane, pid)
	}
	if err := h.SetOptions(ctx, session, map[string]string{OptStoppedBy: reason}); err != nil {
		return err
	}
	// a child in its own group still has the leader as an ancestor, so the tree is read before TERM
	tree := descendants(pid)
	signal := func(sig syscall.Signal) (first error) {
		for _, target := range append([]int{-pid}, tree...) {
			if err := syscall.Kill(target, sig); err != nil && err != syscall.ESRCH && first == nil {
				first = err
			}
		}
		return first
	}
	if err := signal(syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		if _, dead, err := h.panePID(ctx, pane); err != nil || dead {
			if err != nil {
				return err
			}
			break
		}
	}
	return signal(syscall.SIGKILL)
}

// descendants is the pids below root, read from the process table.
func descendants(root int) []int {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}
	children := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		p, e1 := strconv.Atoi(f[0])
		pp, e2 := strconv.Atoi(f[1])
		if e1 == nil && e2 == nil {
			children[pp] = append(children[pp], p)
		}
	}
	var tree []int
	for queue := []int{root}; len(queue) > 0; queue = queue[1:] {
		for _, c := range children[queue[0]] {
			tree = append(tree, c)
			queue = append(queue, c)
		}
	}
	return tree
}

func (h *Host) panePID(ctx context.Context, pane string) (pid int, dead bool, err error) {
	// display-message prints nothing useful over the control connection, so read the list and pick the pane
	lines, err := h.command(ctx, "list-panes -a -F '#{pane_id}|#{pane_pid}|#{pane_dead}'")
	if err != nil {
		return 0, false, err
	}
	for _, line := range lines {
		f := strings.Split(line, "|")
		if len(f) != 3 || f[0] != pane {
			continue
		}
		if f[2] == "1" { // a dead pane has no pid
			return 0, true, nil
		}
		if pid, err = strconv.Atoi(f[1]); err != nil {
			return 0, false, err
		}
		return pid, false, nil
	}
	return 0, false, fmt.Errorf("no pane %s", pane)
}
