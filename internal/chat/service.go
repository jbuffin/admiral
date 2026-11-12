package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/transcriptfs"
	"github.com/jbuffin/admiral/internal/usage"
)

// NotStarted holds until the first SessionStart: Claude may be on its folder-trust dialog,
// where a pasted message would land.
const NotStarted = "Open the terminal to finish starting the chat"

const Unreadable = "Admiral can't read this chat's transcript"

const optionEvery = 200 * time.Millisecond

// Questions are served whether or not the chat is running.
type Snapshot struct {
	Running   bool       `json:"running"`
	State     string     `json:"state"` // idle, working or waiting; "" while not running
	Messages  []Message  `json:"messages"`
	Questions []Question `json:"questions"`
	Error     string     `json:"error,omitempty"`
}

type wire struct {
	Type string `json:"type"`
	Snapshot
}

type Service struct {
	Host     *sessionhost.Host
	Hub      *server.Hub
	Launcher *launcher.Launcher
	Config   func() *config.Config
	Tmux     string // the tmux binary
	Socket   string
	Home     string // $ADMIRAL_HOME, where chat/questions/ is
	// Outcomes takes a usage record at each reset; nil writes none.
	Outcomes *outcomes.Writer
	// LastLive gives the chat's last live figures when its transcript has none; nil when nothing keeps them.
	LastLive func(session string) (usage.Figures, bool)
	// ForgetLive drops the chat's live figures once a reset record has read them.
	ForgetLive func(session string)

	op  sync.Mutex // start, New chat and send, one at a time
	qmu sync.Mutex // answering and withdrawing chat questions, one at a time

	mu       sync.Mutex // everything below; no tmux call runs under it
	st       state
	ticket   uint64 // numbers the option reads, so a late one can't undo a newer
	startErr string // why the last start was refused, until a start works

	cmu      sync.Mutex // the question cache; the directory is read under it, never under mu
	qs       []Question // the questions as last read
	qAt      time.Time
	qItems   map[string]cachedQuestion
	qAnswers map[string]cachedAnswer

	resets resetUsage
}

// state is cleared when the pane changes.
type state struct {
	pane    string // the pane id of the `_chat` session read last
	opts    chatOptions
	readAt  time.Time
	optRaw  string // the transcript option's value acted on last
	path    string // the transcript path in it
	rd      *conversation.Reader
	cleared bool   // a clear could not find the file's end: serve nothing until one does
	refused string // a path already logged as refused
	unread  bool   // the recorded path is one Admiral refuses to read
	optSeq  uint64 // the ticket of the option read applied last
}

type chatOptions struct {
	model, effort, fleet string
	transcript           string // "<unix ns> <source> <path>", "" before the first SessionStart
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/chat/session", s.serveSession)
	mux.HandleFunc("POST /api/chat/start", s.serveStart)
	mux.HandleFunc("POST /api/chat/messages", s.serveSend)
	mux.HandleFunc("POST /api/chat/new", s.serveNew)
	mux.HandleFunc("POST /api/chat/questions/{id}/answer", s.serveAnswer)
}

func (s *Service) Connected(c *server.Client) {
	m := wire{Type: "chat", Snapshot: s.currentFresh()}
	_ = c.SendOnce("chat", func() any { return m })
}

// Run also ticks, for what the host doesn't see: the chat's options and its transcript.
func (s *Service) Run(ctx context.Context) {
	if s.Host == nil {
		return
	}
	events, cancel := s.Host.Subscribe()
	defer cancel()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			if ev.Session != sessionhost.ChatName && ev.Kind != sessionhost.Reset && ev.Kind != sessionhost.Resumed {
				continue
			}
		case <-tick.C:
		}
		if s.Hub.Len() == 0 {
			continue // nobody to tell; a request reads the chat itself
		}
		s.broadcast()
	}
}

func (s *Service) broadcast() {
	m := s.message()
	s.Hub.BroadcastOnce("chat", func() any { return m })
}

func (s *Service) message() wire { return wire{Type: "chat", Snapshot: s.current()} }

// A request, and a new connection, must not see a stale question list.
func (s *Service) currentFresh() Snapshot {
	snap, _ := s.refreshQ(false, true)
	return snap
}

func (s *Service) current() Snapshot {
	snap, _ := s.refresh(false)
	return snap
}

func (s *Service) session() (sessionhost.Session, bool) {
	if s.Host == nil {
		return sessionhost.Session{}, false
	}
	for _, sess := range s.Host.Sessions() {
		if sess.Name == sessionhost.ChatName && !sess.PaneDead {
			return sess, true
		}
	}
	return sessionhost.Session{}, false
}

// The tmux call runs outside the lock, so a stuck tmux holds up only the caller that made it.
func (s *Service) refresh(force bool) (Snapshot, error) { return s.refreshQ(force, force) }

