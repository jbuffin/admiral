// Package shipconv serves a Claude Code ship's conversation (#264, ADR 0030):
// GET /api/ships/<ship>/conversation, and the `conversation` WebSocket message
// to the browsers that opened the ship. The file is found from the pane option
// @admiral-transcript, which the launcher sets before the harness starts; the
// conversation itself is read by internal/conversation. See docs/api.md.
package shipconv

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/transcriptfs"
)

const (
	// pageSize is how many items a page holds.
	pageSize = 200
	// MaxOpen is how many ships one client holds open; opening one more closes
	// the oldest.
	MaxOpen = 16
	// pollEvery is how often the open ships' files are looked at for growth.
	pollEvery = 250 * time.Millisecond
	// findEvery is how often a file that is not where it was computed is looked
	// for under the other project directories.
	findEvery = time.Second
	// maxBefore bounds a ?before= id.
	maxBefore = 256
)

// The states of the endpoint.
const (
	StateOK         = "ok"
	StateNone       = "none"
	StateUnreadable = "unreadable"
	StateGone       = "gone"
)

// Service serves the conversations of the ships Ships knows.
type Service struct {
	Ships  *ships.Service
	Config func() *config.Config
	Tmux   string
	Socket string

	mu      sync.Mutex
	seen    map[string]string // session -> the option value a file was seen for, to tell gone from none
	clients map[*server.Client][]string
	watches map[string]*watch
	running bool
}

// Routes registers the endpoint.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ships/{session}/conversation", s.serveConversation)
}

// Connected sends a new client nothing: a client asks for a ship's conversation.
func (s *Service) Connected(*server.Client) {}

// Messages are the WebSocket messages a client sends, for Server.Messages.
func (s *Service) Messages() map[string]server.MessageHandler {
	return map[string]server.MessageHandler{
		"conversation_open":  s.handleOpen,
		"conversation_close": s.handleClose,
	}
}

// where is a ship's transcript as far as the pane option and the config say.
type where struct {
	ship      ships.Ship
	raw       string // the option's value
	path      string // the computed path
	id        string // the session id
	configDir string
}

// locate finds the ship and where its transcript is. ok is false for a ship that
// is not there; none is true when the ship has no conversation Admiral can read.
func (s *Service) locate(session string) (w where, ok, none bool) {
	sh, ok := s.Ships.Get(session)
	if !ok {
		// A ship just launched may not be listed yet.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.Ships.Host.Sync(ctx)
		cancel()
		if sh, ok = s.Ships.Get(session); !ok {
			return where{}, false, false
		}
	}
	w.ship = sh
	if sh.Harness != "claude" {
		return w, true, true
	}
	var pane string
	for _, sess := range s.Ships.Host.Sessions() {
		if sess.Name == session {
			pane = sess.PaneID
		}
	}
	if pane == "" {
		return w, true, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := sessionhost.NewServer(s.Tmux, s.Socket).ReadPaneOption(ctx, pane, "transcript")
	if err != nil {
		return w, true, true
	}
	// "<unix ns> <source> <path>"; ships record "launch".
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) != 3 || parts[1] != "launch" || !sessionhost.ValidTranscriptPath(parts[2]) {
		return w, true, true
	}
	id, valid := transcriptfs.SessionID(parts[2])
	if !valid {
		return w, true, true
	}
	cfg := s.Config()
	f, found := cfg.Fleet(sh.Fleet)
	if !found {
		return w, true, true
	}
	var env map[string]string
	if d, has := f.Harness("claude"); has {
		env = d.Env
	}
	w.raw, w.path, w.id = raw, parts[2], id
	w.configDir = config.IdentityDir("claude", env)
	return w, true, false
}

// open opens the ship's transcript: where it was computed, else wherever its
// session id is under the config dir's projects/. A file that is not there is an
// fs.ErrNotExist error; one the ADR 0019 checks refuse is transcriptfs.ErrRefused.
func (w where) open(find bool) (*os.File, os.FileInfo, string, error) {
	f, info, err := transcriptfs.Open(w.configDir, w.path)
	if err == nil {
		return f, info, w.path, nil
	}
	if !errors.Is(err, fs.ErrNotExist) || !find {
		return nil, nil, "", err
	}
	p, err := transcriptfs.Find(w.configDir, w.id)
	if err != nil {
		return nil, nil, "", err
	}
	f, info, err = transcriptfs.Open(w.configDir, p)
	return f, info, p, err
}

// Page is the endpoint's answer.
type Page struct {
	State string              `json:"state"`
	Items []conversation.Item `json:"items"`
	More  bool                `json:"more"`
}

func (s *Service) serveConversation(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	loc, ok, none := s.locate(session)
	if !ok {
		s.forget(session)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	before := ""
	if v, has := r.URL.Query()["before"]; has {
		before = v[0]
		if before == "" || len(before) > maxBefore || !utf8.ValidString(before) || strings.ContainsAny(before, "\x00\n\r") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "before must be the id of an item"})
			return
		}
	}
	page := Page{State: StateNone, Items: []conversation.Item{}}
	if none {
		writeJSON(w, http.StatusOK, page)
		return
	}
	f, info, _, err := loc.open(true)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		s.mu.Lock()
		was := s.seen[session] == loc.raw
		s.mu.Unlock()
		if was {
			page.State = StateGone
		}
		writeJSON(w, http.StatusOK, page)
		return
	case err != nil:
		page.State = StateUnreadable
		writeJSON(w, http.StatusOK, page)
		return
	}
	defer f.Close()
	s.markSeen(session, loc.raw)
	items, more, err := conversation.Page(f, info, conversation.Options{}, before, pageSize)
	switch {
	case errors.Is(err, conversation.ErrUnknownID):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "before is not the id of an item"})
		return
	case err != nil:
		page.State = StateUnreadable
		writeJSON(w, http.StatusOK, page)
		return
	}
	page.State, page.More = StateOK, more
	if items != nil {
		page.Items = items
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Service) markSeen(session, raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]string{}
	}
	s.seen[session] = raw
}

