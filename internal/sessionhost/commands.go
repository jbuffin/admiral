package sessionhost

import (
	"fmt"
	"strings"
)

// The commands the bridge sends on its control-mode client (Client.Do), one
// string each. Each is a single tmux command line; control mode reads the line
// with tmux's own parser, so an argument that might carry a quote, a backslash
// or a $ goes through quoteArg.

// modeFormat is what ModeCmd reads: the cursor, the modes and the sizes.
const modeFormat = "#{cursor_x}|#{cursor_y}|#{cursor_flag}|#{keypad_cursor_flag}|#{alternate_on}|#{bracket_paste_flag}|#{mouse_any_flag}|#{window_width}|#{window_height}|#{history_size}"

func quoteArg(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`).Replace(s) + `"`
}

// ResizeWindowCmd sizes one window alone: a whole-client size would be followed
// by every window of the session.
func ResizeWindowCmd(window string, cols, rows int) string {
	return fmt.Sprintf("refresh-client -C %s:%dx%d", window, cols, rows)
}

// FirstPaneCmd prints the pane id and the window id of the session's first window.
func FirstPaneCmd(session string) string {
	return "display-message -p -t " + quoteArg("="+session+":^") + " '#{pane_id} #{window_id}'"
}

// PaneSizeCmd prints the size of the pane's window as COLSxROWS.
func PaneSizeCmd(pane string) string {
	return "display-message -p -t " + pane + " '#{window_width}x#{window_height}'"
}

// ModeCmd prints the cursor, the modes and the sizes of the pane, "|"-separated:
// cursor x and y, cursor visible, application cursor, alternate screen, bracketed
// paste, mouse, window width and height, history size.
func ModeCmd(pane string) string {
	return "display-message -p -t " + pane + " '" + modeFormat + "'"
}

// ScreenCmd prints the pane's visible screen with its colours.
func ScreenCmd(pane string) string { return "capture-pane -p -e -t " + pane }

// ScrollbackCmd prints the lines lines of history above the pane's screen.
func ScrollbackCmd(pane string, lines int) string {
	return fmt.Sprintf("capture-pane -p -e -S -%d -E -1 -t %s", lines, pane)
}

// SendBytesCmds types raw into the pane as hex key codes, chunk bytes to a command.
func SendBytesCmds(pane string, raw []byte, chunk int) []string {
	var cmds []string
	for len(raw) > 0 {
		n := min(chunk, len(raw))
		var sb strings.Builder
		sb.WriteString("send-keys -H -t " + pane)
		for _, c := range raw[:n] {
			fmt.Fprintf(&sb, " %02x", c)
		}
		cmds = append(cmds, sb.String())
		raw = raw[n:]
	}
	return cmds
}
