package sessionhost

import "context"

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
	_, err := h.commandList(ctx, setReason+" ; set-option -p -t "+target+" "+StateOption+" "+state, 2)
	return err
}

// KillSession ends the ship's session, whatever its pane is doing.
func (h *Host) KillSession(ctx context.Context, session string) error {
	_, err := h.command(ctx, "kill-session -t "+tmuxQuote("="+session+":"))
	return err
}
