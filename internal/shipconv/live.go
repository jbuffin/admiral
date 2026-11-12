package shipconv

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/codexlive"
	"github.com/jbuffin/admiral/internal/codexpane"
	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

const reconcileEvery = 500 * time.Millisecond

// observer is the one connection to a running Codex ship's app-server, shared by every view of the ship.
type observer struct {
	session, launched string
	sock, startCmd    string
	rollout           string // the recorded rollout this observer picked its thread by; a new one means the TUI moved on
	prefer            string // the thread the server last told of, kept across reconnects; only observe reads it
	asking            bool   // the pane was told a question waits; only the observer's goroutine touches it
	cancel            context.CancelFunc
	live              *conversation.Live // set while connected; guarded by Service.mu
}

// liveRead is what the panes said about a ship at, so the tmux reads are not repeated every tick.
type liveRead struct {
	sock, startCmd, rollout string
	at                      time.Time
	panes                   string
}

func (s *Service) runningCodex() []ships.Ship {
	if s.liveShips != nil {
		return s.liveShips()
	}
	var out []ships.Ship
	for _, sh := range s.Ships.List() {
		if sh.Harness == "codex" && (sh.Status == ships.StatusUnderway || sh.Status == ships.StatusNeedsYou) {
			out = append(out, sh)
		}
	}
	return out
}

