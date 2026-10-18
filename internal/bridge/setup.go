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

// setupSession stands in for a ship when the bridge watches the `_setup` session:
// underway while the session exists, gone when it ends. It never reaches the
// ships list.
func (s *Service) setupSession() (ships.Ship, bool) {
	if s.Host == nil {
		return ships.Ship{}, false
	}
	for _, sess := range s.Host.Sessions() {
		if sess.Name == sessionhost.SetupName {
			return ships.Ship{Session: sess.Name, Name: "Setup", Status: ships.StatusUnderway}, true
		}
	}
	return ships.Ship{}, false
}

// get is the ship for a session, or the setup terminal's stand-in.
func (s *Service) get(session string) (ships.Ship, bool) {
	if session == sessionhost.SetupName {
		return s.setupSession()
	}
	return s.Ships.Get(session)
}

// inputSeen is Ships.InputSeen; the setup terminal has no needs-you state.
func (s *Service) inputSeen(session string) (ships.Ship, error) {
	if session == sessionhost.SetupName {
		if sh, ok := s.setupSession(); ok {
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
