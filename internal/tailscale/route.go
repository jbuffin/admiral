package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jbuffin/admiral/internal/config"
)

// Route serves GET /api/remote (the mode, the allowed logins and, with the mode on, the live checks) and the two
// writes Settings makes: the allowed-logins edit and Set up.
type Route struct {
	Config func() *config.Config
	// Set writes the remote section through the config manager, which reloads before it returns.
	Set  func(config.Remote) error
	Port string
	// Local reports whether a request comes from this Mac itself; only such a request may run Set up.
	Local func(*http.Request) bool

	mu        sync.Mutex // one read-modify-write of the remote section at a time
	setupBusy atomic.Bool
}

// Routes registers unversioned; the server strips /v1 after the guard.
func (rt *Route) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/remote", rt.serve)
	mux.HandleFunc("POST /api/remote/logins", rt.logins)
	mux.HandleFunc("POST /api/remote/setup", rt.setup)
}

func (rt *Route) writeView(w http.ResponseWriter, r *http.Request) {
	remote := rt.Config().Remote
	view := struct {
		Tailscale     bool     `json:"tailscale"`
		AllowedLogins []string `json:"allowedLogins"`
		URL           string   `json:"url"`
		Checks        []Check  `json:"checks"`
	}{remote.Tailscale, remote.AllowedLogins, "", []Check{}}
	if view.AllowedLogins == nil {
		view.AllowedLogins = []string{}
	}
	if remote.Tailscale {
		rep := Inspect(r.Context(), rt.Port)
		view.URL, view.Checks = rep.URL, rep.Checks
	}
	writeJSON(w, http.StatusOK, view)
}

func (rt *Route) serve(w http.ResponseWriter, r *http.Request) { rt.writeView(w, r) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// logins adds or removes one allowed login. The write reloads the config, whose subscriber cuts the sockets of a login that left.
func (rt *Route) logins(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Add    *string `json:"add"`
		Remove *string `json:"remove"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || (body.Add == nil) == (body.Remove == nil) {
		fail(w, http.StatusBadRequest, `send {"add": login} or {"remove": login}`)
		return
	}
	rt.mu.Lock()
	cur := rt.Config().Remote
	next := config.Remote{Tailscale: cur.Tailscale, AllowedLogins: slices.Clone(cur.AllowedLogins)}
	changed := false
	if body.Add != nil {
		login := strings.TrimSpace(*body.Add)
		switch {
		case login == "":
			rt.mu.Unlock()
			fail(w, http.StatusBadRequest, "a login can't be blank")
			return
		case slices.Contains(cur.AllowedLogins, login):
			rt.mu.Unlock()
			fail(w, http.StatusBadRequest, login+" is already allowed")
			return
		}
		next.AllowedLogins = append(next.AllowedLogins, login)
		changed = true
	} else {
		login := strings.TrimSpace(*body.Remove)
		if i := slices.Index(next.AllowedLogins, login); i >= 0 {
			next.AllowedLogins = slices.Delete(next.AllowedLogins, i, i+1)
			changed = true
		}
	}
	if changed {
		if err := rt.Set(next); err != nil {
			rt.mu.Unlock()
			log.Printf("remote: saving the allowed logins: %v", err)
			fail(w, http.StatusInternalServerError, "couldn't save the allowed logins")
			return
		}
	}
	rt.mu.Unlock()
	rt.writeView(w, r)
}

// setup runs the checks, serve when it isn't set up yet, and then writes the mode and the Mac's login.
// A failed step stops it before anything is written.
func (rt *Route) setup(w http.ResponseWriter, r *http.Request) {
	if rt.Local == nil || !rt.Local(r) {
		fail(w, http.StatusForbidden, "Set up runs on this Mac: open Admiral here, or run admiral setup")
		return
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || !body.Confirm {
		fail(w, http.StatusBadRequest, `Set up needs {"confirm": true}`)
		return
	}
	if !rt.setupBusy.CompareAndSwap(false, true) {
		fail(w, http.StatusConflict, "Set up is already running")
		return
	}
	defer rt.setupBusy.Store(false)

	type answer struct {
		OK     bool    `json:"ok"`
		Checks []Check `json:"checks"`
	}
	stop := func(rows []Check) { writeJSON(w, http.StatusOK, answer{false, rows}) }
	pre := Prepare(r.Context(), rt.Port)
	var rows []Check
	for _, c := range pre.Checks {
		rows = append(rows, c)
		if !c.OK {
			stop(rows)
			return
		}
	}
	switch {
	case pre.Serving:
		rows = append(rows, Check{CheckServe, true, "tailscale serve already proxies to Admiral; left as it is"})
	case pre.Holder != "":
		rows = append(rows, Check{"https-443", false, fmt.Sprintf("HTTPS port 443 is already used by %s, and Set up never replaces it. To serve Admiral on another HTTPS port, run: tailscale serve --bg --https=8443 http://127.0.0.1:%s", pre.Holder, rt.Port)})
		stop(rows)
		return
	default:
		if err := EnableServe(r.Context(), rt.Port); err != nil {
			rows = append(rows, Check{CheckServe, false, err.Error()})
			stop(rows)
			return
		}
		rows = append(rows, Check{CheckServe, true, "tailscale serve now proxies HTTPS port 443 to Admiral"})
	}
	rt.mu.Lock()
	err := rt.Set(rt.Config().Remote.Allowing(true, pre.Login))
	rt.mu.Unlock()
	if err != nil {
		log.Printf("remote: saving the mode and the Mac's login: %v", err)
		fail(w, http.StatusInternalServerError, "couldn't save the Tailscale mode")
		return
	}
	writeJSON(w, http.StatusOK, answer{true, rows})
}

// Base is the pairing base in the mode: the ts.net URL without its trailing slash, or "" when there is none.
func Base(ctx context.Context, port string) string {
	u := Inspect(ctx, port).URL
	if n := len(u); n > 0 {
		return u[:n-1]
	}
	return ""
}