func (s *Service) refreshQ(force, fresh bool) (Snapshot, error) {
	qs := s.questions(fresh)
	sess, running := s.session()
	var (
		opts    chatOptions
		have    bool
		readErr error
		ticket  uint64
	)
	if running {
		s.mu.Lock()
		need := force || sess.PaneID != s.st.pane || time.Since(s.st.readAt) >= optionEvery
		s.ticket++
		ticket = s.ticket
		s.mu.Unlock()
		if need {
			opts, readErr = s.readOptions(sess.PaneID)
			have = readErr == nil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &s.st
	if !running {
		*st = state{}
		return Snapshot{Messages: []Message{}, Questions: qs, Error: s.startErr}, nil
	}
	if sess.PaneID != st.pane {
		// A new session (the first one Admiral sees, or a restart): nothing of the
		// old one's conversation carries over, and a transcript is read from its start.
		*st = state{pane: sess.PaneID}
	}
	if have && ticket > st.optSeq {
		st.opts, st.readAt, st.optSeq = opts, time.Now(), ticket
	}
	if st.opts.transcript != st.optRaw {
		s.transcriptChangedLocked(st.opts.transcript)
	}
	s.followLocked()

	snap := Snapshot{Running: true, State: mapState(sess.State, sess.Reason), Messages: st.served(), Questions: qs}
	if unanswered(qs) {
		snap.State = "waiting"
	}
	switch {
	case st.optRaw == "":
		snap.Error = NotStarted
	case st.unread:
		snap.Error = Unreadable
	}
	if force && readErr != nil {
		return snap, readErr
	}
	return snap, nil
}

func mapState(state, reason string) string {
	switch {
	case state == "working":
		return "working"
	case state == "needs_you" && reason != "stop":
		return "waiting"
	}
	return "idle"
}

// The same path with source "clear" empties the conversation and serves only what is written after.
func (s *Service) transcriptChangedLocked(raw string) {
	st := &s.st
	first := st.optRaw == ""
	st.optRaw = raw
	_, source, path, ok := parseTranscriptOption(raw)
	if !ok {
		st.path, st.rd, st.unread, st.cleared = "", nil, false, false
		return
	}
	switch {
	case path != st.path || st.rd == nil:
		st.path, st.rd, st.unread, st.cleared = path, conversation.NewReader(conversation.TextView()), false, false
	case source == "clear" && !first:
		s.clearLocked()
	}
}

func parseTranscriptOption(raw string) (stamp, source, path string, ok bool) {
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// Login is the identity dir of the harness login the chat runs under.
func (s *Service) Login() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.configDir()
}

func (s *Service) configDir() string {
	cfg := s.Config()
	// The fleet the session started with. The option can be written from inside the
	// chat, so one that names no fleet falls back to the chat's own.
	f, ok := cfg.Fleet(s.st.opts.fleet)
	if !ok {
		f, _ = cfg.ChatFleet()
	}
	var env map[string]string
	if d, ok := f.Harness("claude"); ok {
		env = d.Env
	}
	return config.IdentityDir("claude", env)
}

func (s *Service) openLocked() (*os.File, os.FileInfo, error) {
	st := &s.st
	if st.path == "" {
		return nil, nil, fs.ErrNotExist
	}
	f, info, err := transcriptfs.Open(s.configDir(), st.path)
	if errors.Is(err, transcriptfs.ErrRefused) && st.refused != st.path {
		st.refused = st.path
		log.Printf("admiral: chat: not reading %q: %v", st.path, err)
	}
	return f, info, err
}

func (s *Service) followLocked() {
	st := &s.st
	if st.rd == nil {
		return
	}
	f, info, err := s.openLocked()
	if err != nil {
		st.unread = errors.Is(err, transcriptfs.ErrRefused)
		return
	}
	defer f.Close()
	st.unread = false
	if st.cleared {
		return
	}
	// ErrUnreadable (content, but no line that parses) shows an empty chat.
	_, _ = st.rd.Refresh(f, info)
}

// served is nothing after a clear whose file could not be opened.
func (st *state) served() []Message {
	if st.rd == nil || st.cleared {
		return []Message{}
	}
	return messages(st.rd.Items())
}

func (s *Service) readOptions(pane string) (chatOptions, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	o, err := s.server().ReadChatOptions(ctx, pane)
	if err != nil {
		return chatOptions{}, err
	}
	return chatOptions{model: o.Model, effort: o.Effort, fleet: o.Fleet, transcript: o.Transcript}, nil
}

func (s *Service) server() sessionhost.Server {
	return sessionhost.NewServer(s.Tmux, s.Socket).WithoutUTF8().StderrErrors()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Service) serveSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentFresh())
}

// sync makes what a start or restart did known before the answer.
func (s *Service) sync() {
	if s.Host == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Host.Sync(ctx)
}

func (s *Service) exists() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.server().HasSession(ctx, sessionhost.ChatName)
}

