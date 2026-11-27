package e2e

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The suite has one clock, the test binary's -timeout: a wait blocks until its condition holds and a test that
// hangs fails near the end of the run, so a loaded machine is slow but never red.
const (
	// testMargin leaves a hung test time to dump its Admirals and fail before the binary's own timeout panics.
	testMargin = 45 * time.Second
	// watchdogMargin dumps every live Admiral before that panic, for a hang that no context reaches.
	watchdogMargin = 20 * time.Second
	pollEvery      = 50 * time.Millisecond
)

var ctxs sync.Map // *testing.T -> context.Context

// testCtx ends shortly before the binary's deadline, or with the test.
func testCtx(t *testing.T) context.Context {
	if c, ok := ctxs.Load(t); ok {
		return c.(context.Context)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if d, ok := t.Deadline(); ok {
		ctx, cancel = context.WithDeadline(context.Background(), d.Add(-testMargin))
	}
	if c, loaded := ctxs.LoadOrStore(t, ctx); loaded {
		cancel()
		return c.(context.Context)
	}
	t.Cleanup(func() { cancel(); ctxs.Delete(t) })
	return ctx
}

// eventually polls check until it holds. last describes what check saw, for the failure.
func eventually(t *testing.T, what string, check func() (ok bool, last string)) {
	t.Helper()
	ctx := testCtx(t)
	for {
		ok, last := check()
		if ok {
			return
		}
		select {
		case <-ctx.Done():
			msg := what + " never happened before the test run's deadline"
			if last != "" {
				msg += "; last seen: " + last
			}
			hung(t, msg)
		case <-time.After(pollEvery):
		}
	}
}

// until is eventually for a check with nothing worth reporting.
func until(t *testing.T, what string, check func() bool) {
	t.Helper()
	eventually(t, what, func() (bool, string) { return check(), "" })
}

// hung fails t after logging where every live Admiral was stuck.
func hung(t *testing.T, msg string) {
	t.Helper()
	dumpAll(func(s string) { t.Log(s) })
	t.Fatal(msg)
}

type tracked struct{ in *instance }

// running holds each live instance, so a hang can say where the servers were stuck: CI keeps nothing else of them.
var running sync.Map // *instance -> tracked

func track(t *testing.T, in *instance) {
	running.Store(in, tracked{in})
	t.Cleanup(func() { running.Delete(in) })
}

func dumpAll(logf func(string)) {
	var wg sync.WaitGroup
	running.Range(func(k, _ any) bool {
		if _, ok := running.LoadAndDelete(k); !ok {
			return true
		}
		in := k.(*instance)
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Go answers SIGQUIT by printing every goroutine's stack to stderr and exiting.
			_ = in.cmd.Process.Signal(syscall.SIGQUIT)
			select {
			case <-in.done:
			case <-time.After(5 * time.Second):
			}
			logf(fmt.Sprintf("admiral at %s (home %s), stderr and goroutines:\n%s", in.LocalURL, in.Home, in.stderrBuf.String()))
		}()
		return true
	})
	wg.Wait()
}

var afterRun []func()

func TestMain(m *testing.M) {
	flag.Parse()
	if to := flag.Lookup("test.timeout").Value.(flag.Getter).Get().(time.Duration); to > watchdogMargin {
		time.AfterFunc(to-watchdogMargin, func() {
			fmt.Fprintln(os.Stderr, "e2e: the test run is about to time out; dumping every live admiral")
			dumpAll(func(s string) { _, _ = io.WriteString(os.Stderr, s+"\n") })
		})
	}
	code := m.Run()
	for _, f := range afterRun {
		f()
	}
	os.Exit(code)
}

// failRequest fails t on a request error, dumping the Admirals first when the request ran into the deadline.
func failRequest(t *testing.T, call string, err error) {
	t.Helper()
	if testCtx(t).Err() != nil {
		hung(t, fmt.Sprintf("%s did not answer before the test run's deadline: %v", call, err))
	}
	t.Fatalf("%s: %v", call, err)
}
