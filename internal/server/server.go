// Package server is Admiral's HTTP and WebSocket surface. Access control is in
// internal/auth and wraps the handler returned here.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/home"
)

func init() {
	// Go's built-in table has no woff2.
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

type MessageHandler func(ctx context.Context, c *Client, raw json.RawMessage)

type Module interface {
	Routes(mux *http.ServeMux)
	// Connected runs after the client has registered with the hub.
	Connected(c *Client)
}

type Server struct {
	Version  string
	Commit   string
	UI       fs.FS
	Messages map[string]MessageHandler
	Config   *config.Manager
	Hub      *Hub
	// Path names how a request reached Admiral, for the move message; nil leaves every client's path unknown.
	Path    func(*http.Request) string
	Modules []Module
	// Capabilities entries are only ever added within v1; they are not harness
	// capabilities.
	Capabilities []string
}

var APIVersions = []int{1}

// Handler has no access control. The v1 prefix is stripped before routing, so
// every current and future route has a v1 path.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.serveWS)
	mux.HandleFunc("GET /api/config", s.serveConfig)
	mux.HandleFunc("PUT /api/fleets/{key}", s.serveFleetSave)
	mux.HandleFunc("GET /api/version", s.serveVersion)
	mux.HandleFunc("GET /api/logos/{harness}", serveLogo)
	for _, m := range s.Modules {
		m.Routes(mux)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}` + "\n"))
	})
	mux.HandleFunc("/", s.serveUI)
	return stripV1(mux)
}

func stripV1(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, root := range []string{"/api", "/ws"} {
			rest, ok := strings.CutPrefix(r.URL.Path, root+"/v1")
			if !ok || (rest != "" && rest[0] != '/') {
				continue
			}
			r2 := new(http.Request)
			*r2 = *r
			u := *r.URL
			u.Path = root + rest
			// Like http.StripPrefix: an encoded %2F in a path value must stay encoded.
			if u.RawPath != "" {
				if raw, ok := strings.CutPrefix(u.RawPath, root+"/v1"); ok {
					u.RawPath = root + raw
				} else {
					u.RawPath = ""
				}
			}
			r2.URL = &u
			r = r2
			break
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) serveVersion(w http.ResponseWriter, r *http.Request) {
	caps := s.Capabilities
	if caps == nil {
		caps = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": s.Version, "commit": s.Commit, "api_versions": APIVersions, "capabilities": caps,
	})
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has already answered
	}
	defer conn.CloseNow()
	ctx := r.Context()

	hello, _ := json.Marshal(map[string]string{"type": "hello", "version": s.Version, "commit": s.Commit})
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = conn.Write(wctx, websocket.MessageText, hello)
	cancel()
	if err != nil {
		return
	}

	path := ""
	if s.Path != nil {
		path = s.Path(r)
	}
	c := s.Hub.add(ctx, conn, path)
	defer s.Hub.remove(c)
	s.sendConfig(c)
	for _, m := range s.Modules {
		m.Connected(c)
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if h := s.Messages[msg.Type]; h != nil {
			h(ctx, c, data)
		}
	}
}

func (s *Server) sendConfig(c *Client) {
	if s.Config != nil {
		_ = c.SendOnce("config", func() any { return s.Config.View() })
	}
}

func (s *Server) WatchConfig(ctx context.Context) {
	if s.Config == nil {
		return
	}
	changes, unsubscribe := s.Config.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-changes:
			s.Hub.BroadcastOnce("config", func() any { return s.Config.View() })
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) serveConfig(w http.ResponseWriter, r *http.Request) {
	if s.Config == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(s.Config.View())
}

// serveFleetSave takes the fields in the body and ignores every other key, so docks, env and name stay as stored.
func (s *Server) serveFleetSave(w http.ResponseWriter, r *http.Request) {
	if s.Config == nil {
		http.NotFound(w, r)
		return
	}
	var req struct {
		SearchRoots   *[]string `json:"search_roots"`
		Owners        *[]string `json:"owners"`
		WakeReviewers *[]string `json:"wake_reviewers"`
		CloneRoot     *string   `json:"clone_root"`
		Color         *string   `json:"color"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	err := s.Config.SetFleetFields(r.PathValue("key"), config.FleetFields{
		SearchRoots: req.SearchRoots, Owners: req.Owners, WakeReviewers: req.WakeReviewers, CloneRoot: req.CloneRoot, Color: req.Color})
	switch {
	case errors.Is(err, config.ErrNoFleet):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, config.ErrConflict):
		writeJSONError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeJSONError(w, http.StatusBadRequest, err.Error())
	default:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(s.Config.View())
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Only the harness names Admiral knows are looked up, so the path never takes
// anything the request wrote.
func serveLogo(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("harness")
	if !slices.Contains(config.Harnesses(), name) {
		http.NotFound(w, r)
		return
	}
	dir, err := home.Resolve()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, "logos", name+".svg"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(s.UI, name)
	if err != nil && path.Ext(name) == "" {
		name = "index.html" // client-side route
		if r.URL.Path == "/app" || strings.HasPrefix(r.URL.Path, "/app/") {
			name = "app/index.html" // the new shell's own routes
		}
		data, err = fs.ReadFile(s.UI, name)
	}
	if err != nil {
		if path.Base(name) == "index.html" {
			http.Error(w, "UI not built: run make build", http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, time.Time{}, bytesReader(data))
}
