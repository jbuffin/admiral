package sessionhost

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

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

// ValidTranscriptPath is whether p is a path the chat may record or read, or a
// crew boat may record (ADR 0021; see ValidCrewTranscriptPath): absolute,
// ending in .jsonl, at most 4096 bytes, with no control character. The hook
// records only such a path and the reader requires it again, since the option
// can be written by anything in the chat session.
func ValidTranscriptPath(p string) bool {
	return filepath.IsAbs(p) && strings.HasSuffix(p, ".jsonl") && len(p) <= 4096 &&
		strings.IndexFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

// ValidCrewTranscriptPath is ValidTranscriptPath plus the stricter check for a
// crew boat's path (ADR 0021): valid UTF-8; none of ' " \ ` $ # ; { }, so
// `admiral hook` can quote the path inside a nested tmux command; and no C1
// control, bidi formatting or line/paragraph separator character, which would
// hide what the path says where a driver shows it. The hook records only such a path and the reader
// requires it again, on one rule.
func ValidCrewTranscriptPath(p string) bool {
	return ValidTranscriptPath(p) && utf8.ValidString(p) && !strings.ContainsAny(p, "'\"\\`$#;{}") &&
		strings.IndexFunc(p, func(r rune) bool {
			return unicode.IsControl(r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0x2028 || r == 0x2029
		}) < 0
}
