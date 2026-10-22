package sessionhost

import "context"

// StateOption is the pane option the hooks write: needs_you or working.
const StateOption = Prefix + "state"

// ReasonOption is the pane option that says why a ship needs you: a short code,
// set with needs_you and unset with working.
const ReasonOption = Prefix + "reason"

// SetPaneState writes @admiral-state on the ship's pane, and with it
// @admiral-reason, or unsets the reason when it is empty. needs_you gets its
// reason first and working loses the state first, so a poll never sees
// needs_you without a reason. The writes are two commands, since control mode
// answers each command line holding `;` once per command. tmux accepts the
// write on a dead pane without storing anything, so callers check PaneDead
// first.
func (h *Host) SetPaneState(ctx context.Context, session, value, reason string) error {
	target := tmuxQuote("=" + session + ":")
	setReason := "set-option -p -u -t " + target + " " + ReasonOption
	if reason != "" {
		setReason = "set-option -p -t " + target + " " + ReasonOption + " " + tmuxQuote(reason)
	}
	setState := "set-option -p -t " + target + " " + StateOption + " " + value
	first, second := setReason, setState
	if reason == "" {
		first, second = setState, setReason
	}
	if _, err := h.command(ctx, first); err != nil {
		return err
	}
	_, err := h.command(ctx, second)
	return err
}

// KillSession ends the ship's session, whatever its pane is doing.
func (h *Host) KillSession(ctx context.Context, session string) error {
	_, err := h.command(ctx, "kill-session -t "+tmuxQuote("="+session+":"))
	return err
}
