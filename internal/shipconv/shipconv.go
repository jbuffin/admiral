// Package shipconv serves a Claude Code ship's conversation over HTTP and WebSocket (docs/api.md).
package shipconv

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"regexp"
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
	option func(sh ships.Ship) (raw, startCmd, configDir string, ok bool)
	crew   func(sh ships.Ship, name string) (sessionhost.Crew, string, bool) // the boat and its fleet's Claude config dir

	mu      sync.Mutex
	seen    map[string]string // session -> option value a file was seen for: tells gone from none
	clients map[*server.Client][]string
	outbox  map[*server.Client]chan map[string]any // one sender per client, so a stalled one holds up no other
	watches map[string]*watch
	running bool
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ships/{session}/conversation", s.serveConversation)
	mux.HandleFunc("GET /api/ships/{session}/crew/{name}/conversation", s.serveConversation)
}

func (s *Service) Connected(*server.Client) {}

func (s *Service) Messages() map[string]server.MessageHandler {
	return map[string]server.MessageHandler{
		"conversation_open":  s.handleOpen,
		"conversation_close": s.handleClose,
	}
}

type where struct {
	ship         ships.Ship
	crew         string // the boat's name, empty for the ship
	boatLaunched string
	raw          string // the option's value
	path         string
	id           string // the session id
	configDir    string
}

// key names a ship's conversation, or a crew boat's, in the maps below.
func key(session, crew string) string {
	if crew == "" {
		return session
	}
	return session + "/crew/" + crew
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

// optionOf reads the ship's @admiral-transcript, the command its pane started with and the Claude config dir of its fleet.
func (s *Service) optionOf(sh ships.Ship) (raw, startCmd, configDir string, ok bool) {
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
		return "", "", "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	host := sessionhost.NewServer(s.Tmux, s.Socket)
	raw, err := host.ReadPaneOption(ctx, pane, "transcript")
	if err != nil {
		return "", "", "", false
	}
	startCmd, err = host.ReadPaneStartCommand(ctx, pane)
	if err != nil {
		return "", "", "", false
	}
	dir, found := s.claudeConfigDir(sh)
	if !found {
		return "", "", "", false
	}
	return raw, startCmd, dir, true
}

func (s *Service) claudeConfigDir(sh ships.Ship) (string, bool) {
	f, found := s.Config().Fleet(sh.Fleet)
	if !found {
		return "", false
	}
	var env map[string]string
	if d, has := f.Harness("claude"); has {
		env = d.Env
	}
	return config.IdentityDir("claude", env), true
}

// crewOf finds a boat of the ship by name.
func (s *Service) crewOf(sh ships.Ship, name string) (sessionhost.Crew, string, bool) {
	if s.crew != nil {
		return s.crew(sh, name)
	}
	find := func() (sessionhost.Crew, bool) {
		boats, _ := s.Ships.Crew(sh.Session)
		for _, b := range boats {
			if b.Name == name {
				return b, true
			}
		}
		return sessionhost.Crew{}, false
	}
	b, ok := find()
	if !ok {
		// A boat just launched may not be listed yet.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.Ships.Host.Sync(ctx)
		cancel()
		if b, ok = find(); !ok {
			return sessionhost.Crew{}, "", false
		}
	}
	// The polled row can trail the pane by a poll, so read the recorded path itself.
	if b.PaneID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if raw, err := sessionhost.NewServer(s.Tmux, s.Socket).ReadPaneOption(ctx, b.PaneID, "crew-transcript"); err == nil {
			b.Transcript = ""
			if sessionhost.ValidCrewTranscriptPath(raw) {
				b.Transcript = raw
			}
		}
	}
	dir, _ := s.claudeConfigDir(sh)
	return b, dir, true
}

var startedWith = regexp.MustCompile(`--session-id["']?\s+["']?([0-9a-f-]{36})`)

// locate finds the ship and its transcript. ok is false for an unknown ship;
// none is true when the ship has no conversation of ours to read.
func (s *Service) locate(session string) (w where, ok, none bool) { return s.locateBoat(session, "") }

// locateBoat is locate for a crew boat of the ship, or for the ship itself when crew is empty.
func (s *Service) locateBoat(session, crew string) (w where, ok, none bool) {
	sh, ok := s.shipOf(session, true)
	if !ok {
		return where{}, false, false
	}
	w.ship = sh
	if crew != "" {
		return s.locateCrew(w, crew)
	}
	if sh.Harness != "claude" {
		return w, true, true
	}
	raw, startCmd, configDir, has := s.optionOf(sh)
	if !has {
		return w, true, true
	}
	// "<unix ns> <source> <path>"; a ship's is recorded by its launch.
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) != 3 || parts[1] != "launch" || !sessionhost.ValidTranscriptPath(parts[2]) {
		return w, true, true
	}
	id, valid := transcriptfs.SessionID(parts[2])
	// The option is writable from the pane and the start command is not, so the id the launch gave the harness decides.
	if m := startedWith.FindStringSubmatch(startCmd); !valid || m == nil || m[1] != id {
		return w, true, true
	}
	w.raw, w.path, w.id, w.configDir = raw, parts[2], id, configDir
	return w, true, false
}

