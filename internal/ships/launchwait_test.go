package ships

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// hostRig is a service whose host runs real tmux through a symlink the test can remove.
type hostRig struct {
	*mooredFixture
	tmux, link, sock string
	ctx              context.Context
}

// parkedCtx closes parked when its Done is first read, which is where a launch waits for the host.
type parkedCtx struct {
	context.Context
	parked chan struct{}
	once   sync.Once
}

func newParkedCtx(parent context.Context) *parkedCtx {
	return &parkedCtx{Context: parent, parked: make(chan struct{})}
}

func (c *parkedCtx) Done() <-chan struct{} {
	c.once.Do(func() { close(c.parked) })
	return c.Context.Done()
}

func newHostRig(t *testing.T) *hostRig {
	t.Helper()
	if testing.Short() {
		t.Skip("needs tmux: skipped under -short")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	dir, err := os.MkdirTemp("/tmp", "sh")
	if err != nil {
		t.Fatal(err)
	}
	r := &hostRig{tmux: tmux, link: dir + "/tmux", sock: dir + "/tmux.sock"}
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", r.sock, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	if err := os.Symlink(tmux, r.link); err != nil {
		t.Fatal(err)
	}
	r.mooredFixture = mooredService(t, nil)
	r.s.Host = sessionhost.New(r.link, r.sock)
	r.s.Orders = sliceOrders{}
	r.s.Launcher.Tmux = r.link
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	r.ctx = ctx
	return r
}

func (r *hostRig) startHost() { go r.s.Host.Run(r.ctx) }

// sliceLaunch is a launch that carries issues, so it needs the host's list of ships, and that the moored Slice ship refuses.
func (r *hostRig) sliceLaunch(t *testing.T) launcher.Request {
	t.Helper()
	if err := moor.Write(r.home, moor.Record{Name: "Slice", Session: "slice", Fleet: "work", URL: sliceB, Issues: []string{sliceB}}); err != nil {
		t.Fatal(err)
	}
	return launcher.Request{Source: "campaign", OrderID: "c/slice", Issues: []string{"https://github.com/acme/widgets/issues/30", sliceB}}
}

func TestALaunchBeforeTheHostsFirstReadWaitsForItInsteadOfAnswering503(t *testing.T) {
	t.Parallel()
	r := newHostRig(t)
	req := r.sliceLaunch(t)
	pc := newParkedCtx(context.Background())
	done := make(chan *launcher.Error, 1)
	go func() {
		_, lerr := r.s.Launch(pc, req)
		done <- lerr
	}()
	var lerr *launcher.Error
	select {
	case <-pc.parked:
		r.startHost()
		lerr = <-done
	case lerr = <-done:
	}
	if lerr == nil || lerr.Status != http.StatusConflict || !strings.Contains(lerr.Msg, "(Slice)") {
		t.Fatalf("a launch that began before the host's first read = %v, want it to wait for the read and be refused by the Slice ship's claim (409)", lerr)
	}
}

func TestALaunchWhoseRequestEndsBeforeTheHostsFirstReadReturnsTheContextsError(t *testing.T) {
	t.Parallel()
	r := newHostRig(t) // the host never runs: no first read
	req := r.sliceLaunch(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pc := newParkedCtx(ctx)
	done := make(chan *launcher.Error, 1)
	go func() {
		_, lerr := r.s.Launch(pc, req)
		done <- lerr
	}()
	var lerr *launcher.Error
	select {
	case <-pc.parked:
		cancel()
		lerr = <-done
	case lerr = <-done:
	}
	if lerr == nil || !strings.Contains(lerr.Msg, context.Canceled.Error()) {
		t.Fatalf("a launch whose request ended before the first read = %v, want the context's error (%q)", lerr, context.Canceled)
	}
}

// An empty ship list says nothing about which orders hold a ship, so autopilot's tick could start an order that already runs.
func TestALaunchWhileTheHostsConnectionIsLostAnswers503(t *testing.T) {
	t.Parallel()
	r := newHostRig(t)
	req := r.sliceLaunch(t)
	r.startHost()
	<-r.s.Host.Ready()
	if err := os.Remove(r.link); err != nil { // the next connection cannot start
		t.Fatal(err)
	}
	if out, err := exec.Command(r.tmux, "-S", r.sock, "kill-server").CombinedOutput(); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	for r.s.Host.Known() {
		time.Sleep(5 * time.Millisecond)
	}
	_, lerr := r.s.Launch(context.Background(), req)
	if lerr == nil || lerr.Status != http.StatusServiceUnavailable || lerr.Msg != NotKnownMsg {
		t.Fatalf("a launch while the connection is lost = %v, want a 503 %q", lerr, NotKnownMsg)
	}
}

// The cap counts ships the host lists, which is nothing until its first read, so a launch must wait for the read before it is admitted.
func TestAnAutopilotLaunchBeforeTheHostsFirstReadIsRefusedByTheCapOnceItLands(t *testing.T) {
	t.Parallel()
	r := newWakeRig(t, 1)
	r.mustLaunch(t, nil) // the one ship the cap allows
	s2 := r.restarted()
	s2.Host = sessionhost.New(r.s.Launcher.Tmux, r.s.Launcher.Socket)
	hostCtx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	pc := newParkedCtx(context.Background())
	done := make(chan *launcher.Error, 1)
	go func() {
		_, lerr := s2.Launch(pc, r.keyReq(func(q *launcher.Request) { q.Autopilot, q.Source, q.OrderID = true, "s", "o2" }))
		done <- lerr
	}()
	var lerr *launcher.Error
	select {
	case <-pc.parked:
		go s2.Host.Run(hostCtx)
		lerr = <-done
	case lerr = <-done:
	}
	if lerr == nil || lerr.Status != http.StatusConflict || !strings.Contains(lerr.Msg, launcher.AutopilotCapText) {
		t.Fatalf("an autopilot launch made before the first read, with the cap reached = %v, want a 409 %q", lerr, launcher.AutopilotCapText)
	}
}
