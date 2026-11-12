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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/chatlog"
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

// A Codex chat with no transcript yet: SessionStart fires on its first turn, not at start, so a pane showing its composer
// is ready, and only these screens need the engineer.
const (
	CodexTrustHooks = "Codex is asking you to trust Admiral's hooks: open the terminal and trust them"
	CodexUpdate     = "Codex is offering to update itself, and Enter would run a brew upgrade: open the terminal and choose Skip"
	CodexUntrusted  = "Codex's hooks aren't trusted, so Admiral can't follow this chat: in the terminal type /hooks, press t to trust them, then /new (or end the chat and start a new one)"
)

// CopilotTrustFolder is what the panel says while a Copilot chat is on its folder-trust dialog.
const CopilotTrustFolder = "Copilot is asking whether to trust this folder: open the terminal and choose \"Yes, and remember this folder\""

const Unreadable = "Admiral can't read this chat's transcript"

const optionEvery = 200 * time.Millisecond

// HistoryWindow is how many of a fleet's newest log entries the session serves: as many as the
// old chat's transcript tail, so a page holds the same amount whatever the log's age.
const HistoryWindow = 200

// Questions are served whether or not the chat is running.
type Snapshot struct {
	Fleet     string     `json:"fleet"`
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

// Service runs one chat per fleet whose chat is on.
type Service struct {
	Host     *sessionhost.Host
	Hub      *server.Hub
	Launcher *launcher.Launcher
	Config   func() *config.Config
	Tmux     string // the tmux binary
	Socket   string
	Home     string // $ADMIRAL_HOME, where chat/<fleet>/ is
	// Outcomes takes a usage record at each reset; nil writes none.
	Outcomes *outcomes.Writer
	// LastLive gives a chat's last live figures when its transcript has none; nil when nothing keeps them.
	LastLive func(session string) (usage.Figures, bool)
	// ForgetLive drops a chat's live figures once a reset record has read them.
	ForgetLive func(session string)
	// Actor mints each chat's token for AdmiralBot; nil leaves the chats without one.
	Actor *actor.Minter
	// OnQuestions runs when a chat's questions may have changed, for the Inbox that lists them.
	OnQuestions func()

	logOnce sync.Once
	log     *chatlog.Log

	chatsMu sync.Mutex
	chats   map[string]*fleetChat
}

// fleetChat is one fleet's chat: its session, transcript follower and questions.
type fleetChat struct {
	*Service
	key string

	op  sync.Mutex // start, New chat and send, one at a time
	qmu sync.Mutex // answering and withdrawing chat questions, one at a time

	mu       sync.Mutex // everything below; no tmux call runs under it
	st       state
	ticket   uint64 // numbers the option reads, so a late one can't undo a newer
	startErr string // why the last start was refused, until a start works
	logErr   string // the last log failure reported, so a broken disk is logged once

	cmu      sync.Mutex // the question cache; the directory is read under it, never under mu
	qs       []Question // the questions as last read
	qAt      time.Time
	qItems   map[string]cachedQuestion
	qAnswers map[string]cachedAnswer
	qSig     string // the waiting set the Inbox was last told of

	resets resetUsage
}

func (s *Service) history() *chatlog.Log {
	s.logOnce.Do(func() { s.log = chatlog.New(s.Home, time.Now) })
	return s.log
}

// chatFor is the fleet's chat, or the reason it has none: 404 for an unknown fleet, 409 when
// its chat is off.
func (s *Service) chatFor(key string) (*fleetChat, *launcher.Error) {
	_, off, ok := s.Config().ChatFleet(key)
	switch {
	case !ok:
		return nil, &launcher.Error{Status: http.StatusNotFound, Msg: "no fleet " + key}
	case off != "":
		return nil, &launcher.Error{Status: http.StatusConflict, Msg: "the chat is off for this fleet: " + off}
	}
	return s.chat(key), nil
}

func (s *Service) chat(key string) *fleetChat {
	s.chatsMu.Lock()
	defer s.chatsMu.Unlock()
	if s.chats == nil {
		s.chats = map[string]*fleetChat{}
	}
	fc, ok := s.chats[key]
	if !ok {
		fc = &fleetChat{Service: s, key: key}
		s.chats[key] = fc
	}
	return fc
}

// onFleets are the fleets whose chat is on, in config order.
func (s *Service) onFleets() []*fleetChat {
	cfg := s.Config()
	var out []*fleetChat
	for _, f := range cfg.Fleets {
		if cfg.ChatOffReason(f) == "" {
			out = append(out, s.chat(f.Key))
		}
	}
	return out
}

func (s *fleetChat) name() string { return sessionhost.ChatSession(s.key) }

// state is cleared when the pane changes.
type state struct {
	pane    string // the pane id of the fleet's chat session read last
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
	screen               string // a Codex chat's pane text while it has no transcript
}

// route resolves the path's fleet first, so an unknown fleet or one whose chat is off is refused
// with the reason before a handler runs.
func (s *Service) route(h func(*fleetChat, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fc, err := s.chatFor(r.PathValue("fleet"))
		if err != nil {
			writeError(w, err.Status, err.Msg)
			return
		}
		h(fc, w, r)
	}
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/chat/questions", s.serveAsk)
	mux.HandleFunc("GET /api/chat/questions/{id}/answer", s.serveAskAnswer)
	mux.HandleFunc("GET /api/chat/{fleet}/session", s.route((*fleetChat).serveSession))
	mux.HandleFunc("POST /api/chat/{fleet}/start", s.route((*fleetChat).serveStart))
	mux.HandleFunc("POST /api/chat/{fleet}/messages", s.route((*fleetChat).serveSend))
	mux.HandleFunc("POST /api/chat/{fleet}/new", s.route((*fleetChat).serveNew))
	mux.HandleFunc("POST /api/chat/{fleet}/questions/{id}/answer", s.route((*fleetChat).serveAnswer))
}

// Connected sends one message per fleet whose chat is on.
func (s *Service) Connected(c *server.Client) {
	for _, fc := range s.onFleets() {
		m := wire{Type: "chat", Snapshot: fc.currentFresh()}
		_ = c.SendOnce("chat:"+fc.key, func() any { return m })
	}
}

// Run also ticks, for what the host doesn't see: each chat's options and its transcript. It
// follows every running chat whether or not a browser is connected, since the log is Admiral's
// and must not miss what is said while nobody watches.
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
			if !sessionhost.IsChatSession(ev.Session) && ev.Kind != sessionhost.Reset && ev.Kind != sessionhost.Resumed {
				continue
			}
		case <-tick.C:
		}
		watched := s.Hub.Len() > 0
		for _, fc := range s.onFleets() {
			// Nobody to tell and nothing running: a request reads the chat itself.
			if _, running := fc.session(); running || watched {
				fc.broadcastIf(watched)
			}
		}
	}
}

