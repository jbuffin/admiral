package sessionhost

// The pane options the chat session keeps (ADR 0019). The host's shared format
// doesn't carry them: the chat service reads them from tmux itself.
const (
	// TranscriptOption is written by `admiral hook SessionStart` on the chat's
	// pane: "<unix nanoseconds> <source> <transcript path>".
	TranscriptOption = Prefix + "transcript"
	// ChatModelOption, ChatEffortOption and ChatFleetOption are set on the chat
	// session when it starts, so a restarted Admiral can tell whether New chat can
	// clear the session or must restart it.
	ChatModelOption  = Prefix + "chat-model"
	ChatEffortOption = Prefix + "chat-effort"
	ChatFleetOption  = Prefix + "chat-fleet"
)