func (s *Service) start() *launcher.Error {
	if s.Launcher == nil || s.Tmux == "" {
		return &launcher.Error{Status: http.StatusServiceUnavailable, Msg: "the chat needs tmux"}
	}
	if s.exists() {
		return nil
	}
	err := s.Launcher.StartChat()
	var le *launcher.Error
	switch {
	case err == nil:
	case errors.As(err, &le):
	default:
		le = &launcher.Error{Status: http.StatusInternalServerError, Msg: err.Error()}
	}
	s.mu.Lock()
	s.startErr = ""
	if le != nil {
		s.startErr = le.Msg
	}
	s.mu.Unlock()
	if le != nil {
		return le
	}
	return nil
}

func (s *Service) serveStart(w http.ResponseWriter, r *http.Request) {
	s.op.Lock()
	defer s.op.Unlock()
	if err := s.start(); err != nil {
		writeError(w, err.Status, err.Msg)
		return
	}
	s.sync()
	s.broadcast()
	writeJSON(w, http.StatusOK, map[string]bool{"running": true})
}

func (s *Service) serveSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	text := sessionhost.CleanMessage(req.Text)
	switch {
	case text == "":
		writeError(w, http.StatusBadRequest, "the message is empty")
		return
	case len(text) > sessionhost.MaxPaste:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("the message is %d bytes; at most %d", len(text), sessionhost.MaxPaste))
		return
	}
	s.op.Lock()
	defer s.op.Unlock()
	pane, status, msg := s.ready()
	if pane == "" {
		writeError(w, status, msg)
		return
	}
	if err := s.pasteMessage(pane, text); err != nil {
		writeError(w, http.StatusBadGateway, "could not send the message: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func (s *Service) ready() (pane string, status int, msg string) {
	snap, err := s.refresh(true)
	switch {
	case !snap.Running:
		return "", http.StatusConflict, "the chat is not running"
	case err != nil:
		return "", http.StatusBadGateway, "could not read the chat's state: " + err.Error()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.optRaw == "" {
		return "", http.StatusConflict, NotStarted
	}
	return s.st.pane, 0, ""
}

func (s *Service) pasteMessage(pane, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.server().PasteMessage(ctx, pane, text)
}

// serveNew sends /clear when the running session matches the config, else starts a new session.
func (s *Service) serveNew(w http.ResponseWriter, r *http.Request) {
	s.op.Lock()
	defer s.op.Unlock()
	snap, err := s.refresh(true)
	if !snap.Running {
		writeError(w, http.StatusConflict, "the chat is not running")
		return
	}
	if err != nil {
		// Without the options the settings can't be compared: don't restart a healthy session.
		writeError(w, http.StatusBadGateway, "could not read the chat's state: "+err.Error())
		return
	}
	s.mu.Lock()
	opts, pane, noPath := s.st.opts, s.st.pane, s.st.optRaw == ""
	path, dir, raw := s.st.path, s.configDir(), s.st.optRaw
	s.mu.Unlock()
	fleet, _ := s.Config().ChatFleet()
	chat := fleet.Chat
	if opts.model == chat.Model && opts.effort == chat.Effort && opts.fleet == fleet.Key {
		if noPath {
			writeError(w, http.StatusConflict, NotStarted)
			return
		}
		s.WithdrawQuestions()
		s.mu.Lock()
		s.clearLocked()
		s.mu.Unlock()
		s.broadcast()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Typed, then Enter: the way an engineer runs a command.
		if err := s.server().SendLiteral(ctx, pane, "/clear"); err != nil {
			writeError(w, http.StatusBadGateway, "could not clear the chat: "+err.Error())
			return
		}
		if err := s.server().SendEnter(ctx, pane); err != nil {
			writeError(w, http.StatusBadGateway, "could not clear the chat: "+err.Error())
			return
		}
		s.recordReset(path, dir, opts.fleet, raw)
		writeJSON(w, http.StatusOK, map[string]bool{"running": true})
		return
	}
	// The settings changed since this session started: a new one takes them.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.server().KillSession(ctx, sessionhost.ChatName); err != nil {
		writeError(w, http.StatusBadGateway, "could not end the chat: "+err.Error())
		return
	}
	s.recordReset(path, dir, opts.fleet, "")
	s.mu.Lock()
	s.st = state{}
	s.mu.Unlock()
	if err := s.start(); err != nil {
		s.sync()
		s.broadcast()
		writeError(w, err.Status, err.Msg)
		return
	}
	s.sync()
	s.broadcast()
	writeJSON(w, http.StatusOK, map[string]bool{"running": true})
}

func (s *Service) clearLocked() {
	st := &s.st
	if st.rd == nil {
		return
	}
	f, info, err := s.openLocked()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Nothing to skip: the file is read from its start once it appears.
		st.rd.Clear(0)
		st.cleared = false
		return
	case err != nil:
		st.cleared = true
		return
	}
	f.Close()
	st.rd.Clear(info.Size())
	st.cleared = false
}