func (s *fleetChat) broadcast() { s.broadcastIf(true) }

func (s *fleetChat) broadcastIf(send bool) {
	m := s.message()
	if send {
		s.Hub.BroadcastOnce("chat:"+s.key, func() any { return m })
		if s.questionsMoved() {
			s.questionsChanged()
		}
	}
}

// questionsMoved reports whether the questions differ from those the Inbox was last given: the ids
// and whether each is answered.
func (s *fleetChat) questionsMoved() bool {
	sig := questionsSig(s.questions(false))
	s.cmu.Lock()
	defer s.cmu.Unlock()
	moved := s.qSig != sig
	s.qSig = sig
	return moved
}

func questionsSig(qs []Question) string {
	var sig strings.Builder
	for _, q := range qs {
		sig.WriteString(q.Item.ID)
		if len(q.Answer) > 0 {
			sig.WriteString(":answered")
		}
		sig.WriteByte(' ')
	}
	return sig.String()
}

func (s *Service) questionsChanged() {
	if s.OnQuestions != nil {
		s.OnQuestions()
	}
}

func (s *fleetChat) message() wire { return wire{Type: "chat", Snapshot: s.current()} }

// A request, and a new connection, must not see a stale question list.
func (s *fleetChat) currentFresh() Snapshot {
	snap, _ := s.refreshQ(false, true)
	return snap
}

