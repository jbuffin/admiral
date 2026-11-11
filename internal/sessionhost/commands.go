package sessionhost

import (
	"fmt"
	"strings"
)

// Control mode reads each line with tmux's own parser, so an argument that might carry a
// quote, a backslash or a $ goes through quoteArg.

const modeFormat = "#{cursor_x}|#{cursor_y}|#{cursor_flag}|#{keypad_cursor_flag}|#{alternate_on}|#{bracket_paste_flag}|#{mouse_any_flag}|#{window_width}|#{window_height}|#{history_size}"

func quoteArg(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`).Replace(s) + `"`
}

// ResizeWindowCmd sizes one window alone: a whole-client size would be followed
// by every window of the session.
func ResizeWindowCmd(window string, cols, rows int) string {
	return fmt.Sprintf("refresh-client -C %s:%dx%d", window, cols, rows)
}

func FirstPaneCmd(session string) string {
	return "display-message -p -t " + quoteArg("="+session+":^") + " '#{pane_id} #{window_id}'"
}

func PaneSizeCmd(pane string) string {
	return "display-message -p -t " + pane + " '#{window_width}x#{window_height}'"
}

func ModeCmd(pane string) string {
	return "display-message -p -t " + pane + " '" + modeFormat + "'"
}

func ScreenCmd(pane string) string { return "capture-pane -p -e -t " + pane }

func ScrollbackCmd(pane string, lines int) string {
	return fmt.Sprintf("capture-pane -p -e -S -%d -E -1 -t %s", lines, pane)
}

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
