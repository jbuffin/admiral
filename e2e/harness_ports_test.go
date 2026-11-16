package e2e

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
// draw and Admiral's bind; later draws are real. It returns how many of them are still undrawn. It swaps a package
// global, so a test that uses it must not be t.Parallel.
func firstDraws(t *testing.T, ports ...string) (undrawn func() int) {
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
	return func() int { return len(left) }
}

// freePortUnreserved is a real free port the harness has not recorded.
func freePortUnreserved(t *testing.T) string {
	t.Helper()
	return drawPort(t)
}

// An Admiral whose port the harness does not need before the start takes it from the kernel (host:0), which never
// hands out a port another process holds, so no stranger can be on it. Ports held on loopback sit first in line for
// a draw; a start that asks the harness for one gets a stranger, so every one must be left undrawn, and each
// Admiral must answer as itself on its own port.
func TestHarnessStartsDrawNoPortForAnAdmiralThatTakesItsOwn(t *testing.T) {
	held := []string{freePortUnreserved(t), freePortUnreserved(t), freePortUnreserved(t), freePortUnreserved(t)}
	for _, p := range held {
		holdPort(t, "127.0.0.1:"+p)
	}
	undrawn := firstDraws(t, held...)
	answers := func(in *instance) {
		t.Helper()
		resp, err := http.Get(in.LocalURL + "/api/ships")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s/api/ships = %d, want 200: a stranger answers where Admiral should", in.LocalURL, resp.StatusCode)
		}
		for _, p := range held {
			if in.Port == p {
				t.Fatalf("Admiral is on the held port %s", p)
			}
		}
	}
	t.Run("loopback", func(t *testing.T) { answers(startAdmiral(t, newHome(t), "127.0.0.1:0")) })
	t.Run("LAN", func(t *testing.T) { answers(startLAN(t)) })
	t.Run("world", func(t *testing.T) { answers(newWorld(t).start().instance) })
	t.Run("world LAN", func(t *testing.T) { answers(newWorld(t).startLAN(t).instance) })
	if n := undrawn(); n != len(held) {
		t.Fatalf("the starts drew %d of the %d held ports; an Admiral that takes its own port needs none", len(held)-n, len(held))
	}
}

// A port that is known before the start (a restart keeps its port, the tailscale rig's serve config names it) can be
// taken first. loopbackOwner must call another server a stranger and a broken answer unclear.
func TestHarnessLoopbackOwnerTellsAStrangerFromTheAdmiral(t *testing.T) {
	in := startAdmiral(t, newHome(t), "127.0.0.1:0")
	if owned, unclear := loopbackOwner(in); !owned || unclear != "" {
		t.Fatalf("the Admiral itself: owned %v, unclear %q", owned, unclear)
	}
	at := func(h http.HandlerFunc) *instance {
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		return &instance{Home: in.Home, LocalURL: srv.URL}
	}
	for _, c := range []struct {
		name     string
		h        http.HandlerFunc
		stranger bool
	}{
		{"404", http.NotFound, true},
		{"another home", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]string{"home": "0000"})
		}, true},
		{"not JSON", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) }, true},
		{"500", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) }, false},
	} {
		owned, unclear := loopbackOwner(at(c.h))
		if owned || (unclear == "") != c.stranger {
			t.Errorf("%s: owned %v, unclear %q; want not owned and a stranger=%v", c.name, owned, unclear, c.stranger)
		}
	}
	dead := &instance{Home: in.Home, LocalURL: "http://127.0.0.1:" + freePort(t)}
	if owned, unclear := loopbackOwner(dead); owned || unclear == "" {
		t.Errorf("nothing listening: owned %v, unclear %q; want unclear", owned, unclear)
	}
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
	if got := r.serveProxy(); got != "http://127.0.0.1:"+r.port {
		t.Fatalf("serve.json proxies to %q, want http://127.0.0.1:%s", got, r.port)
	}
}

// serveProxy is the proxy target of the root handler in the rig's serve config.
func (r *tsRig) serveProxy() string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, "serve.json"))
	if err != nil {
		r.t.Fatal(err)
	}
	var cfg struct {
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		r.t.Fatal(err)
	}
	return cfg.Web[tsHostPort(443)].Handlers["/"].Proxy
}
