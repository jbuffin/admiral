// Package auth decides who may reach Admiral (ADR 0001). Peers on loopback are
// local and need nothing. Everyone else presents the token, as a cookie or,
// for the WebSocket, as ?token=. Requests that could let a web page on
// another site drive the Mac are refused first.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const cookieMaxAge = 400 * 24 * 60 * 60

// LoadOrCreateToken returns the token in <home>/token, creating it (mode 0600,
// 256 random bits, URL-safe) when missing, so it survives restarts.
func LoadOrCreateToken(home string) (string, error) {
	path := filepath.Join(home, "token")
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, os.Chmod(path, 0o600)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, os.Chmod(path, 0o600) // WriteFile keeps the mode of a file that already existed
}

func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// RotateToken replaces the token in <home>/token with a new one (mode 0600)
// and returns it. The file is replaced by a rename, so a reader sees the old
// token or the new one. A running server notices (Auth.Watch).
func RotateToken(home string) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(home, ".token.*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return token, os.Rename(tmp.Name(), filepath.Join(home, "token"))
}

// Auth guards one listener.
type Auth struct {
	cookie string

	mu    sync.Mutex
	token string
	socks map[*socket]struct{}
}

// socket is a non-local WebSocket request and the token it got in with.
type socket struct {
	token  string
	cancel context.CancelFunc
}

// New returns the guard for a server on port. The port is in the cookie name
// so a test instance on the same host never overwrites the real one's cookie.
func New(token, port string) *Auth {
	return &Auth{token: token, cookie: "admiral_token_" + port, socks: map[*socket]struct{}{}}
}

func (a *Auth) current() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.token
}

func (a *Auth) valid(candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(a.current())) == 1
}

// SetToken makes token the one that is accepted and ends every non-local
// WebSocket that got in with another (the dashboard's and the bridge's alike:
// their handlers read with the request's context, which is cancelled here).
func (a *Auth) SetToken(token string) {
	a.mu.Lock()
	a.token = token
	var stale []*socket
	for s := range a.socks {
		if s.token != token {
			stale = append(stale, s)
		}
	}
	a.mu.Unlock()
	for _, s := range stale {
		s.cancel()
	}
}

// Watch follows <home>/token until ctx ends, so `admiral token rotate` takes
// effect without a restart (story 8). It watches by polling: the file is a few
// bytes, and a rename over it is seen the same as a write.
func (a *Auth) Watch(ctx context.Context, home string) {
	path := filepath.Join(home, "token")
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if b, err := os.ReadFile(path); err == nil {
				if t := strings.TrimSpace(string(b)); t != "" && t != a.current() {
					a.SetToken(t)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// track runs next for a non-local WebSocket, which SetToken can end.
func (a *Auth) track(w http.ResponseWriter, r *http.Request, next http.Handler) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	a.mu.Lock()
	s := &socket{token: a.token, cancel: cancel}
	a.socks[s] = struct{}{}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.socks, s)
		a.mu.Unlock()
	}()
	next.ServeHTTP(w, r.WithContext(ctx))
}

// Middleware applies the checks in order: Host (loopback peers), Origin
// (WebSocket and non-GET), then the token (non-local peers).
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isWS := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		local := isLoopbackPeer(r)

		if local && !hostAllowed(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && (isWS || r.Method != http.MethodGet) &&
			!strings.EqualFold(origin, "http://"+r.Host) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if local {
			next.ServeHTTP(w, r)
			return
		}

		query := r.URL.Query()
		tokenOK := a.valid(query.Get("token"))
		// A GET carrying the token always trades it for the cookie, so the
		// token never stays in the address bar or history.
		if tokenOK && !isWS && r.Method == http.MethodGet {
			a.redirectWithCookie(w, r, query)
			return
		}
		if c, err := r.Cookie(a.cookie); err == nil && a.valid(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if tokenOK && isWS {
			next.ServeHTTP(w, r)
			return
		}
		unauthorized(w, r)
	})
}

func (a *Auth) serve(w http.ResponseWriter, r *http.Request, next http.Handler, isWS bool) {
	if isWS {
		a.track(w, r, next)
		return
	}
	next.ServeHTTP(w, r)
}

func (a *Auth) redirectWithCookie(w http.ResponseWriter, r *http.Request, query url.Values) {
	http.SetCookie(w, &http.Cookie{
		Name: a.cookie, Value: a.token, Path: "/", MaxAge: cookieMaxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	query.Del("token")
	// One leading slash only: "//host/..." would redirect off-site.
	target := url.URL{Path: "/" + strings.TrimLeft(r.URL.Path, "/"), RawQuery: query.Encode()}
	http.Redirect(w, r, target.String(), http.StatusFound)
}

const unauthorizedPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Admiral</title></head>
<body style="font-family: system-ui, sans-serif; margin: 3rem 1.5rem"><p>Open the URL Admiral printed</p></body></html>
`

func unauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}` + "\n"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(unauthorizedPage))
}

func isLoopbackPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// hostAllowed reports whether a Host header names this Mac. It guards loopback
// peers against DNS rebinding.
func hostAllowed(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		addrs, _ := net.InterfaceAddrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
				return true
			}
		}
		return false
	}
	if name, err := os.Hostname(); err == nil {
		name = strings.ToLower(name)
		return host == name || host == strings.TrimSuffix(name, ".local")+".local"
	}
	return false
}
