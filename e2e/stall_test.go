package e2e

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"syscall"
	"testing"
	"time"
)

type tracked struct {
	t  *testing.T
	in *instance
}

// running maps each live instance's loopback host:port to it, so a request that runs out of time can say where the
// instance was stuck: CI keeps nothing else of a server that stalled.
var running sync.Map

func track(t *testing.T, in *instance) {
	host := "127.0.0.1:" + in.Port
	running.Store(host, tracked{t, in})
	t.Cleanup(func() { running.CompareAndDelete(host, tracked{t, in}) })
}

// stallDump makes a request that ran out of time log the goroutines and stderr of the instance it was sent to.
type stallDump struct{ base http.RoundTripper }

func (s stallDump) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := s.base.RoundTrip(req)
	if err != nil && errors.Is(req.Context().Err(), context.DeadlineExceeded) {
		dumpStuck(req.URL.Host, req.Method+" "+req.URL.Path)
	}
	return resp, err
}

func dumpStuck(host, call string) {
	v, ok := running.LoadAndDelete(host)
	if !ok {
		return
	}
	tr := v.(tracked)
	// Go answers SIGQUIT by printing every goroutine's stack to stderr and exiting.
	_ = tr.in.cmd.Process.Signal(syscall.SIGQUIT)
	select {
	case <-tr.in.done:
	case <-time.After(5 * time.Second):
	}
	tr.t.Logf("%s ran out of time; admiral's stderr and goroutines at that moment:\n%s", call, tr.in.stderrBuf.String())
}
