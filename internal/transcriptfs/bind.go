package transcriptfs

// Bind reads a ship's @admiral-transcript value against the command its pane started with and answers the path and session id they agree on.
func Bind(raw, startCmd string) (path, id string, ok bool) { return "", "", false }
