package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

// Exit codes of `admiral lock` besides 0, 1 and 2.
const (
	lockExitBusy       = 75  // EX_TEMPFAIL: no unit was free and --wait was not given
	lockExitNotStarted = 127 // the command could not be started
)

// ExitError ends the process with Code, after main prints Msg when it is set.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("exit %d", e.Code)
	}
	return e.Msg
}

// Lock is `admiral lock [--port <n>] [--wait] <resource> -- <command…>` (#118):
// it holds one unit of the resource while the command runs, and exits with the
// command's status. The hold is an open request to the running server, so a
// killed `admiral lock` frees the unit.
func Lock(args []string, std IO) error {
	// Everything after the first -- is the command's, untouched.
	var command []string
	for i, a := range args {
		if a == "--" {
			args, command = args[:i], args[i+1:]
			break
		}
	}
	if len(command) == 0 {
		return usagef("%s", Usage("lock"))
	}
	fs := newFlags("lock")
	port := fs.String("port", DefaultPort, "the port Admiral listens on")
	wait := fs.Bool("wait", false, "wait for a unit instead of exiting 75")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return usagef("%s", Usage("lock"))
	}
	// Flags may follow the resource too.
	resource := fs.Arg(0)
	if err := parseFlags(fs, fs.Args()[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("unexpected %q: lock takes one resource, then -- and the command", fs.Arg(0))
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	if !serves(*port, dir) {
		return fmt.Errorf("no running Admiral for this home on port %s", *port)
	}

	q := url.Values{"label": {fmt.Sprintf("admiral lock (pid %d): %s", os.Getpid(), filepath.Base(command[0]))}}
	if *wait {
		q.Set("wait", "1")
	}
	target := "http://" + net.JoinHostPort("127.0.0.1", *port) + "/api/resources/" + url.PathEscape(resource) + "/lock?" + q.Encode()
	// No client timeout: it would cut the hold, which is the response body.
	resp, err := (&http.Client{}).Post(target, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusConflict:
		return &ExitError{lockExitBusy, fmt.Sprintf("%s is busy (--wait waits for a free unit)", resource)}
	case http.StatusNotFound:
		return fmt.Errorf("no resource called %q (it is not in the config)", resource)
	default:
		return fmt.Errorf("the lock was refused: %s", resp.Status)
	}
	var held struct {
		Held bool `json:"held"`
	}
	body := bufio.NewReader(resp.Body)
	line, err := body.ReadBytes('\n')
	if err != nil || json.Unmarshal(line, &held) != nil || !held.Held {
		return fmt.Errorf("the server did not confirm the lock")
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = std.In, std.Out, std.Err
	// From here SIGTERM goes on to the command. The terminal delivers SIGINT, SIGHUP
	// and SIGQUIT to the command itself (the same foreground group), so they are
	// caught only to be ignored: passing them on would deliver them twice.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		return &ExitError{lockExitNotStarted, fmt.Sprintf("%s: %v", command[0], err)}
	}
	done := make(chan struct{})
	// The hold is the response: if it ends while the command runs, the server went
	// away and the unit is free, so say so. The command keeps its own exit status.
	go func() {
		_, _ = io.Copy(io.Discard, body)
		select {
		case <-done:
		default:
			fmt.Fprintf(std.Err, "admiral: lost the lock on %s (the server went away); %s is still running without it\n", resource, filepath.Base(command[0]))
		}
	}()
	go func() {
		for {
			select {
			case s := <-sigs:
				if s == syscall.SIGTERM {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()
	err = cmd.Wait()
	close(done)
	if st, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && st.Signaled() {
		return &ExitError{Code: 128 + int(st.Signal())}
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		return &ExitError{Code: code}
	}
	return err
}
