package transcriptfs

import (
	"regexp"
	"strings"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

var startedWith = regexp.MustCompile(`--session-id["']?\s+["']?([0-9a-f-]{36})`)

// Bind reads a ship's @admiral-transcript value against the command its pane started with and answers the path and session id they agree on.
func Bind(raw, startCmd string) (path, id string, ok bool) { return BindFor("claude", raw, startCmd) }

// BindFor is Bind for a harness's path layout; Claude and Copilot launches carry a --session-id.
func BindFor(harness, raw, startCmd string) (path, id string, ok bool) {
	// "<unix ns> <source> <path>"; a ship's is recorded by its launch.
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) != 3 || parts[1] != "launch" || !sessionhost.ValidTranscriptPath(parts[2]) {
		return "", "", false
	}
	id, valid := SessionIDFor(harness, parts[2])
	// The option is writable from the pane and the start command is not, so the id the launch gave the harness decides.
	if m := startedWith.FindStringSubmatch(startCmd); !valid || m == nil || m[1] != id {
		return "", "", false
	}
	return parts[2], id, true
}
