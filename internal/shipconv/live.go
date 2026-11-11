package shipconv

import (
	"context"
	"net/http"
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
	cancel            context.CancelFunc
	live              *conversation.Live // set while connected; guarded by Service.mu
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
		for {
			s.reconcile(ctx)
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

func (s *Service) reconcile(ctx context.Context) {
	want := map[string]ships.Ship{}
	for _, sh := range s.runningCodex() {
		want[sh.Session] = sh
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
		sock, cmd := s.socketOf(sh)
		if o := have[k]; o != nil {
			if o.sock == sock && o.startCmd == cmd {
				continue
			}
			s.mu.Lock()
			o.cancel()
			delete(s.observers, k)
			s.mu.Unlock()
		}
		if sock == "" {
			continue
		}
		octx, cancel := context.WithCancel(ctx)
		o := &observer{session: sh.Session, launched: sh.Launched, sock: sock, startCmd: cmd, cancel: cancel}
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

// observe connects, serves the connection until it drops, and tries again with a growing pause.
func (s *Service) observeLive(ctx context.Context, o *observer) {
	for failures := 0; ctx.Err() == nil; failures++ {
		rollout := ""
		if loc, ok, none := s.locateBoat(o.session, ""); ok && !none {
			rollout = loc.path
		}
		conn, err := codexlive.Connect(ctx, codexlive.Config{Socket: o.sock, StartCmd: o.startCmd, DaemonDir: s.dir(), Rollout: rollout, Timeout: s.timeout()})
		if err == nil {
			failures = 0
			s.serveObserved(ctx, o, conn)
		}
		select {
		case <-ctx.Done():
		case <-time.After(s.backoff(failures)):
		}
	}
}

func (s *Service) serveObserved(ctx context.Context, o *observer, conn *codexlive.Conn) {
	defer conn.Close()
	live := conversation.NewLive(conversation.Options{}, conn.Thread)
	live.Apply(conn.Resume)
	s.mu.Lock()
	o.live = live
	// A view that was on the rollout starts over from the server's history.
	s.sendLocked(o.session, map[string]any{"items": live.Items(), "reset": true})
	if w := s.watches[o.session]; w != nil {
		w.wasLive = true
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		o.live = nil
		s.mu.Unlock()
	}()
	for {
		frame, err := conn.Next(ctx)
		if err != nil {
			return
		}
		s.mu.Lock()
		if items := live.Apply(frame); len(items) > 0 {
			s.sendLocked(o.session, map[string]any{"items": items})
		}
		s.mu.Unlock()
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
