package chat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// NotStarted is the error the session carries until the first SessionStart has
// recorded a transcript. On a first start in a new folder Claude may be on its
// folder-trust dialog, where a pasted message would land (ADR 0019).
const NotStarted = "Open the terminal to finish starting the chat"

// Unreadable is the error when the recorded transcript is one Admiral refuses to
// read, so the engineer knows why the chat is blank.
const Unreadable = "Admiral can't read this chat's transcript"

// maxSend is the longest message a send takes.
const maxSend = 16 << 10

// optionEvery is how often the chat's options are read from tmux at most.
const optionEvery = 200 * time.Millisecond

const sep = "\x1f"

// Snapshot is what GET /api/chat/session and the `chat` WebSocket message carry.
// The wire message adds "type". Questions are the chat questions (ADR 0020): the
// newest 20, oldest first, served whether or not the chat is running.
type Snapshot struct {
	Running   bool       `json:"running"`
	State     string     `json:"state"` // idle, working or waiting; "" while not running
	Messages  []Message  `json:"messages"`
	Questions []Question `json:"questions"`
	Error     string     `json:"error,omitempty"`
}

// wire is the WebSocket form of a Snapshot.
type wire struct {
	Type string `json:"type"`
	Snapshot
}

// Service is the chat session's server module.
type Service struct {
	Host     *sessionhost.Host
	Hub      *server.Hub
	Launcher *launcher.Launcher
	Config   func() *config.Config
	Tmux     string // the tmux binary
	Socket   string
	Home     string // $ADMIRAL_HOME, where chat/questions/ is

	op  sync.Mutex // start, New chat and send, one at a time
	qmu sync.Mutex // answering and withdrawing chat questions, one at a time

	mu       sync.Mutex // everything below; no tmux call runs under it
	st       state
	ticket   uint64 // numbers the option reads, so a late one can't undo a newer
	startErr string // why the last start was refused, until a start works

	cmu sync.Mutex // the question cache; the directory is read under it, never under mu
	qs  []Question // the questions as last read
	qAt time.Time
	// What each question and answer file read as, by id, while its time and size hold.
	qItems   map[string]cachedQuestion
	qAnswers map[string]cachedAnswer
}

// state is the chat as last read. It is cleared when the pane changes.
type state struct {
	pane    string // the pane id of the `_chat` session read last
	opts    chatOptions
	readAt  time.Time
	optRaw  string // the transcript option's value acted on last
	path    string // the transcript path in it
	fol     *follower
	msgs    []Message
	bytes   int
	refused string // a path already logged as refused
	unread  bool   // the recorded path is one Admiral refuses to read
	optSeq  uint64 // the ticket of the option read applied last
}

// chatOptions are the options one tmux read returns.
type chatOptions struct {
	model, effort, fleet string
	transcript           string // "<unix ns> <source> <path>", "" before the first SessionStart
}

// Routes registers the chat API.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/chat/session", s.serveSession)
	mux.HandleFunc("POST /api/chat/start", s.serveStart)
	mux.HandleFunc("POST /api/chat/messages", s.serveSend)
	mux.HandleFunc("POST /api/chat/new", s.serveNew)
	mux.HandleFunc("POST /api/chat/questions/{id}/answer", s.serveAnswer)
}

// Connected sends a new client the chat.
func (s *Service) Connected(c *server.Client) {
	m := wire{Type: "chat", Snapshot: s.currentFresh()}
	_ = c.SendOnce("chat", func() any { return m })
}

// Run follows the session until ctx ends: on every event of the host about it,
// and on a short tick for what the host doesn't see (the chat's options and its
// transcript), it pushes the chat when it changed.
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

// currentFresh is current with the questions read from their directory now: a
// request, and a new connection, must not see a stale list.
func (s *Service) currentFresh() Snapshot {
	snap, _ := s.refreshQ(false, true)
	return snap
}

// current reads the chat and returns it.
func (s *Service) current() Snapshot {
	snap, _ := s.refresh(false)
	return snap
}

// session is the `_chat` session as the host last read it.
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

// refresh reads the session from the host, its options from tmux and its
// transcript, and returns the snapshot. force reads the options even if they were
// read a moment ago, and then a failed read is an error (the snapshot is still
// returned). The tmux call runs outside the lock, so a stuck tmux holds up only
// the caller that made it.
func (s *Service) refresh(force bool) (Snapshot, error) { return s.refreshQ(force, force) }

