package lockroute_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/lockroute"
	"github.com/jbuffin/admiral/internal/resources"
)

// A lock is taken only by a request that is local under auth's rule, so a request proxied onto loopback is refused.
func TestLockRefusesProxiedLoopback(t *testing.T) {
	mux := http.NewServeMux()
	(&lockroute.Route{Set: resources.New(resources.Options{Config: func() *config.Config { return &config.Config{} }})}).Routes(mux)

	do := func(host string, hdr map[string]string) int {
		req := httptest.NewRequest("POST", "/api/resources/simulator/lock", nil)
		req.RemoteAddr = "127.0.0.1:51000"
		req.Host = host
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	// Local reaches the resource lookup, which has no such resource.
	if c := do("127.0.0.1:7420", nil); c != http.StatusNotFound {
		t.Fatalf("a local request: %d, want 404 from the resource lookup", c)
	}
	for name, tc := range map[string]struct {
		host string
		hdr  map[string]string
	}{
		"X-Forwarded-For":      {"127.0.0.1:7420", map[string]string{"X-Forwarded-For": "100.64.0.9"}},
		"Tailscale-User-Login": {"127.0.0.1:7420", map[string]string{"Tailscale-User-Login": "fake@example.com"}},
		"tailscale serve Host": {"mac.tailnet.ts.net", nil},
	} {
		if c := do(tc.host, tc.hdr); c != http.StatusForbidden {
			t.Errorf("%s from a loopback peer: %d, want 403", name, c)
		}
	}
}