// socketOf is the socket the pane recorded and the command it started with.
func (s *Service) socketOf(sh ships.Ship) (sock, startCmd string) {
	if s.liveSocket != nil {
		return s.liveSocket(sh)
	}
	pane := ""
	for _, sess := range s.Ships.Host.Sessions() {
		if sess.Name == sh.Session {
			pane = sess.PaneID
		}
	}
	if pane == "" {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	host := sessionhost.NewServer(s.Tmux, s.Socket)
	sock, err := host.ReadPaneOption(ctx, pane, "codex-socket")
	if err != nil {
		return "", ""
	}
	startCmd, err = host.ReadPaneStartCommand(ctx, pane)
	if err != nil {
		return "", ""
	}
	return sock, startCmd
}

// StartLive keeps an observer on each running Codex ship until ctx ends.
func (s *Service) StartLive(ctx context.Context) {
	go func() {
		t := time.NewTicker(reconcileEvery)
		defer t.Stop()
		reads := map[string]liveRead{}
		for {
			s.reconcile(ctx, reads)
			select {
			case <-ctx.Done():
				s.mu.Lock()
				for k, o := range s.observers {
					o.cancel()
					delete(s.observers, k)
				}
				s.mu.Unlock()
				return
			case <-t.C:
			}
		}
	}()
}

// paneSig changes when a running Codex ship gains, loses or replaces a pane, which is when its options are read again.
func (s *Service) paneSig(want map[string]ships.Ship) string {
	var parts []string
	for k, sh := range want {
		parts = append(parts, k+"|"+sh.Launched)
	}
	if s.liveSocket == nil && s.Ships != nil && s.Ships.Host != nil {
		for _, sess := range s.Ships.Host.Sessions() {
			if _, ok := want[sess.Name]; ok {
				parts = append(parts, sess.Name+"="+sess.PaneID)
			}
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func (s *Service) readEvery() time.Duration {
	if s.liveReadEvery > 0 {
		return s.liveReadEvery
	}
	return 5 * time.Second
}

func (s *Service) reconcile(ctx context.Context, reads map[string]liveRead) {
	want := map[string]ships.Ship{}
	for _, sh := range s.runningCodex() {
		want[sh.Session] = sh
	}
	sig := s.paneSig(want)
	for k := range reads {
		if _, ok := want[k]; !ok {
			delete(reads, k)
		}
	}
	s.mu.Lock()
	if s.observers == nil {
		s.observers = map[string]*observer{}
	}
	for k, o := range s.observers {
		if sh, ok := want[k]; !ok || sh.Launched != o.launched {
			o.cancel()
			delete(s.observers, k)
		}
	}
	have := map[string]*observer{}
	for k, o := range s.observers {
		have[k] = o
	}
	s.mu.Unlock()
	for k, sh := range want {
		r, known := reads[k]
		if !known || r.panes != sig || time.Since(r.at) >= s.readEvery() {
			r = liveRead{panes: sig, at: time.Now()}
			r.sock, r.startCmd = s.socketOf(sh)
			if loc, ok, none := s.locateBoat(sh.Session, ""); ok && !none {
				r.rollout = loc.path
			}
			reads[k] = r
		}
		if o := have[k]; o != nil {
			if o.sock == r.sock && o.startCmd == r.startCmd && o.rollout == r.rollout {
				continue
			}
			s.mu.Lock()
			o.cancel()
			delete(s.observers, k)
			s.mu.Unlock()
		}
		if r.sock == "" {
			continue
		}
		octx, cancel := context.WithCancel(ctx)
		o := &observer{session: sh.Session, launched: sh.Launched, sock: r.sock, startCmd: r.startCmd, rollout: r.rollout, cancel: cancel}
		s.mu.Lock()
		s.observers[k] = o
		s.mu.Unlock()
		go s.observeLive(octx, o)
	}
}

func (s *Service) dir() string {
	if s.liveDir != "" {
		return s.liveDir
	}
	return codexpane.DaemonDir()
}

func (s *Service) timeout() time.Duration {
	if s.liveTimeout > 0 {
		return s.liveTimeout
	}
	return 5 * time.Second
}

func (s *Service) backoff(failures int) time.Duration {
	if s.liveBackoff != nil {
		return s.liveBackoff(failures)
	}
	d := time.Second << min(failures, 5)
	return min(d, 30*time.Second)
}

// observeLive connects, serves the connection until it drops, and tries again with a growing pause. A thread the server
// told of is attached to at once.
func (s *Service) observeLive(ctx context.Context, o *observer) {
	for failures := 0; ctx.Err() == nil; failures++ {
		conn, err := codexlive.Connect(ctx, codexlive.Config{Socket: o.sock, StartCmd: o.startCmd, DaemonDir: s.dir(), Rollout: o.rollout, Prefer: o.prefer, Timeout: s.timeout()})
		if err == nil {
			failures = 0
			if s.serveObserved(ctx, o, conn) {
				failures = -1 // the loop adds one: no pause
				continue
			}
		} else {
			s.mu.Lock()
			o.live = nil // a switch kept the old thread's items only until the new one answered
			s.mu.Unlock()
		}
		select {
		case <-ctx.Done():
		case <-time.After(s.backoff(failures)):
		}
	}
}

// moved is the thread a thread/started frame says the TUI has started, resumed or forked, when it is not the one attached to.
func moved(frame []byte, current string) string {
	var f struct {
		Method string `json:"method"`
		Params struct {
			Thread struct {
				ID        string          `json:"id"`
				Ephemeral bool            `json:"ephemeral"`
				Source    json.RawMessage `json:"source"`
			} `json:"thread"`
		} `json:"params"`
	}
	if json.Unmarshal(frame, &f) != nil || f.Method != "thread/started" {
		return ""
	}
	t := f.Params.Thread
	// A sub-agent's thread runs beside the TUI's, which stays where it was.
	if t.ID == "" || t.ID == current || t.Ephemeral || strings.Contains(string(t.Source), "subAgent") {
		return ""
	}
	return t.ID
}

// serveObserved serves one connection; it reports whether it ended to follow the TUI to another thread.
func (s *Service) serveObserved(ctx context.Context, o *observer, conn *codexlive.Conn) (switched bool) {
	defer conn.Close()
	live := conversation.NewLive(conversation.Options{}, conn.Thread)
	live.Apply(conn.Resume)
	s.mu.Lock()
	o.live = live
	// A view that was on the rollout, or on another thread, starts over from the server's history.
	s.sendLocked(o.session, map[string]any{"items": live.Items(), "reset": true})
	if w := s.watches[o.session]; w != nil {
		w.wasLive = true
	}
	s.mu.Unlock()
	defer func() {
		if switched {
			return // the next connection replaces it without a gap the rollout could fill
		}
		s.mu.Lock()
		o.live = nil
		s.mu.Unlock()
	}()
	s.signal(o, live)
	for {
		frame, err := conn.Next(ctx)
		if err != nil {
			return false
		}
		if s.LiveFrame != nil {
			s.LiveFrame(o.session, frame)
		}
		if id := moved(frame, conn.Thread); id != "" {
			o.prefer = id
			s.signal(o, live) // the thread it leaves asks nothing now
			return true
		}
		s.mu.Lock()
		if items := live.Apply(frame); len(items) > 0 {
			s.sendLocked(o.session, map[string]any{"items": items})
		}
		s.mu.Unlock()
		s.signal(o, live)
	}
}

// signal tells the ship when a question starts or stops waiting on the server. Codex's TUI rings no bell for a
// question in a pane it thinks is focused, and no hook fires for one, so the pending request is the signal. A drop
// leaves the pane as it is.
func (s *Service) signal(o *observer, live *conversation.Live) {
	asking := len(live.Pending()) > 0
	if asking == o.asking {
		return
	}
	o.asking = asking
	if s.Asking != nil {
		s.Asking(o.session, asking)
	}
}

// sendLocked sends a conversation message to the views of a ship.
func (s *Service) sendLocked(session string, msg map[string]any) {
	w := s.watches[session]
	if w == nil {
		return
	}
	msg["type"], msg["ship"] = "conversation", session
	for c := range w.subs {
		s.enqueueLocked(c, msg)
	}
}

func (s *Service) liveObserver(session string) *observer {
	o := s.observers[session]
	if o == nil || o.live == nil {
		return nil
	}
	return o
}

func (s *Service) liveUp(session string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.liveObserver(session) != nil
}

func (s *Service) liveItems(session string) ([]conversation.Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o := s.liveObserver(session); o != nil {
		return o.live.Items(), true
	}
	return nil, false
}

func (s *Service) livePending(session string) ([]conversation.Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o := s.liveObserver(session); o != nil {
		return o.live.Pending(), true
	}
	return nil, false
}

// ApprovalPending is whether the app-server of a Codex ship waits on an approval.
func (s *Service) ApprovalPending(session, crew string) bool {
	if crew != "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.liveObserver(session)
	return o != nil && o.live.Approving()
}

// serveLive pages the server's conversation the way the rollout's is paged.
func (s *Service) serveLive(w http.ResponseWriter, all []conversation.Item, before string) {
	end := len(all)
	if before != "" {
		end = -1
		for i, it := range all {
			if it.ID == before {
				end = i
			}
		}
		if end < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "before is not the id of an item"})
			return
		}
	}
	start := max(0, end-pageSize)
	page := Page{State: StateOK, Items: append([]conversation.Item{}, all[start:end]...), More: start > 0}
	writeJSON(w, http.StatusOK, page)
}