func (s *fleetChat) current() Snapshot {
	snap, _ := s.refresh(false)
	return snap
}

func (s *fleetChat) session() (sessionhost.Session, bool) {
	if s.Host == nil {
		return sessionhost.Session{}, false
	}
	for _, sess := range s.Host.Sessions() {
		if sess.Name == s.name() && !sess.PaneDead {
			return sess, true
		}
	}
	return sessionhost.Session{}, false
}

// The tmux call runs outside the lock, so a stuck tmux holds up only the caller that made it.
func (s *fleetChat) refresh(force bool) (Snapshot, error) { return s.refreshQ(force, force) }

// refreshQ reads the chat, writes what is new to the fleet's log and serves the log's recent
// window, which outlives New chat, a stopped chat and a restart.
func (s *fleetChat) refreshQ(force, fresh bool) (Snapshot, error) {
	snap, err := s.read(force, fresh)
	s.record(snap)
	snap.Fleet = s.key
	snap.Messages = s.recent()
	return snap, err
}

func (s *fleetChat) read(force, fresh bool) (Snapshot, error) {
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
		_, snap.Error = s.notStartedLocked()
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
func (s *fleetChat) transcriptChangedLocked(raw string) {
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
		o := conversation.TextView()
		o.Harness = s.harness()
		st.path, st.rd, st.unread, st.cleared = path, conversation.NewReader(o), false, false
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

// Login is the identity dir of the harness login a fleet's chat session runs under; "" for any other session.
func (s *Service) Login(session string) string {
	key, ok := sessionhost.ChatFleetOfSession(session)
	if !ok {
		return ""
	}
	if _, ok := s.Config().Fleet(key); !ok {
		return ""
	}
	return s.chat(key).loginDir()
}

// harness is the fleet's chat harness, from its config and never the session option.
func (s *fleetChat) harness() string {
	f, _ := s.Config().Fleet(s.key)
	return f.Chat.Harness
}

// loginDir is the engineer's identity dir for the chat's harness: where the login lives, which is not always where
// the chat keeps its transcripts. Copilot's chat has none until it launches.
func (s *fleetChat) loginDir() string {
	// The fleet this chat belongs to, never the session option: anything in the chat can write that.
	f, _ := s.Config().Fleet(s.key)
	h := f.Chat.Harness
	if h != "claude" && h != "codex" && h != "copilot" {
		return ""
	}
	var env map[string]string
	if d, ok := f.Harness(h); ok {
		env = d.Env
	}
	return config.IdentityDir(h, env)
}

// configDir is the dir the chat's transcripts are read under and fenced to: Claude's identity dir, or the Codex
// chat's own CODEX_HOME so a rollout in the engineer's sessions/ is never read.
func (s *fleetChat) configDir() string {
	switch s.harness() {
	case "claude":
		return s.loginDir()
	case "codex":
		return launcher.ChatCodexHome(s.Home, s.key)
	case "copilot":
		return launcher.ChatCopilotHome(s.Home, s.key)
	}
	return ""
}

func (s *fleetChat) openLocked() (*os.File, os.FileInfo, error) {
	st := &s.st
	if st.path == "" {
		return nil, nil, fs.ErrNotExist
	}
	dir := s.configDir()
	if dir == "" {
		return nil, nil, transcriptfs.ErrRefused
	}
	f, info, err := transcriptfs.OpenFor(s.harness(), dir, st.path)
	if errors.Is(err, transcriptfs.ErrRefused) && st.refused != st.path {
		st.refused = st.path
		log.Printf("admiral: chat: not reading %q: %v", st.path, err)
	}
	return f, info, err
}

func (s *fleetChat) followLocked() {
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

func (s *fleetChat) readOptions(pane string) (chatOptions, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	o, err := s.server().ReadChatOptions(ctx, pane)
	if err != nil {
		return chatOptions{}, err
	}
	opts := chatOptions{model: o.Model, effort: o.Effort, fleet: o.Fleet, transcript: o.Transcript}
	if o.Transcript == "" && (s.harness() == "codex" || s.harness() == "copilot") {
		opts.screen = s.readScreen(pane)
	}
	return opts, nil
}

func (s *fleetChat) server() sessionhost.Server {
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

func (s *fleetChat) serveSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentFresh())
}

// sync makes what a start or restart did known before the answer.
func (s *fleetChat) sync() {
	if s.Host == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Host.Sync(ctx)
}

func (s *fleetChat) exists() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.server().HasSession(ctx, s.name())
}

// ActorToken is the token of the fleet's running chat session, or "" when it has none. The nonce stays on the
// session in tmux, so the token outlives an Admiral restart while the chat lives and changes with a new session.
func (s *Service) ActorToken(fleet string) string {
	if s.Actor == nil || s.Tmux == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	nonce, err := sessionhost.NewServer(s.Tmux, s.Socket).WithoutUTF8().StderrErrors().ReadSessionOption(ctx, sessionhost.ChatSession(fleet), sessionhost.ChatNonceOpt)
	if err != nil || nonce == "" {
		return ""
	}
	return s.Actor.Token(fleet, nonce)
}

// PromptKey names the prompt the fleet's chat is answering, "<session nonce>-<prompt stamp>", or "" when the chat
// isn't running. The nonce starts a new series with each session; the stamp is the hook's, so a message typed
// in the terminal moves it too.
func (s *Service) PromptKey(fleet string) string {
	if s.Tmux == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv := sessionhost.NewServer(s.Tmux, s.Socket).WithoutUTF8().StderrErrors()
	session := sessionhost.ChatSession(fleet)
	nonce, err := srv.ReadSessionOption(ctx, session, sessionhost.ChatNonceOpt)
	if err != nil || nonce == "" {
		return ""
	}
	stamp, err := srv.ReadPaneOption(ctx, "="+session+":", sessionhost.ChatPromptOpt)
	if err != nil || !sessionhost.SafeToken.MatchString(stamp) {
		stamp = "0"
	}
	return nonce + "-" + stamp
}

func (s *fleetChat) start() *launcher.Error {
	if s.Launcher == nil || s.Tmux == "" {
		return &launcher.Error{Status: http.StatusServiceUnavailable, Msg: "the chat needs tmux"}
	}
	if s.exists() {
		return nil
	}
	err := s.Launcher.StartChat(s.key)
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

func (s *fleetChat) serveStart(w http.ResponseWriter, r *http.Request) {
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

func (s *fleetChat) serveSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	if err := s.send(req.Text); err != nil {
		writeError(w, err.Status, err.Msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

// send pastes a message into the running chat; the error is the HTTP answer the engineer's send gets.
func (s *fleetChat) send(raw string) *launcher.Error {
	text := sessionhost.CleanMessage(raw)
	switch {
	case text == "":
		return &launcher.Error{Status: http.StatusBadRequest, Msg: "the message is empty"}
	case len(text) > sessionhost.MaxPaste:
		return &launcher.Error{Status: http.StatusBadRequest, Msg: fmt.Sprintf("the message is %d bytes; at most %d", len(text), sessionhost.MaxPaste)}
	}
	s.op.Lock()
	defer s.op.Unlock()
	pane, status, msg := s.ready()
	if pane == "" {
		return &launcher.Error{Status: status, Msg: msg}
	}
	if err := s.pasteMessage(pane, text); err != nil {
		return &launcher.Error{Status: http.StatusBadGateway, Msg: "could not send the message: " + err.Error()}
	}
	return nil
}

// Send pastes a turn into the fleet's running chat, as a routine's prompt does; it errors when the chat is off or not running.
func (s *Service) Send(fleet, text string) error {
	fc, lerr := s.chatFor(fleet)
	if lerr != nil {
		return errors.New(lerr.Msg)
	}
	if lerr := fc.send(text); lerr != nil {
		return errors.New(lerr.Msg)
	}
	return nil
}

func (s *fleetChat) ready() (pane string, status int, msg string) {
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
		if ok, why := s.notStartedLocked(); !ok {
			return "", http.StatusConflict, why
		}
	}
	return s.st.pane, 0, ""
}

// notStartedLocked says whether a chat with no transcript can take a message, and what to tell the engineer if not. A
// Claude chat waits for its SessionStart; a Codex chat is ready once its composer shows with its hooks trusted, because
// its SessionStart is the first turn's.
func (s *fleetChat) notStartedLocked() (ready bool, msg string) {
	screen := s.st.opts.screen
	switch s.harness() {
	case "codex":
	case "copilot":
		return copilotReady(screen)
	default:
		return false, NotStarted
	}
	switch {
	case strings.Contains(screen, "Hooks need review"):
		return false, CodexTrustHooks
	case strings.Contains(screen, "Update now") && strings.Contains(screen, "Skip until next version"):
		return false, CodexUpdate
	case strings.Contains(screen, "enter confirm") || strings.Contains(screen, "esc skip"):
		return false, NotStarted
	case !strings.Contains(screen, "Ask Codex") && !strings.Contains(screen, "for shortcuts"):
		return false, NotStarted
	case !codexHooksTrusted(s.configDir()):
		return false, CodexUntrusted
	}
	return true, ""
}

// copilotReady: a Copilot chat's sessionStart fires on its first prompt, so a chat with no transcript is ready once its
// composer shows. The trust dialog's list also uses the prompt mark, so the footer tells the two apart.
func copilotReady(screen string) (bool, string) {
	switch {
	case strings.Contains(screen, "Confirm folder trust") || strings.Contains(screen, "Do you trust the files in this folder"):
		return false, CopilotTrustFolder
	case strings.Contains(screen, "/ commands"):
		return true, ""
	}
	return false, NotStarted
}

// codexHooksTrusted reads the chat home's config.toml for the trust entry Codex writes when the engineer trusts the
// SessionStart hook; "Continue without trusting" writes none.
func codexHooksTrusted(home string) bool {
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	return err == nil && strings.Contains(string(b), ":session_start:")
}

func (s *fleetChat) readScreen(pane string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := s.server().CapturePane(ctx, pane)
	return out
}

func (s *fleetChat) pasteMessage(pane, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.server().PasteMessage(ctx, pane, text)
}

// resetCommand is the slash command that starts a fresh conversation in the harness's TUI.
func resetCommand(harness string) string {
	if harness == "codex" {
		return "/new"
	}
	return "/clear"
}

// serveNew sends the reset command when the running session matches the config, else starts a new session.
func (s *fleetChat) serveNew(w http.ResponseWriter, r *http.Request) {
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
	harness := s.harness()
	s.mu.Unlock()
	fleet, off, _ := s.Config().ChatFleet(s.key)
	if off != "" {
		writeError(w, http.StatusConflict, "the chat is off for this fleet: "+off)
		return
	}
	chat := fleet.Chat
	if opts.model == chat.Model && opts.effort == chat.Effort && opts.fleet == fleet.Key {
		if noPath {
			writeError(w, http.StatusConflict, NotStarted)
			return
		}
		s.withdraw()
		s.mu.Lock()
		s.clearLocked()
		s.mu.Unlock()
		s.broadcast()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Typed, then Enter: the way an engineer runs a command.
		if err := s.server().SendLiteral(ctx, pane, resetCommand(s.harness())); err != nil {
			writeError(w, http.StatusBadGateway, "could not clear the chat: "+err.Error())
			return
		}
		if err := s.server().SendEnter(ctx, pane); err != nil {
			writeError(w, http.StatusBadGateway, "could not clear the chat: "+err.Error())
			return
		}
		s.recordReset(harness, path, dir, s.key, raw)
		// A reset that failed leaves no marker.
		s.markReset()
		s.broadcast()
		writeJSON(w, http.StatusOK, map[string]bool{"running": true})
		return
	}
	// The settings changed since this session started: a new one takes them.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.server().KillSession(ctx, s.name()); err != nil {
		writeError(w, http.StatusBadGateway, "could not end the chat: "+err.Error())
		return
	}
	s.recordReset(harness, path, dir, s.key, "")
	s.markReset()
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

func (s *fleetChat) clearLocked() {
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
