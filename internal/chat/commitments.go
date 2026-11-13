package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/autopilotgate"
	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ledger"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/textsafe"
)

// maxCommitmentBody leaves room past the ledger's own launch-spec limit, so that limit words the refusal.
const maxCommitmentBody = 64 << 10

func (s *Service) serveCommitmentAdd(w http.ResponseWriter, r *http.Request) {
	fleet, ok := actor.FleetOf(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "only AdmiralBot records commitments here")
		return
	}
	var body struct {
		Text    string
		Trigger ledger.Trigger
		Action  string
		Launch  json.RawMessage
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCommitmentBody))
	if err := dec.Decode(&body); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, `send one JSON object: {"text", "trigger": {"kind", "subject"}, "action", "launch"}`)
		return
	}
	if !s.noteFleetOK(w, fleet) {
		return
	}
	cfg := s.Config()
	f, ok := cfg.Fleet(fleet)
	if !ok {
		writeError(w, http.StatusNotFound, "no fleet "+fleet)
		return
	}
	trigger, msg := s.checkTrigger(cfg, f, body.Trigger)
	if msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	c := ledger.Commitment{Text: body.Text, Trigger: trigger, Action: body.Action}
	switch body.Action {
	case ledger.ActionLaunch:
		spec, lerr := s.checkLaunch(cfg, f, body.Launch)
		if lerr != nil {
			writeError(w, lerr.Status, lerr.Msg)
			return
		}
		c.Launch = spec
	case ledger.ActionRemind:
		if len(body.Launch) > 0 && !bytes.Equal(bytes.TrimSpace(body.Launch), []byte("null")) {
			writeError(w, http.StatusUnprocessableEntity, "a remind commitment carries no launch")
			return
		}
	}
	got, err := s.notes().AddCommitment(fleet, c, actor.Via)
	var refused *ledger.RefusedError
	switch {
	case errors.As(err, &refused):
		writeError(w, http.StatusUnprocessableEntity, refused.Msg)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusCreated, got)
	}
}

// checkTrigger is the trigger as it is stored (the subject canonical, a ship's launch stamp beside its session) and the refusal for one that could never fire, or "".
func (s *Service) checkTrigger(cfg *config.Config, f config.Fleet, t ledger.Trigger) (ledger.Trigger, string) {
	t.Launched = ""
	switch t.Kind {
	case ledger.TriggerShipSailed, ledger.TriggerShipAnchored:
		if s.Ships != nil {
			for _, sh := range s.Ships() {
				if sh.Session == t.Subject && sh.Fleet == f.Key {
					if sh.Launched == "" {
						return t, fmt.Sprintf("ship %q has no launch stamp, so a trigger on it could never fire", t.Subject)
					}
					t.Launched = sh.Launched
					return t, ""
				}
			}
		}
		return t, fmt.Sprintf("no ship %q in fleet %s", t.Subject, f.Name)
	case ledger.TriggerPRMerged, ledger.TriggerPRClosed, ledger.TriggerIssueClosed:
		pull := t.Kind != ledger.TriggerIssueClosed
		ref, err := campaigns.ParseGitHubURL(t.Subject, launcher.GitHubHosts(cfg, f), pull)
		if err != nil {
			return t, err.Error()
		}
		noun := "issues"
		if pull {
			noun = "pull"
		}
		t.Subject = fmt.Sprintf("https://%s/%s/%s/%s/%d", strings.ToLower(ref.Host), ref.Owner, ref.Repo, noun, ref.Number)
	case ledger.TriggerTime:
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(t.Subject))
		switch {
		case err != nil:
			return t, "a time trigger's subject must be an RFC 3339 time such as 2026-11-14T09:00:00Z"
		case !at.After(time.Now()):
			return t, "that time has already passed"
		}
		t.Subject = at.UTC().Format(time.RFC3339)
	case ledger.TriggerNone:
		if t.Subject != "" {
			return t, "a trigger of none has no subject"
		}
	}
	return t, ""
}

