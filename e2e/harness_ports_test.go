package e2e

import (
	"net"
	"net/http"
	"testing"
)

// holdPort is a stranger on addr that answers 404 to everything, the way another suite's server would.
func holdPort(t *testing.T, addr string) {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NotFoundHandler()}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
}

// firstDraws makes the harness's next draws return these ports, as if another process took each one between the
// draw and Admiral's bind; later draws are real.
func firstDraws(t *testing.T, ports ...string) {
	t.Helper()
	real := drawPort
	left := ports
	drawPort = func(t *testing.T) string {
		if len(left) > 0 {
			p := left[0]
			left = left[1:]
			return p
		}
		return real(t)
	}
	t.Cleanup(func() { drawPort = real })
}

// The harness may not hand a test an Admiral that does not own its ports. Each case takes the port first, the
// way a parallel suite does, and the Admiral that comes back must still be the test's own.
func TestHarnessAdmiralOwnsItsPortsWhenAStrangerTookThemFirst(t *testing.T) {
	for _, c := range []struct {
		name   string
		listen func(port string) string
		hold   string // the stranger's address on the listen port
		lan    bool   // the stranger holds the LAN port instead
	}{
		{"same address", func(p string) string { return "127.0.0.1:" + p }, "127.0.0.1:", false},
		{"loopback while Admiral binds the wildcard", func(p string) string { return "0.0.0.0:" + p }, "127.0.0.1:", false},
		{"the LAN port", func(p string) string { return "0.0.0.0:" + p }, "127.0.0.1:", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			listenPort, lanPort := freePortUnreserved(t), freePortUnreserved(t)
			taken := listenPort
			if c.lan {
				taken = lanPort
			}
			holdPort(t, c.hold+taken)
			firstDraws(t, listenPort, lanPort)
			home := newHome(t)
			in := startWithRetry(t, c.listen(freePort(t)), func(listen string) (*instance, bool) {
				return startAdmiralOnce(t, home, listen)
			})
			resp, err := http.Get(in.LocalURL + "/api/ships")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s/api/ships = %d, want 200: a stranger answers where Admiral should", in.LocalURL, resp.StatusCode)
			}
		})
	}
}

// freePortUnreserved is a real free port the harness has not recorded.
func freePortUnreserved(t *testing.T) string {
	t.Helper()
	return drawPort(t)
}

// The tailscale rig starts Admiral on its own port, outside startWithRetry; it too must end up with an Admiral that
// owns the port, and its serve config must follow the port it moved to.
func TestHarnessTailscaleRigMovesOffAPortAStrangerTook(t *testing.T) {
	taken := freePortUnreserved(t)
	holdPort(t, "127.0.0.1:"+taken)
	firstDraws(t, taken)
	r := newTSRig(t)
	if r.port != taken {
		t.Fatalf("rig drew %s, want the held %s", r.port, taken)
	}
	p := r.start(false, false)
	if p.Port == taken {
		t.Fatalf("rig kept the held port %s", taken)
	}
	resp, err := http.Get(p.LocalURL + "/api/ships")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || r.port != p.Port {
		t.Fatalf("status %d, rig port %s, Admiral port %s", resp.StatusCode, r.port, p.Port)
	}
}
