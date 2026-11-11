package transcriptfs

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

var startedWith = regexp.MustCompile(`--session-id["']?\s+["']?([0-9a-f-]{36})`)

// Bind reads a ship's @admiral-transcript value against the command its pane started with and answers the path and session id they agree on.
func Bind(raw, startCmd string) (path, id string, ok bool) {
	// "<unix ns> <source> <path>"; a ship's is recorded by its launch.
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) != 3 || parts[1] != "launch" || !sessionhost.ValidTranscriptPath(parts[2]) {
		return "", "", false
	}
	id, valid := SessionID(parts[2])
	// The option is writable from the pane and the start command is not, so the id the launch gave the harness decides.
	if m := startedWith.FindStringSubmatch(startCmd); !valid || m == nil || m[1] != id {
		return "", "", false
	}
	return parts[2], id, true
}

// LaunchTime is the launch time at the front of a ship's @admiral-transcript value; zero when it has none.
func LaunchTime(raw string) time.Time {
	first, _, _ := strings.Cut(raw, " ")
	ns, err := strconv.ParseInt(first, 10, 64)
	if err != nil || ns <= 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}
