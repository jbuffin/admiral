package sessionhost

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ChatModelOpt  = "chat-model"
	ChatEffortOpt = "chat-effort"
	ChatFleetOpt  = "chat-fleet"
	// ChatHarnessOpt is the harness the session was launched under: its pane is what a reset or flush would be typed into.
	ChatHarnessOpt = "chat-harness"
	// ChatNonceOpt is minted when a chat session starts and ends with it; it is not a secret, it only
	// makes the session's actor token differ from every earlier session's.
	ChatNonceOpt = "chat-nonce"
	// ChatPromptOpt is a pane option the chat's UserPromptSubmit hook sets to the unix nanoseconds of each
	// prompt, so a message typed in the terminal moves it too.
	ChatPromptOpt = "chat-prompt"
	// ChatDigestOpt is the highest digest sequence the chat's UserPromptSubmit hook has printed.
	ChatDigestOpt = "chat-digest"
)

// The host's shared format doesn't carry these: the chat service reads them from tmux itself.
const (
	// TranscriptOption is "<unix nanoseconds> <source> <transcript path>".
	TranscriptOption = Prefix + "transcript"
	// Set at start so a restarted Admiral can tell whether New chat can clear the
	// session or must restart it.
	ChatModelOption   = Prefix + ChatModelOpt
	ChatEffortOption  = Prefix + ChatEffortOpt
	ChatFleetOption   = Prefix + ChatFleetOpt
	ChatHarnessOption = Prefix + ChatHarnessOpt
)

// The reader checks again what the hook checked, since anything in the session can write the option.
func ValidTranscriptPath(p string) bool {
	return filepath.IsAbs(p) && strings.HasSuffix(p, ".jsonl") && len(p) <= 4096 &&
		strings.IndexFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

// ValidCrewTranscriptPath refuses quote and shell characters so `admiral hook` can quote the path
// inside a nested tmux command, and bidi or separator characters that would hide what it says.
func ValidCrewTranscriptPath(p string) bool {
	return ValidTranscriptPath(p) && utf8.ValidString(p) && !strings.ContainsAny(p, "'\"\\`$#;{}") &&
		strings.IndexFunc(p, func(r rune) bool {
			return unicode.IsControl(r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0x2028 || r == 0x2029
		}) < 0
}
