package tailscale

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jbuffin/admiral/internal/config"
)

// Route serves GET /api/remote: the mode, the allowed logins and, with the mode on, the live checks.
type Route struct {
	Config func() *config.Config
	Port   string
}

// Routes registers unversioned; the server strips /v1 after the guard.
func (rt *Route) Routes(mux *http.ServeMux) { mux.HandleFunc("GET /api/remote", rt.serve) }

func (rt *Route) serve(w http.ResponseWriter, r *http.Request) {
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
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(view)
}

// Base is the pairing base in the mode: the ts.net URL without its trailing slash, or "" when there is none.
func Base(ctx context.Context, port string) string {
	u := Inspect(ctx, port).URL
	if n := len(u); n > 0 {
		return u[:n-1]
	}
	return ""
}