// checkLaunch judges a launch spec by the rules the launch itself will apply, short of live capacity, and returns it normalised for the ledger.
func (s *Service) checkLaunch(cfg *config.Config, f config.Fleet, raw json.RawMessage) (json.RawMessage, *launcher.Error) {
	bad := func(format string, a ...any) *launcher.Error {
		return &launcher.Error{Status: http.StatusUnprocessableEntity, Msg: fmt.Sprintf(format, a...)}
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, bad("a launch commitment needs a launch spec")
	}
	var req launcher.Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, bad("the launch spec is not a ship launch body: %v", err)
	}
	if req.Fleet != "" && req.Fleet != f.Key {
		return nil, bad("a commitment launches in its own fleet, not %s", req.Fleet)
	}
	req.Fleet, req.Autopilot, req.Anyway, req.ChatMessage, req.Via = f.Key, true, false, "", actor.Via
	if req.Clone && req.Dir != "" {
		return nil, bad("send dir or clone, not both")
	}
	var orderText, orderRepo string
	if req.Source != "" {
		if req.Source == config.ReservedSourceKey {
			return nil, bad("launch a campaign's child through its campaign")
		}
		if len(req.Issues) > 0 {
			return nil, bad("issues go only on a launch with no source")
		}
		if s.SourceFleet == nil || s.OrderExists == nil {
			return nil, bad("this server cannot check order sources")
		}
		if owner, known := s.SourceFleet(req.Source); !known || owner != f.Key {
			return nil, bad("no order source %q in fleet %s", req.Source, f.Name)
		}
		if !s.OrderExists(req.Source, req.OrderID) {
			return nil, bad("order %q not found in source %s", req.OrderID, req.Source)
		}
		if req.Clone {
			if s.OrderRepo == nil {
				return nil, bad("this server cannot read an order's repo")
			}
			var ok bool
			if orderText, orderRepo, ok = s.OrderRepo(req.Source, req.OrderID); !ok {
				return nil, bad("order %q not found in source %s", req.OrderID, req.Source)
			}
		}
	}
	if req.Clone {
		text := req.Text
		if req.Source != "" {
			text = orderText
		}
		if _, lerr := launcher.CheckClone(cfg, req, orderRepo, text); lerr != nil {
			return nil, lerr
		}
	}
	if _, lerr := ships.CheckIssueURLs(req.Issues, launcher.GitHubHosts(cfg, f)); lerr != nil {
		return nil, lerr
	}
	for _, name := range req.Resources {
		if _, ok := cfg.Resources[name]; !ok {
			return nil, bad("no resource named %q", name)
		}
	}
	for _, r := range req.Text + req.Note {
		if textsafe.Hidden(r) && r != '\n' && r != '\t' {
			return nil, bad("the launch prompt holds a control or invisible character (U+%04X); write it again in plain text", r)
		}
	}
	chk, lerr := launcher.CheckSpec(cfg, s.Home, req)
	if lerr != nil {
		if lerr.Status >= 500 || lerr.Status < 400 {
			lerr.Status = http.StatusUnprocessableEntity
		}
		return nil, lerr
	}
	// Check would post an Inbox notice; a refusal at record time only answers the chat.
	if !autopilotgate.Proven(s.Home, f.Key, req.Harness) {
		return nil, bad("autopilot is not proven on %s in fleet %s: its guard has not passed", req.Harness, f.Name)
	}
	if ok, reason := autopilotgate.Advisor(f, capabilities.Open(s.Home)); !ok {
		return nil, bad("autopilot is not proven on %s in fleet %s: its advisor is not proven (%s)", req.Harness, f.Name, reason)
	}
	spec, err := json.Marshal(chk.Req)
	if err != nil {
		return nil, bad("the launch spec cannot be stored: %v", err)
	}
	return spec, nil
}

func (s *Service) serveCommitments(w http.ResponseWriter, r *http.Request) {
	fleet, ok := s.noteFleet(w, r)
	if !ok {
		return
	}
	list, err := s.notes().Commitments(fleet)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"commitments": list})
}

func (s *Service) serveCommitmentDelete(w http.ResponseWriter, r *http.Request) {
	fleet, ok := s.noteFleet(w, r)
	if !ok {
		return
	}
	err := s.notes().CancelCommitment(fleet, r.PathValue("id"))
	switch {
	case errors.Is(err, ledger.ErrNotFound):
		writeError(w, http.StatusNotFound, "no open commitment with that id")
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