// locateCrew has no start command to bind the boat's path to, since the path comes from a hook payload
// the boat controls; transcriptfs confines it to the fleet's projects dir and nothing more.
func (s *Service) locateCrew(w where, name string) (where, bool, bool) {
	boat, configDir, ok := s.crewOf(w.ship, name)
	if !ok {
		return where{}, false, false
	}
	w.crew, w.boatLaunched = name, boat.Launched
	if boat.Harness != "claude" || configDir == "" || !sessionhost.ValidCrewTranscriptPath(boat.Transcript) {
		return w, true, true
	}
	id, _ := transcriptfs.SessionID(boat.Transcript)
	w.raw, w.path, w.id, w.configDir = boat.Transcript, boat.Transcript, id, configDir
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
	if w.id == "" {
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
	session, crew := r.PathValue("session"), r.PathValue("name")
	k := key(session, crew)
	loc, ok, none := s.locateBoat(session, crew)
	if !ok {
		s.forget(k)
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
		was := s.seen[k] == loc.raw
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
	s.markSeen(k, loc.raw)
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
	crew     string
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
		Crew string `json:"crew"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Ship == "" {
		return
	}
	k := key(m.Ship, m.Crew)
	s.mu.Lock()
	for _, have := range s.clients[c] {
		if have == k {
			s.ackLocked(c, m.Ship, m.Crew)
			s.mu.Unlock()
			return
		}
	}
	s.mu.Unlock()

	loc, ok, none := s.locateBoat(m.Ship, m.Crew)
	if !ok || none {
		return // an unknown ship or boat, or one with no conversation, is ignored
	}
	w := &watch{session: m.Ship, crew: m.Crew, launched: loc.ship.Launched + "|" + loc.boatLaunched, loc: loc, reader: conversation.NewReader(conversation.Options{}),
		subs: map[*server.Client]struct{}{}}
	// Prime the reader: what is already there is not sent (the client reads the
	// page), and a tool result that arrives later still finds its call.
	if f, info, p, err := loc.open(true); err == nil {
		_, _ = w.reader.Refresh(f, info)
		f.Close()
		w.path = p
		s.markSeen(k, loc.raw)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clients == nil {
		s.clients, s.watches, s.outbox = map[*server.Client][]string{}, map[string]*watch{}, map[*server.Client]chan map[string]any{}
	}
	for _, have := range s.clients[c] {
		if have == k {
			s.ackLocked(c, m.Ship, m.Crew)
			return
		}
	}
	if cur, has := s.watches[k]; has && cur.launched == w.launched {
		w = cur
	} else {
		s.watches[k] = w
	}
	w.subs[c] = struct{}{}
	s.clients[c] = append(s.clients[c], k)
	for len(s.clients[c]) > MaxOpen {
		s.unsubscribeLocked(c, s.clients[c][0])
	}
	if _, has := s.outbox[c]; !has {
		q := make(chan map[string]any, 64)
		s.outbox[c] = q
		go func() {
			for {
				select {
				case msg := <-q:
					_ = c.Send(msg)
				case <-ctx.Done():
					s.dropClient(c)
					return
				}
			}
		}()
	}
	s.ackLocked(c, m.Ship, m.Crew)
	if !s.running {
		s.running = true
		go s.run()
	}
}

// ackLocked tells the client its open is in effect: items appended from now on are pushed, so it may read the page.
func (s *Service) ackLocked(c *server.Client, ship, crew string) {
	s.enqueueLocked(c, tagged(map[string]any{"type": "conversation_opened", "ship": ship}, crew))
}

// tagged adds the boat's name to a message about a crew boat's conversation.
func tagged(msg map[string]any, crew string) map[string]any {
	if crew != "" {
		msg["crew"] = crew
	}
	return msg
}

// enqueueLocked drops the message when the client's queue is full: its writes are stalled and the hub will close it.
func (s *Service) enqueueLocked(c *server.Client, msg map[string]any) {
	select {
	case s.outbox[c] <- msg:
	default:
	}
}

func (s *Service) handleClose(_ context.Context, c *server.Client, raw json.RawMessage) {
	var m struct {
		Ship string `json:"ship"`
		Crew string `json:"crew"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unsubscribeLocked(c, key(m.Ship, m.Crew))
}

func (s *Service) unsubscribeLocked(c *server.Client, session string) { // session is a key
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
	delete(s.outbox, c)
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
	if !s.alive(w) {
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
	s.markSeen(w.key(), w.loc.raw)
	up, _ := w.reader.Refresh(f, info)
	if len(up.Items) == 0 {
		return
	}
	msg := tagged(map[string]any{"type": "conversation", "ship": w.session, "items": up.Items}, w.crew)
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range w.subs {
		s.enqueueLocked(c, msg)
	}
}

func (s *Service) endWatch(w *watch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watches[w.key()] != w {
		return
	}
	for c := range w.subs {
		s.unsubscribeLocked(c, w.key())
	}
	delete(s.watches, w.key())
}

func (w *watch) key() string { return key(w.session, w.crew) }

// alive is false once the ship, or the boat's window, is gone or replaced under the same name.
func (s *Service) alive(w *watch) bool {
	sh, ok := s.shipOf(w.session, false)
	if !ok || sh.Launched != w.loc.ship.Launched {
		return false
	}
	if w.crew == "" {
		return true
	}
	boat, _, ok := s.crewOf(sh, w.crew)
	return ok && boat.Launched == w.loc.boatLaunched
}
