package cli

import (
	"os"

	"github.com/jbuffin/admiral/internal/codexpane"
)

// CodexPane is the command a Codex ship's pane runs; the launcher writes its arguments, so it is
// not for typing.
func CodexPane(args []string, _ IO) error {
	var socket string
	var command []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			command = args[i+1:]
			break
		}
		if args[i] == "--socket" && i+1 < len(args) {
			socket = args[i+1]
			i++
			continue
		}
		return usagef("%s", Usage("codex-pane"))
	}
	if socket == "" || len(command) == 0 {
		return usagef("%s", Usage("codex-pane"))
	}
	code := codexpane.Run(codexpane.Options{Socket: socket, Exe: command[0], Args: command[1:], Owner: os.Getuid()})
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}
