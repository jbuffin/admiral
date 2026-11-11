// Package shipconv serves a Claude Code ship's conversation over HTTP and WebSocket (docs/api.md).
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
	pageSize  = 200
	MaxOpen   = 16 // per client; opening one more closes the oldest
	pollEvery = 250 * time.Millisecond
	findEvery = time.Second // how often to search the other project dirs for a moved file
	maxBefore = 256
)

// The states of the endpoint.
const (
	StateOK         = "ok"
	StateNone       = "none"
	StateUnreadable = "unreadable"
	StateGone       = "gone"
)

type Service struct {
	Ships  *ships.Service
	Config func() *config.Config
	Tmux   string
	Socket string

	// Tests replace these two lookups, which need a live tmux.
	ship   func(session string) (ships.Ship, bool)
	option func(sh ships.Ship) (raw, configDir string, ok bool)

	mu      sync.Mutex
	seen    map[string]string // session -> option value a file was seen for: tells gone from none
	clients map[*server.Client][]string
	watches map[string]*watch
	running bool
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ships/{session}/conversation", s.serveConversation)
}

func (s *Service) Connected(*server.Client) {}

func (s *Service) Messages() map[string]server.MessageHandler {
	return map[string]server.MessageHandler{
		"conversation_open":  s.handleOpen,
		"conversation_close": s.handleClose,
	}
}

type where struct {
	ship      ships.Ship
	raw       string // the option's value
	path      string
	id        string // the session id
	configDir string
}

func (s *Service) shipOf(session string, sync bool) (ships.Ship, bool) {
	if s.ship != nil {
		return s.ship(session)
	}
	sh, ok := s.Ships.Get(session)
	if !ok && sync {
		// A ship just launched may not be listed yet.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.Ships.Host.Sync(ctx)
		cancel()
		sh, ok = s.Ships.Get(session)
	}
	return sh, ok
}

// optionOf reads the ship's @admiral-transcript and the Claude config dir of its fleet.
func (s *Service) optionOf(sh ships.Ship) (raw, configDir string, ok bool) {
	if s.option != nil {
		return s.option(sh)
	}
	var pane string
	for _, sess := range s.Ships.Host.Sessions() {
		if sess.Name == sh.Session {
			pane = sess.PaneID
		}
	}
	if pane == "" {
		return "", "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := sessionhost.NewServer(s.Tmux, s.Socket).ReadPaneOption(ctx, pane, "transcript")
	if err != nil {
		return "", "", false
	}
	f, found := s.Config().Fleet(sh.Fleet)
	if !found {
		return "", "", false
	}
	var env map[string]string
	if d, has := f.Harness("claude"); has {
		env = d.Env
	}
	return raw, config.IdentityDir("claude", env), true
}

// locate finds the ship and its transcript. ok is false for an unknown ship;
// none is true when the ship has no conversation of ours to read.
func (s *Service) locate(session string) (w where, ok, none bool) {
	sh, ok := s.shipOf(session, true)
	if !ok {
		return where{}, false, false
	}
	w.ship = sh
	if sh.Harness != "claude" {
		return w, true, true
	}
	raw, configDir, has := s.optionOf(sh)
	if !has {
		return w, true, true
	}
	// "<unix ns> <source> <path>"; a ship's is recorded by its launch.
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) != 3 || parts[1] != "launch" || !sessionhost.ValidTranscriptPath(parts[2]) {
		return w, true, true
	}
	id, valid := transcriptfs.SessionID(parts[2])
	if !valid {
		return w, true, true
	}
	w.raw, w.path, w.id, w.configDir = raw, parts[2], id, configDir
	return w, true, false
}

// open tries the recorded path, then (find) any project dir holding the session id.
// A missing file is fs.ErrNotExist; a refused one is transcriptfs.ErrRefused.
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

type watch struct {
	session  string
	launched string // tells a reused ship name from the ship that was watched
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
	// Prime the reader: what is already there is not sent (the client reads the
	// page), and a tool result that arrives later still finds its call.
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

func (s *Service) poll(w *watch) {
	if sh, ok := s.shipOf(w.session, false); !ok || sh.Launched != w.launched {
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