// refreshQ is refresh, and fresh reads the questions directory even if it was read a
// moment ago.
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

	snap := Snapshot{Running: true, State: mapState(sess.State, sess.Reason), Messages: append([]Message{}, st.msgs...), Questions: qs}
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

// mapState turns the hook's ship vocabulary into the chat's: working is working;
// needs you with the reason stop is idle; any other needs you is waiting.
func mapState(state, reason string) string {
	switch {
	case state == "working":
		return "working"
	case state == "needs_you" && reason != "stop":
		return "waiting"
	}
	return "idle"
}

// transcriptChangedLocked acts on a new value of the transcript option: a new
// path starts a new conversation read from its start; the same path with source
// "clear" empties it and serves only what is written after.
func (s *Service) transcriptChangedLocked(raw string) {
	st := &s.st
	first := st.optRaw == ""
	st.optRaw = raw
	_, source, path, ok := parseTranscriptOption(raw)
	if !ok {
		st.path, st.fol, st.unread = "", nil, false
		st.msgs, st.bytes = nil, 0
		return
	}
	switch {
	case path != st.path || st.fol == nil:
		st.path, st.fol, st.unread = path, &follower{}, false
		st.msgs, st.bytes = nil, 0
	case source == "clear" && !first:
		s.clearLocked()
	}
}

// parseTranscriptOption splits "<unix ns> <source> <path>".
func parseTranscriptOption(raw string) (stamp, source, path string, ok bool) {
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// configDir is the Claude config directory the running chat uses: that of the
// dock of the fleet it started with.
func (s *Service) configDir() string {
	cfg := s.Config()
	// The fleet the session started with. The option can be written from inside the
	// chat, so one that names no fleet falls back to the chat's own.
	f, ok := cfg.Fleet(s.st.opts.fleet)
	if !ok {
		f, _ = cfg.Fleet(cfg.Chat.Fleet)
	}
	var env map[string]string
	if d, ok := f.Harness("claude"); ok {
		env = d.Env
	}
	return config.IdentityDir("claude", env)
}

// openLocked opens the transcript at the recorded path if it may be read.
func (s *Service) openLocked() (string, os.FileInfo, *os.File, error) {
	st := &s.st
	if st.path == "" {
		return "", nil, nil, fs.ErrNotExist
	}
	root, rel, err := resolveTranscript(s.configDir(), st.path)
	if err == nil {
		var r *os.Root
		if r, err = os.OpenRoot(root); err == nil {
			defer r.Close()
			var f *os.File
			// O_NONBLOCK: a FIFO swapped in after the checks must not block the open.
			// A regular file reads the same either way.
			if f, err = r.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
				var info os.FileInfo
				if info, err = f.Stat(); err == nil && info.Mode().IsRegular() {
					return rel, info, f, nil
				}
				f.Close()
				err = errRefused
			}
		}
	}
	if errors.Is(err, errRefused) && st.refused != st.path {
		st.refused = st.path
		log.Printf("admiral: chat: not reading %q: %v", st.path, err)
	}
	return "", nil, nil, err
}

// followLocked reads what the transcript has gained.
func (s *Service) followLocked() {
	st := &s.st
	if st.fol == nil {
		return
	}
	_, info, f, err := s.openLocked()
	if err != nil {
		st.unread = errors.Is(err, errRefused)
		return
	}
	defer f.Close()
	st.unread = false
	st.fol.read(f, info, s.add, func() { st.msgs, st.bytes = nil, 0 })
}

// add keeps a message, and only the last maxMessages of them within maxBytes.
func (s *Service) add(m Message) {
	st := &s.st
	st.msgs = append(st.msgs, m)
	st.bytes += len(m.Text)
	for len(st.msgs) > 1 && (len(st.msgs) > maxMessages || st.bytes > maxBytes) {
		st.bytes -= len(st.msgs[0].Text)
		st.msgs = st.msgs[1:]
	}
}

// readOptions reads the chat's options off its pane in one tmux call. The chat
// can write any of them. The transcript is last, so a separator in it can't shift
// the others; one in another option only shifts the fields after it, which makes
// the settings look changed (New chat then restarts the session).
func (s *Service) readOptions(pane string) (chatOptions, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	format := strings.Join([]string{"#{" + sessionhost.ChatModelOption + "}", "#{" + sessionhost.ChatEffortOption + "}",
		"#{" + sessionhost.ChatFleetOption + "}", "#{" + sessionhost.TranscriptOption + "}"}, sep)
	out, err := s.tmux(ctx, "", "-u", "display-message", "-p", "-t", pane, format)
	if err != nil {
		return chatOptions{}, err
	}
	f := strings.SplitN(strings.TrimSuffix(out, "\n"), sep, 4)
	if len(f) != 4 {
		return chatOptions{}, fmt.Errorf("unexpected options %q", out)
	}
	return chatOptions{model: f[0], effort: f[1], fleet: f[2], transcript: f[3]}, nil
}

