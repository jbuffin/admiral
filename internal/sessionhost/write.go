package sessionhost

import (
	"context"
	"sort"
	"strings"
)

// StateOption is the pane option the hooks write: needs_you or working.
const StateOption = Prefix + "state"

// ReasonOption is the pane option that says why a ship needs you: a short code,
// set with needs_you and unset with working.
const ReasonOption = Prefix + "reason"

// SetNeedsYou writes @admiral-state needs_you on the ship's pane with its
// reason, a short code. tmux accepts the write on a dead pane without storing
// anything, so callers check PaneDead first.
func (h *Host) SetNeedsYou(ctx context.Context, session, reason string) error {
	return h.setPane(ctx, session, "needs_you", reason)
}

// SetWorking writes @admiral-state working on the ship's pane and unsets its
// reason, so a stale reason never outlives its stop.
func (h *Host) SetWorking(ctx context.Context, session string) error {
	return h.setPane(ctx, session, "working", "")
}

// setPane writes the reason (an unset when empty) and then the state in one
// command list, like `admiral hook`: the reason goes first so a poll never sees
// needs_you without it, and tmux runs a client's list without another client's
// commands between.
func (h *Host) setPane(ctx context.Context, session, state, reason string) error {
	target := tmuxQuote("=" + session + ":")
	setReason := "set-option -p -u -t " + target + " " + ReasonOption
	if reason != "" {
		setReason = "set-option -p -t " + target + " " + ReasonOption + " " + tmuxQuote(reason)
	}
	_, err := h.commandList(ctx, setReason, "set-option -p -t "+target+" "+StateOption+" "+state)
	return err
}

// SetOptions writes session options of the ship in one command list. Names are
// without the prefix; an empty value unsets the option. Unlike a pane option, a
// session option is stored while the pane is dead.
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

// IsNoSession is whether err is tmux saying the session is not there.
func IsNoSession(err error) bool {
	return err != nil && strings.Contains(err.Error(), "can't find session")
}

// KillSession ends the ship's session, whatever its pane is doing.
func (h *Host) KillSession(ctx context.Context, session string) error {
	_, err := h.command(ctx, "kill-session -t "+tmuxQuote("="+session+":"))
	return err
}
