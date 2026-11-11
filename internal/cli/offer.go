package cli

import (
	"bufio"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// isTerminal is false for /dev/null and pipes, which launchd jobs and tests have.
func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), getTermios)
	return err == nil
}

// OfferSetup does nothing without a terminal (launchd), so Admiral just starts.
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
