package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

type SetupCommand struct {
	Exe  string
	Home string
}

var ownSessions = map[string]string{sessionhost.SetupName: "Setup", sessionhost.ChatName: "Chat session"}

// standIn is underway while the session exists and gone when it ends; it never reaches the ships list.
func (s *Service) standIn(session string) (ships.Ship, bool) {
	name, own := ownSessions[session]
	if s.Host == nil || !own {
		return ships.Ship{}, false
	}
	for _, sess := range s.Host.Sessions() {
		if sess.Name == session {
			return ships.Ship{Session: sess.Name, Name: name, Status: ships.StatusUnderway}, true
		}
	}
	return ships.Ship{}, false
}

func (s *Service) setupSession() (ships.Ship, bool) { return s.standIn(sessionhost.SetupName) }

func (s *Service) get(session string) (ships.Ship, bool) {
	if _, own := ownSessions[session]; own {
		return s.standIn(session)
	}
	return s.Ships.Get(session)
}

// The setup terminal and the chat have no needs-you state a keystroke could clear.
func (s *Service) inputSeen(session string) (ships.Ship, error) {
	if _, own := ownSessions[session]; own {
		if sh, ok := s.standIn(session); ok {
			return sh, nil
		}
		return ships.Ship{}, ships.ErrNotFound
	}
	return s.Ships.InputSeen(session)
}

func (s *Service) server() sessionhost.Server {
	return sessionhost.NewServer(s.Tmux, s.Socket).WithoutUTF8().KeepingTMUXEnv()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// A page that lost its connection asks this to learn that setup finished meanwhile.
func (s *Service) serveSetupStatus(w http.ResponseWriter, r *http.Request) {
	_, running := s.setupSession()
	writeJSON(w, http.StatusOK, map[string]bool{"running": running})
}

// Every browser that asks joins the one running session.
func (s *Service) serveSetupStart(w http.ResponseWriter, r *http.Request) {
	if s.Setup == nil || s.Tmux == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the setup terminal needs tmux"})
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	srv := s.server()
	if !srv.HasSession(context.Background(), sessionhost.SetupName) {
		// The session environment is spelled out: a tmux server that outlived an
		// earlier Admiral would otherwise hand setup that one's.
		env := []string{"ADMIRAL_HOME=" + s.Setup.Home, sessionhost.SetupTerminalEnv + "=1", "HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH")}
		if err := srv.StartDetached(sessionhost.SetupName, 100, 30, env, []string{s.Setup.Exe, "setup"}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not start setup: " + err.Error()})
			return
		}
	}
	if s.Host != nil {
		// Not the request's context: the session must be known before anyone joins it.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Host.Sync(ctx)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"running": true})
}
