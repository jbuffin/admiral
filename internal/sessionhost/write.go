package sessionhost

import "context"

// StateOption is the pane option the hooks write: needs_you or working.
const StateOption = Prefix + "state"

// SetPaneState writes @admiral-state on the ship's pane. tmux accepts the write
// on a dead pane without storing anything, so callers check PaneDead first.
func (h *Host) SetPaneState(ctx context.Context, session, value string) error {
	_, err := h.command(ctx, "set-option -p -t "+tmuxQuote("="+session+":")+" "+StateOption+" "+value)
	return err
}

// KillSession ends the ship's session, whatever its pane is doing.
func (h *Host) KillSession(ctx context.Context, session string) error {
	_, err := h.command(ctx, "kill-session -t "+tmuxQuote("="+session+":"))
	return err
}
