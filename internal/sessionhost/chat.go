package sessionhost

import (
	"path/filepath"
	"strings"

	"github.com/jbuffin/admiral/internal/launcher"
)

// The pane options the chat session keeps (ADR 0019). The host's shared format
// doesn't carry them: the chat service reads them from tmux itself.
const (
	// TranscriptOption is written by `admiral hook SessionStart` on the chat's
	// pane: "<unix nanoseconds> <source> <transcript path>".
	TranscriptOption = Prefix + "transcript"
	// The chat's model, effort and fleet, set on the session when it starts (the
	// launcher names them), so a restarted Admiral can tell whether New chat can
	// clear the session or must restart it.
	ChatModelOption  = Prefix + launcher.ChatModelOpt
	ChatEffortOption = Prefix + launcher.ChatEffortOpt
	ChatFleetOption  = Prefix + launcher.ChatFleetOpt
)

// ValidTranscriptPath is whether p is a path the chat may record or read:
// absolute, ending in .jsonl, at most 4096 bytes, with no control character. The
// hook records only such a path and the reader requires it again, since the
// option can be written by anything in the chat session.
func ValidTranscriptPath(p string) bool {
	return filepath.IsAbs(p) && strings.HasSuffix(p, ".jsonl") && len(p) <= 4096 &&
		strings.IndexFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}
