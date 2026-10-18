package cli

import (
	"bufio"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// isTerminal is true for a terminal, not for /dev/null or a pipe (both of which
// a launchd job and the tests have).
func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TIOCGETA)
	return err == nil
}

// OfferSetup is story 12: starting Admiral on a terminal with no fleets offers
// to run setup first. Without a terminal (launchd) it does nothing, and Admiral
// just starts. It reports whether setup ran.
func OfferSetup(std IO) (bool, error) {
	in, ok := std.In.(*os.File)
	if !ok || !isTerminal(in) {
		return false, nil
	}
	yes, err := confirm(bufio.NewReader(in), std.Out, "No fleets are set up yet. Run admiral setup first?")
	if err != nil || !yes {
		return false, err
	}
	if err := Setup(nil, std); err != nil {
		fmt.Fprintln(std.Err, "admiral:", err)
	}
	return true, nil
}
