// Package server is Admiral's HTTP and WebSocket surface: the embedded UI at
// /, JSON under /api/, one WebSocket at /ws. Access control is in
// internal/auth and wraps the handler returned here.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/config"
)

func init() {
	// Go's built-in table has no woff2.
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

// MessageHandler handles one WebSocket message type. Later units register
// theirs in Server.Messages.
type MessageHandler func(ctx context.Context, c *Client, raw json.RawMessage)

// Module is a part of the server that serves its own routes and sends its own
// state to each client that connects.
type Module interface {
	Routes(mux *http.ServeMux)
	// Connected runs for a new client after it has registered with the hub.
	Connected(c *Client)
}

// Server holds what the handlers share.
type Server struct {
	Version  string
	Commit   string
	UI       fs.FS
	Messages map[string]MessageHandler
	Config   *config.Manager
	Hub      *Hub
	Modules  []Module
}

// Handler returns the routes, without access control.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.serveWS)
	mux.HandleFunc("GET /api/config", s.serveConfig)
	for _, m := range s.Modules {
		m.Routes(mux)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}` + "\n"))
	})
	mux.HandleFunc("/", s.serveUI)
	return mux
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

	c := s.Hub.add(ctx, conn)
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

// sendConfig pushes the config unless the client already has exactly this.
func (s *Server) sendConfig(c *Client) {
	if s.Config != nil {
		_ = c.SendOnce("config", func() any { return s.Config.View() })
	}
}

// WatchConfig pushes every config change to all clients until ctx ends.
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

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(s.UI, name)
	if err != nil && path.Ext(name) == "" {
		name = "index.html" // client-side route
		data, err = fs.ReadFile(s.UI, name)
	}
	if err != nil {
		if name == "index.html" {
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