func (s *Service) forget(session string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.seen, session)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- the WebSocket ----

// watch follows one ship's file for the clients that opened it.
type watch struct {
	session  string
	launched string // the ship's launch time, to tell a reused name
	loc      where
	reader   *conversation.Reader
	path     string // where the file was found
	lastFind time.Time
	subs     map[*server.Client]struct{}
}

func (s *Service) handleOpen(ctx context.Context, c *server.Client, raw json.RawMessage) {
	var m struct {
		Ship string `json:"ship"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Ship == "" {
		return
	}
	s.mu.Lock()
	for _, have := range s.clients[c] {
		if have == m.Ship {
			s.mu.Unlock()
			return
		}
	}
	s.mu.Unlock()

	loc, ok, none := s.locate(m.Ship)
	if !ok || none {
		return // an unknown ship, or one with no conversation, is ignored
	}
	w := &watch{session: m.Ship, launched: loc.ship.Launched, loc: loc, reader: conversation.NewReader(conversation.Options{}),
		subs: map[*server.Client]struct{}{}}
	// Prime the reader, so that what is already there is not sent and a tool
	// result that arrives later still finds its call. The client reads the page
	// itself.
	if f, info, p, err := loc.open(true); err == nil {
		_, _ = w.reader.Refresh(f, info)
		f.Close()
		w.path = p
		s.markSeen(m.Ship, loc.raw)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clients == nil {
		s.clients, s.watches = map[*server.Client][]string{}, map[string]*watch{}
	}
	for _, have := range s.clients[c] {
		if have == m.Ship {
			return
		}
	}
	first := len(s.clients[c]) == 0
	if cur, has := s.watches[m.Ship]; has && cur.launched == w.launched {
		w = cur
	} else {
		s.watches[m.Ship] = w
	}
	w.subs[c] = struct{}{}
	s.clients[c] = append(s.clients[c], m.Ship)
	for len(s.clients[c]) > MaxOpen {
		s.unsubscribeLocked(c, s.clients[c][0])
	}
	if first {
		go func() { <-ctx.Done(); s.dropClient(c) }()
	}
	if !s.running {
		s.running = true
		go s.run()
	}
}

func (s *Service) handleClose(_ context.Context, c *server.Client, raw json.RawMessage) {
	var m struct {
		Ship string `json:"ship"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unsubscribeLocked(c, m.Ship)
}

// unsubscribeLocked ends c's interest in a ship, and the watch when nobody has any.
func (s *Service) unsubscribeLocked(c *server.Client, session string) {
	list := s.clients[c]
	for i, have := range list {
		if have == session {
			list = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(s.clients, c)
	} else {
		s.clients[c] = list
	}
	if w := s.watches[session]; w != nil {
		delete(w.subs, c)
		if len(w.subs) == 0 {
			delete(s.watches, session)
		}
	}
}

func (s *Service) dropClient(c *server.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range append([]string(nil), s.clients[c]...) {
		s.unsubscribeLocked(c, session)
	}
}

// run polls the open ships' files until none is open.
func (s *Service) run() {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for range t.C {
		s.mu.Lock()
		if len(s.watches) == 0 {
			s.running = false
			s.mu.Unlock()
			return
		}
		ws := make([]*watch, 0, len(s.watches))
		for _, w := range s.watches {
			ws = append(ws, w)
		}
		s.mu.Unlock()
		for _, w := range ws {
			s.poll(w)
		}
	}
}

// poll sends what the ship's file gained to the clients that opened it. A ship
// that is gone, or a name reused by a newer ship, ends the watch.
func (s *Service) poll(w *watch) {
	if sh, ok := s.Ships.Get(w.session); !ok || sh.Launched != w.launched {
		s.endWatch(w)
		return
	}
	var (
		f    *os.File
		info os.FileInfo
		err  error
	)
	if w.path != "" {
		f, info, err = transcriptfs.Open(w.loc.configDir, w.path)
	}
	if f == nil {
		find := time.Since(w.lastFind) >= findEvery
		if find {
			w.lastFind = time.Now()
		}
		var p string
		if f, info, p, err = w.loc.open(find); err == nil {
			w.path = p
		}
	}
	if err != nil || f == nil {
		return
	}
	defer f.Close()
	s.markSeen(w.session, w.loc.raw)
	up, _ := w.reader.Refresh(f, info)
	if len(up.Items) == 0 {
		return
	}
	s.mu.Lock()
	subs := make([]*server.Client, 0, len(w.subs))
	for c := range w.subs {
		subs = append(subs, c)
	}
	s.mu.Unlock()
	msg := map[string]any{"type": "conversation", "ship": w.session, "items": up.Items}
	for _, c := range subs {
		_ = c.Send(msg)
	}
}

func (s *Service) endWatch(w *watch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watches[w.session] != w {
		return
	}
	for c := range w.subs {
		s.unsubscribeLocked(c, w.session)
	}
	delete(s.watches, w.session)
}
