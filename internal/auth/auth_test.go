package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The e2e tests reach non-local peers only through the machine's LAN address,
// which some machines (and the engineer's Mac) don't answer. This runs the same
// rules with a forged peer address.
func TestNonLocalPeer(t *testing.T) {
	a := New("secret-token", "7420")
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	do := func(method, target, remote string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req.RemoteAddr = remote
		for k, v := range hdr {
			if k == "Host" {
				req.Host = v
			} else {
				req.Header.Set(k, v)
			}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	const lan = "192.168.1.50:51000"
	cookie := map[string]string{"Cookie": "admiral_token_7420=secret-token"}

	if r := do("GET", "/", lan, nil); r.Code != 401 || !strings.Contains(r.Body.String(), "Open the URL Admiral printed") {
		t.Errorf("no token: %d %q", r.Code, r.Body)
	}
	if r := do("GET", "/?token=wrong", lan, nil); r.Code != 401 {
		t.Errorf("wrong token: %d", r.Code)
	}
	if r := do("GET", "/api/x", lan, nil); r.Code != 401 || strings.TrimSpace(r.Body.String()) != `{"error":"unauthorized"}` {
		t.Errorf("api without token: %d %q", r.Code, r.Body)
	}

	r := do("GET", "/?foo=1&token=secret-token", lan, nil)
	if r.Code != 302 || r.Header().Get("Location") != "/?foo=1" {
		t.Errorf("token redirect: %d %q", r.Code, r.Header().Get("Location"))
	}
	c := r.Result().Cookies()
	if len(c) != 1 || c[0].Name != "admiral_token_7420" || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode || c[0].MaxAge != 400*24*3600 {
		t.Errorf("cookie: %+v", c)
	}

	if r := do("GET", "/", lan, cookie); r.Code != 200 {
		t.Errorf("cookie alone: %d", r.Code)
	}
	if r := do("GET", "/ws?token=secret-token", lan, nil); r.Code != 200 {
		t.Errorf("ws with ?token=: %d (must connect, not redirect)", r.Code)
	}
	if r := do("GET", "/ws", lan, nil); r.Code != 401 {
		t.Errorf("ws without token: %d", r.Code)
	}
	if r := do("GET", "/ws", lan, map[string]string{"Host": "vpn.example:7420", "Cookie": cookie["Cookie"]}); r.Code != 200 {
		t.Errorf("non-local, unknown Host, valid token: %d", r.Code)
	}
	if r := do("POST", "/api/x", lan, map[string]string{"Origin": "http://evil.example", "Cookie": cookie["Cookie"]}); r.Code != 403 {
		t.Errorf("foreign Origin: %d", r.Code)
	}
}

func TestLoopbackPeer(t *testing.T) {
	h := New("secret-token", "7420").Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	do := func(host string) int {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "127.0.0.1:51000"
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, host := range []string{"localhost:7420", "127.0.0.1:7420", "[::1]:7420"} {
		if c := do(host); c != 200 {
			t.Errorf("Host %s from loopback: %d, want 200", host, c)
		}
	}
	if c := do("evil.example:7420"); c != 403 {
		t.Errorf("Host evil.example from loopback: %d, want 403", c)
	}
}
