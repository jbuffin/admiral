// Package auth decides who may reach Admiral (ADR 0001). Peers on loopback are
// local and need nothing. Everyone else presents the token, as a cookie or,
// for the WebSocket, as ?token=. Requests that could let a web page on
// another site drive the Mac are refused first.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const cookieMaxAge = 400 * 24 * 60 * 60

// LoadOrCreateToken returns the token in <home>/token, creating it (mode 0600,
// 256 random bits, URL-safe) when missing, so it survives restarts.
func LoadOrCreateToken(home string) (string, error) {
	path := filepath.Join(home, "token")
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

// Auth guards one listener.
type Auth struct {
	token  string
	cookie string
}

// New returns the guard for a server on port. The port is in the cookie name
// so a test instance on the same host never overwrites the real one's cookie.
func New(token, port string) *Auth {
	return &Auth{token: token, cookie: "admiral_token_" + port}
}

func (a *Auth) valid(candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(a.token)) == 1
}

// Middleware applies the checks in order: Host (loopback peers), Origin
// (WebSocket and non-GET), then the token (non-local peers).
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isWS := r.URL.Path == "/ws"
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

		if c, err := r.Cookie(a.cookie); err == nil && a.valid(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		query := r.URL.Query()
		if t := query.Get("token"); t != "" && a.valid(t) {
			if isWS {
				next.ServeHTTP(w, r)
				return
			}
			if r.Method == http.MethodGet {
				a.redirectWithCookie(w, r, query)
				return
			}
		}
		unauthorized(w, r)
	})
}

func (a *Auth) redirectWithCookie(w http.ResponseWriter, r *http.Request, query url.Values) {
	http.SetCookie(w, &http.Cookie{
		Name: a.cookie, Value: a.token, Path: "/", MaxAge: cookieMaxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	query.Del("token")
	target := *r.URL
	target.RawQuery = query.Encode()
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
