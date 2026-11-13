package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/autopilotgate"
	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/ledger"
)

// serveCampaignCommitment records the one commitment that works a followed campaign for AdmiralBot.
func (s *Service) serveCampaignCommitment(w http.ResponseWriter, r *http.Request) {
	fleet, ok := actor.FleetOf(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "only AdmiralBot records commitments here")
		return
	}
	var body struct{ Reference, Harness string }
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCommitmentBody))
	if err := dec.Decode(&body); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, `send one JSON object: {"reference", "harness"}`)
		return
	}
	if s.FollowCampaign == nil || s.CampaignFleet == nil {
		writeError(w, http.StatusServiceUnavailable, "this server cannot follow campaigns")
		return
	}
	if !s.noteFleetOK(w, fleet) {
		return
	}
	f, ok := s.Config().Fleet(fleet)
	if !ok {
		writeError(w, http.StatusNotFound, "no fleet "+fleet)
		return
	}
	harness := strings.TrimSpace(body.Harness)
	if harness == "" {
		harness = "claude"
	}
	owner, lerr := s.CampaignFleet(body.Reference)
	if lerr != nil {
		writeError(w, lerr.Status, lerr.Msg)
		return
	}
	if owner != fleet {
		writeError(w, http.StatusForbidden, "that campaign belongs to another fleet")
		return
	}
	// A refusal at record time only answers the chat; the gate's own notice would post to the Inbox.
	if !autopilotgate.Proven(s.Home, f.Key, harness) {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("autopilot is not proven on %s in fleet %s: its guard has not passed", harness, f.Name))
		return
	}
	if ok, reason := autopilotgate.Advisor(f, capabilities.Open(s.Home)); !ok {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("autopilot is not proven on %s in fleet %s: its advisor is not proven (%s)", harness, f.Name, reason))
		return
	}
	followed, _, lerr := s.FollowCampaign(r.Context(), body.Reference)
	if lerr != nil {
		writeError(w, lerr.Status, lerr.Msg)
		return
	}
	if followed.Fleet != fleet {
		writeError(w, http.StatusForbidden, "that campaign belongs to another fleet")
		return
	}
	name := strings.TrimSpace(body.Reference)
	if ref := campaignRef(followed.URL); ref != "" {
		name = ref
	}
	c := ledger.Commitment{Text: "work on " + name, Trigger: ledger.Trigger{Kind: ledger.TriggerCampaign, Subject: followed.ID}, Action: ledger.ActionWork, Harness: harness}
	got, created, err := s.notes().AddCampaignCommitment(fleet, c, actor.Via)
	var refused *ledger.RefusedError
	switch {
	case errors.As(err, &refused):
		writeError(w, http.StatusUnprocessableEntity, refused.Msg)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	case created:
		writeJSON(w, http.StatusCreated, got)
	default:
		writeJSON(w, http.StatusOK, got)
	}
}

// campaignRef is owner/repo#n for a GitHub issue URL, "" for anything else.
func campaignRef(url string) string {
	parts := strings.Split(strings.TrimRight(url, "/"), "/")
	if n := len(parts); n >= 5 && parts[n-2] == "issues" {
		return parts[n-4] + "/" + parts[n-3] + "#" + parts[n-1]
	}
	return ""
}
