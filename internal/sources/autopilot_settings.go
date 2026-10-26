package sources

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
)

// autopilotSettings is what the autopilot settings routes answer.
type autopilotSettings struct {
	Entry        json.RawMessage `json:"entry"`
	Version      string          `json:"version"`
	MaxShips     int             `json:"max_ships"`
	ResumeSource bool            `json:"resume_source"`
	ArgsError    string          `json:"args_error"`
	ConfigError  string          `json:"config_error"`
}

// storedArgs is the part of an entry the args check reads.
type storedArgs struct {
	Fleet string   `json:"fleet"`
	Args  []string `json:"args"`
}

func parseStoredArgs(raw json.RawMessage) storedArgs {
	var a storedArgs
	_ = json.Unmarshal(raw, &a)
	return a
}

// argsError is the allowlist's refusal of args against the claude dock of the
// fleet cfg names; empty when they pass or the fleet has no such dock.
func argsError(cfg *config.Config, fleet string, args []string) string {
	f, ok := cfg.Fleet(fleet)
	if !ok {
		return ""
	}
	dock, ok := f.Harness("claude")
	if !ok {
		return ""
	}
	if err := launcher.AutopilotArgs("claude", dock.Args, args); err != nil {
		return err.Error()
	}
	return ""
}

// autopilotSettingsOf is the GET shape for the file as it is now.
func (s *Service) autopilotSettingsOf(key, panel string) (autopilotSettings, error) {
	cfg := s.Config.Current()
	out := autopilotSettings{Version: "none", MaxShips: cfg.AutopilotMaxShips, ResumeSource: cfg.ResumeSource != nil}
	entry, version, err := s.Config.ReadPanelAutopilot(key, panel)
	if errors.Is(err, config.ErrInvalid) {
		out.ConfigError = err.Error()
		if e := s.Config.View().Error; e != nil {
			out.ConfigError = e.Message
		}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Entry, out.Version = entry, version
	if entry != nil {
		a := parseStoredArgs(entry)
		out.ArgsError = argsError(cfg, a.Fleet, a.Args)
	}
	return out, nil
}

// panelParams reads the route's source and panel, answering the request itself
// when they don't name a panel of a source.
func (s *Service) panelParams(w http.ResponseWriter, r *http.Request) (key, panel string, ok bool) {
	key, panel = r.PathValue("key"), r.PathValue("panel")
	if panel == "" || hasControl(panel) {
		writeError(w, http.StatusBadRequest, "panel must be a panel ID")
		return "", "", false
	}
	if _, found := s.Config.Current().Source(key); !found {
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return "", "", false
	}
	return key, panel, true
}

// writeAutopilotError answers a refused write: a file that doesn't load or an
// entry that changed is a conflict, anything else the entry's fault.
func writeAutopilotError(w http.ResponseWriter, key string, err error) {
	switch {
	case errors.Is(err, config.ErrInvalid), errors.Is(err, config.ErrChanged):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, config.ErrNoSource):
		writeError(w, http.StatusNotFound, "source "+key+" not found")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func (s *Service) serveAutopilotSettings(w http.ResponseWriter, r *http.Request) {
	key, panel, ok := s.panelParams(w, r)
	if !ok {
		return
	}
	v, err := s.autopilotSettingsOf(key, panel)
	if err != nil {
		writeAutopilotError(w, key, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, v)
}

// serveSaveAutopilotSettings sets one panel's entry. The args pass the
// autopilot allowlist against the chosen fleet's claude dock, except stored
// args that already fail and are saved unchanged with the fleet (ADR 0022).
func (s *Service) serveSaveAutopilotSettings(w http.ResponseWriter, r *http.Request) {
	key, panel, ok := s.panelParams(w, r)
	if !ok {
		return
	}
	var req struct {
		Entry   json.RawMessage `json:"entry"`
		Version string          `json:"version"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Entry) == 0 || req.Entry[0] != '{' {
		writeError(w, http.StatusBadRequest, "entry must be an object")
		return
	}
	if req.Version == "" {
		writeError(w, http.StatusBadRequest, "version is required")
		return
	}
	next := parseStoredArgs(req.Entry)
	check := func(old json.RawMessage, cfg *config.Config) error {
		if old != nil {
			stored := parseStoredArgs(old)
			if stored.Fleet == next.Fleet && slices.Equal(stored.Args, next.Args) && argsError(cfg, stored.Fleet, stored.Args) != "" {
				return nil
			}
		}
		f, _ := cfg.Fleet(next.Fleet)
		dock, _ := f.Harness("claude")
		return launcher.AutopilotArgs("claude", dock.Args, next.Args)
	}
	s.mu.Lock()
	_, err := s.Config.SetPanelAutopilot(key, panel, req.Entry, req.Version, check)
	if err != nil {
		s.mu.Unlock()
		writeAutopilotError(w, key, err)
		return
	}
	v, verr := s.autopilotSettingsOf(key, panel)
	s.mu.Unlock()
	s.Sup.Reconcile() // pushes the sources and wakes autopilot
	if verr != nil {
		writeAutopilotError(w, key, verr)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// serveRemoveAutopilot removes one panel's entry and its pause.
func (s *Service) serveRemoveAutopilot(w http.ResponseWriter, r *http.Request) {
	key, panel, ok := s.panelParams(w, r)
	if !ok {
		return
	}
	version := r.URL.Query().Get("version")
	if version == "" {
		writeError(w, http.StatusBadRequest, "version is required")
		return
	}
	s.mu.Lock()
	_, err := s.Config.SetPanelAutopilot(key, panel, nil, version, nil)
	if err == nil {
		s.State.SetPaused(key, panel, false)
	}
	s.mu.Unlock()
	if err != nil {
		writeAutopilotError(w, key, err)
		return
	}
	s.Sup.Reconcile()
	s.Sup.changed() // the pause went too, which Reconcile doesn't see
	writeJSON(w, http.StatusOK, map[string]string{"removed": panel})
}
