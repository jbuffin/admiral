package cli

import (
	"fmt"
	"path/filepath"

	"github.com/jbuffin/admiral/internal/auth"
	"github.com/jbuffin/admiral/internal/server"
)

// DefaultPort is the port the server listens on unless told otherwise; the
// commands that talk to a running server look there.
const DefaultPort = "7420"

// Token is `admiral token rotate [--port <n>]` (story 8). A running server
// watches the token file, so it signs every non-local device out at once.
func Token(args []string, std IO) error {
	if len(args) == 0 || args[0] != "rotate" {
		return usagef("usage: admiral token rotate [--port <n>]")
	}
	fs := newFlags("token rotate")
	port := fs.String("port", DefaultPort, "the port Admiral listens on, for the URL")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	token, err := auth.RotateToken(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(std.Out, "New token saved in %s.\n", filepath.Join(dir, "token"))
	if ip, ok := server.LANIPv4(); ok {
		fmt.Fprintf(std.Out, "On other devices: http://%s:%s/?token=%s\n", ip, *port, token)
	} else {
		fmt.Fprintf(std.Out, "On other devices: no network address found; this Mac is reachable only at http://127.0.0.1:%s/\n", *port)
	}
	if serves(*port, dir) {
		fmt.Fprintln(std.Out, "Every device is signed out; open the URL above on each one.")
	} else {
		fmt.Fprintf(std.Out, "No running server for this home on port %s; the new token applies when it starts.\n", *port)
	}
	return nil
}

// serves says whether the server on the port is this home's.
func serves(port, dir string) bool {
	_, ok := probe(port, dir)
	return ok
}
