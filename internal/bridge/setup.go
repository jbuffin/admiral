package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

// SetupCommand is what the setup terminal runs: the admiral binary with
// `setup`, in Admiral's home.
type SetupCommand struct {
	Exe  string
	Home string
}

// ownSessions are the sessions the bridge watches that are not ships, and the
// name each shows under.
var ownSessions = map[string]string{sessionhost.SetupName: "Setup", sessionhost.ChatName: "Chat session"}

// standIn stands in for a ship when the bridge watches `_setup` or `_chat`:
// underway while the session exists, gone when it ends. Neither reaches the ships
// list.
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

// setupSession is the setup terminal's stand-in.
func (s *Service) setupSession() (ships.Ship, bool) { return s.standIn(sessionhost.SetupName) }

// get is the ship for a session, or the stand-in of the setup terminal or the chat.
func (s *Service) get(session string) (ships.Ship, bool) {
	if _, own := ownSessions[session]; own {
		return s.standIn(session)
	}
	return s.Ships.Get(session)
}

// inputSeen is Ships.InputSeen; the setup terminal and the chat have no needs-you
// state a keystroke could clear.
func (s *Service) inputSeen(session string) (ships.Ship, error) {
	if _, own := ownSessions[session]; own {
		if sh, ok := s.standIn(session); ok {
			return sh, nil
		}
		return ships.Ship{}, ships.ErrNotFound
	}
	return s.Ships.InputSeen(session)
}

func (s *Service) tmuxRun(args ...string) error {
	return exec.Command(s.Tmux, append([]string{"-f", "/dev/null", "-S", s.Socket}, args...)...).Run()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// serveSetupStatus says whether the setup terminal's session exists. A page
// that lost its connection asks this to learn that setup finished meanwhile.
func (s *Service) serveSetupStatus(w http.ResponseWriter, r *http.Request) {
	_, running := s.setupSession()
	writeJSON(w, http.StatusOK, map[string]bool{"running": running})
}

// serveSetupStart starts `admiral setup` in the `_setup` session, or leaves the
// one that is running alone, so every browser that asks joins the same one.
func (s *Service) serveSetupStart(w http.ResponseWriter, r *http.Request) {
	if s.Setup == nil || s.Tmux == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the setup terminal needs tmux"})
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.tmuxRun("has-session", "-t", "="+sessionhost.SetupName) != nil {
		// The session environment is spelled out: a tmux server that outlived an
		// earlier Admiral would otherwise hand setup that one's.
		args := []string{"new-session", "-d", "-s", sessionhost.SetupName, "-x", "100", "-y", "30",
			"-e", "ADMIRAL_HOME=" + s.Setup.Home, "-e", sessionhost.SetupTerminalEnv + "=1", "-e", "HOME=" + os.Getenv("HOME"), "-e", "PATH=" + os.Getenv("PATH"),
			"--", s.Setup.Exe, "setup"}
		if err := s.tmuxRun(args...); err != nil {
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
