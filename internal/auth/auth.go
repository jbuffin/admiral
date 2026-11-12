// Package auth decides who may reach Admiral (ADRs 0031 and 0032): a local request
// needs nothing, everyone else presents the token of a paired device.
package auth

import (
	"context"
	"mime"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
)

const cookieMaxAge = 400 * 24 * 60 * 60

type pathKind int

const (
	pathNone    pathKind = iota // a peer and headers that fit neither route in
	pathLocal                   // this Mac, no proxy in between
	pathLAN                     // a LAN peer connecting directly
	pathTailnet                 // tailscale serve: a loopback peer carrying the user's login
)

type route struct {
	kind  pathKind
	login string
}

type ctxKey struct{}

func classify(r *http.Request) route {
	loopback := peerIsLoopback(r)
	proxied := hasProxyHeaders(r)
	switch {
	case loopback && !proxied && hostAllowed(r.Host):
		return route{kind: pathLocal}
	case loopback:
		// Decoded and trimmed here only, so the allowlist, the recorded login and the device binding agree.
		if login := strings.TrimSpace(decodeLogin(r.Header.Get("Tailscale-User-Login"))); login != "" {
			return route{kind: pathTailnet, login: login}
		}
	case !proxied:
		return route{kind: pathLAN}
	}
	return route{kind: pathNone}
}

// decodeLogin undoes the RFC 2047 encoding tailscale serve gives a login that isn't plain ASCII.
func decodeLogin(v string) string {
	if d, err := new(mime.WordDecoder).DecodeHeader(v); err == nil {
		return d
	}
	return v
}

// route is classify plus the remote settings, read on every request so an edit applies at once:
// a tailnet request needs the mode on and its login on the allowlist.
func (a *Auth) route(r *http.Request) route {
	rt := classify(r)
	if rt.kind != pathTailnet {
		return rt
	}
	on, logins := a.remote()
	if on && slices.Contains(logins, rt.login) {
		return rt
	}
	return route{kind: pathNone}
}

// EnforceRemote ends every open tailnet socket the remote settings no longer allow: all of
// them with the mode off, else those whose login left the allowlist. Call it when the config changes.
func (a *Auth) EnforceRemote() {
	on, logins := a.remote()
	a.mu.Lock()
	defer a.mu.Unlock()
	for s := range a.socks {
		if s.login != "" && !(on && slices.Contains(logins, s.login)) {
			s.cancel()
		}
	}
}

// IsLocal reports whether the request comes from this Mac itself: a loopback peer,
// no proxy headers, and a Host that is one of this machine's own names.
func IsLocal(r *http.Request) bool { return classify(r).kind == pathLocal }

func hasProxyHeaders(r *http.Request) bool {
	for k := range r.Header {
		if k == "X-Forwarded-For" || k == "Forwarded" || strings.HasPrefix(k, "Tailscale-User-") {
			return true
		}
	}
	return false
}

func peerIsLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isWS := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		rt := a.route(r)
		// https too: behind a TLS proxy the page's origin is https.
		badOrigin := func() bool {
			origin := r.Header.Get("Origin")
			return origin != "" && (isWS || r.Method != http.MethodGet) &&
				!strings.EqualFold(origin, "http://"+r.Host) && !strings.EqualFold(origin, "https://"+r.Host)
		}

		if r.URL.Path == "/pair" || (r.URL.Path == "/api/v1/pair" && r.Method == http.MethodPost) {
			if badOrigin() {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
			a.servePair(w, r, rt)
			return
		}
		if rt.kind == pathLocal {
			if badOrigin() {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if rt.kind == pathNone {
			unauthorized(w, r)
			return
		}

		if token, ok := bearerToken(r); ok {
			id, ok := a.authenticate(token, rt)
			if !ok {
				unauthorized(w, r)
				return
			}
			a.serve(w, r, next, isWS, id, rt.login)
			return
		}
		if c, err := r.Cookie(a.cookie); err == nil {
			if id, ok := a.authenticate(c.Value, rt); ok {
				if badOrigin() {
					http.Error(w, "forbidden origin", http.StatusForbidden)
					return
				}
				if a.shouldReissue(id) {
					a.setCookie(w, r, c.Value)
				}
				a.serve(w, r, next, isWS, id, rt.login)
				return
			}
		}
		unauthorized(w, r)
	})
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:]), true
	}
	return "", false
}

func (a *Auth) serve(w http.ResponseWriter, r *http.Request, next http.Handler, isWS bool, id, login string) {
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, id))
	if isWS {
		a.track(w, r, next, id, login)
		return
	}
	next.ServeHTTP(w, r)
}

// The bridge's sockets end too: their handlers read with the request's context,
// which is cancelled when the device is revoked.
func (a *Auth) track(w http.ResponseWriter, r *http.Request, next http.Handler, id, login string) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	a.mu.Lock()
	if a.find(id) == nil { // revoked since it was checked
		a.mu.Unlock()
		unauthorized(w, r)
		return
	}
	s := &socket{device: id, login: login, cancel: cancel}
	a.socks[s] = struct{}{}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.socks, s)
		a.mu.Unlock()
	}()
	next.ServeHTTP(w, r.WithContext(ctx))
}

func (a *Auth) setCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: a.cookie, Value: token, Path: "/", MaxAge: cookieMaxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
}

const unauthorizedPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Admiral</title></head>
<body style="font-family: system-ui, sans-serif; margin: 3rem 1.5rem"><p>This device isn't paired with Admiral.</p>
<p>On the Mac, run <code>admiral pair</code> and open the address it prints, or <a href="/pair">enter the code</a>.</p></body></html>
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

// hostAllowed guards loopback peers against DNS rebinding.
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
