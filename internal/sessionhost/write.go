package sessionhost

import (
	"context"
	"sort"
	"strings"
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
