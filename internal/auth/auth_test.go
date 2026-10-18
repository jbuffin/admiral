package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
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
	upgrade := map[string]string{"Upgrade": "websocket", "Connection": "Upgrade"}
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

	if r := do("GET", "/?token=secret-token", lan, cookie); r.Code != 302 || r.Header().Get("Location") != "/" {
		t.Errorf("cookie plus token: %d %q, want 302 to /", r.Code, r.Header().Get("Location"))
	}
	if r := do("GET", "/", lan, cookie); r.Code != 200 {
		t.Errorf("cookie alone: %d", r.Code)
	}
	if r := do("GET", "/ws?token=secret-token", lan, upgrade); r.Code != 200 {
		t.Errorf("ws with ?token=: %d (must connect, not redirect)", r.Code)
	}
	if r := do("GET", "/ws", lan, upgrade); r.Code != 401 {
		t.Errorf("ws without token: %d", r.Code)
	}
	if r := do("GET", "/ws", lan, map[string]string{"Upgrade": "WebSocket", "Host": "vpn.example:7420", "Cookie": cookie["Cookie"]}); r.Code != 200 {
		t.Errorf("non-local, unknown Host, valid token: %d", r.Code)
	}
	// A request line like "GET //evil.example/?token=… " must not redirect off-site.
	req := httptest.NewRequest("GET", "/x?token=secret-token", nil)
	req.URL.Path = "//evil.example/"
	req.RemoteAddr = lan
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); rec.Code != 302 || loc != "/evil.example/" {
		t.Errorf("open redirect: %d %q, want 302 to /evil.example/", rec.Code, loc)
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

// Rotating the token ends every non-local WebSocket that got in with the old
// one, and leaves a loopback one alone (story 8). The peer address is forged
// because the e2e version needs the LAN address to answer.
func TestSetTokenClosesNonLocalWebSockets(t *testing.T) {
	a := New("old-token", "7420")
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			if _, _, err := c.Read(r.Context()); err != nil {
				return
			}
		}
	})
	guarded := a.Middleware(echo)
	lan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "192.168.1.50:51000"
		guarded.ServeHTTP(w, r)
	}))
	defer lan.Close()
	local := httptest.NewServer(guarded)
	defer local.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func(srv *httptest.Server, query string) *websocket.Conn {
		t.Helper()
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws"+query, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return c
	}
	far, near := dial(lan, "?token=old-token"), dial(local, "")

	a.SetToken("new-token")

	readCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	if _, _, err := far.Read(readCtx); err == nil || readCtx.Err() != nil {
		t.Fatalf("the non-local socket of the old token should be closed, got %v", err)
	}
	nearCtx, stopNear := context.WithTimeout(ctx, 300*time.Millisecond)
	defer stopNear()
	if _, _, err := near.Read(nearCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a loopback socket has no token to lose, but it ended: %v", err)
	}
	if !a.valid("new-token") || a.valid("old-token") {
		t.Fatal("SetToken should swap the accepted token")
	}
}