// tmux runs the tmux binary on Admiral's socket with stdin as given.
func (s *Service) tmux(ctx context.Context, stdin string, args ...string) (string, error) {
	// -u is a tmux flag, not a command: it goes before the command.
	var flags []string
	for len(args) > 0 && args[0] == "-u" {
		flags, args = append(flags, args[0]), args[1:]
	}
	cmd := exec.CommandContext(ctx, s.Tmux, append(append([]string{"-f", "/dev/null", "-S", s.Socket}, flags...), args...)...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") && !strings.HasPrefix(kv, "TMUX_PANE=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("tmux %s: %w: %s", args[0], err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
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

// sync makes the host read the sessions now, so what a start or restart did is
// known before the answer.
func (s *Service) sync() {
	if s.Host == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Host.Sync(ctx)
}

// exists says whether the `_chat` session is in tmux.
func (s *Service) exists() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := s.tmux(ctx, "", "has-session", "-t", "="+sessionhost.ChatName)
	return err == nil
}

// start makes the session if there is none.
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

// serveStart starts the chat session, or leaves the running one alone.
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

// clean is a message ready to paste: line ends as LF, control characters
// (an escape among them, which could end the bracketed paste early) dropped,
// except LF and tab.
func clean(text string) string {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, text)
	return strings.TrimSpace(text)
}

// serveSend pastes a message into the chat and presses Enter.
func (s *Service) serveSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	text := clean(req.Text)
	switch {
	case text == "":
		writeError(w, http.StatusBadRequest, "the message is empty")
		return
	case len(text) > maxSend:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("the message is %d bytes; at most %d", len(text), maxSend))
		return
	}
	s.op.Lock()
	defer s.op.Unlock()
	pane, status, msg := s.ready()
	if pane == "" {
		writeError(w, status, msg)
		return
	}
	if err := s.paste(pane, text); err != nil {
		writeError(w, http.StatusBadGateway, "could not send the message: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

// ready is the chat's pane when a message can go in: it runs, and a SessionStart
// has recorded its transcript.
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

// paste puts text into the pane as one bracketed paste, then Enter. The text goes
// to tmux on stdin through a buffer of its own, never on a command line or through
// a shell. -r keeps LF as LF; tmux's own sanitising stays on.
func (s *Service) paste(pane, text string) error {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	buf := "admiral-chat-" + hex.EncodeToString(b[:])
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.tmux(ctx, text, "load-buffer", "-b", buf, "-"); err != nil {
		return err
	}
	if _, err := s.tmux(ctx, "", "paste-buffer", "-p", "-r", "-d", "-b", buf, "-t", pane); err != nil {
		_, _ = s.tmux(ctx, "", "delete-buffer", "-b", buf)
		return err
	}
	_, err := s.tmux(ctx, "", "send-keys", "-t", pane, "Enter")
	return err
}

// serveNew resets the conversation: /clear when the running session matches the
// config, else a new session. Every browser's conversation empties at once.
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
	s.mu.Unlock()
	chat := s.Config().Chat
	if opts.model == chat.Model && opts.effort == chat.Effort && opts.fleet == chat.Fleet {
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
		if _, err := s.tmux(ctx, "", "send-keys", "-t", pane, "-l", "/clear"); err != nil {
			writeError(w, http.StatusBadGateway, "could not clear the chat: "+err.Error())
			return
		}
		if _, err := s.tmux(ctx, "", "send-keys", "-t", pane, "Enter"); err != nil {
			writeError(w, http.StatusBadGateway, "could not clear the chat: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"running": true})
		return
	}
	// The settings changed since this session started: a new one takes them.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.tmux(ctx, "", "kill-session", "-t", "="+sessionhost.ChatName); err != nil {
		writeError(w, http.StatusBadGateway, "could not end the chat: "+err.Error())
		return
	}
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

// clearLocked empties the conversation and keeps the transcript followed from
// its current end, so nothing already written comes back.
func (s *Service) clearLocked() {
	st := &s.st
	st.msgs, st.bytes = nil, 0
	if st.fol == nil {
		return
	}
	if _, info, f, err := s.openLocked(); err == nil {
		f.Close()
		st.fol.toEnd(info.Size())
	}
}
