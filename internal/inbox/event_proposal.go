package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// KindEventProposal lives only in the AdmiralSession directory: an Ask first outside event, to launch, dismiss or mute before it expires.
const KindEventProposal = "event_proposal"

// EventProposal is the item's event_proposal object. It holds no event text: AdmiralBot reads every item field.
type EventProposal struct {
	Fleet       string    `json:"fleet"`
	Routine     string    `json:"routine"`
	RoutineName string    `json:"routine_name"`
	Trigger     string    `json:"trigger"`
	Tier        string    `json:"tier"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// EventDetail is what Peek shows of the event, read from the detail route that the actor guard denies.
type EventDetail struct {
	Summary string `json:"summary"`
	Title   string `json:"title"`
	Sender  string `json:"sender"`
	// DedupeKey is the Lookout's validated key, the value a Mute rule by dedupe_key names.
	DedupeKey string `json:"dedupe_key"`
	Link      string `json:"link"`
	// Metadata is true when Summary is the metadata line, not a Lookout summary.
	Metadata bool `json:"metadata"`
	// MuteBy lists the Mute rule choices: sender and dedupe_key, each only when the event has one.
	MuteBy []string `json:"mute_by"`
}

// LaunchEventFunc launches an event proposal's routine. status is 0 on success, else the refusal's HTTP status with msg.
type LaunchEventFunc func(ctx context.Context, p EventProposal) (ship any, status int, msg string)

// eventDetailDir holds each proposal's detail outside inbox/<session>, which AdmiralBot can read.
func eventDetailDir(home string) string { return filepath.Join(home, "inbox-events") }

// PublishEventProposal writes the item event-<trigger> and keeps the routine's other open proposals. The event's text
// goes to a separate file that only the detail route reads.
func (s *Service) PublishEventProposal(p EventProposal, d EventDetail) (string, error) {
	if p.Fleet == "" || p.Routine == "" || p.Trigger == "" || p.ExpiresAt.IsZero() {
		return "", fmt.Errorf("inbox: an event proposal needs a fleet, a routine, a trigger and an expiry")
	}
	id := "event-" + p.Trigger
	if !idPattern.MatchString(id) {
		return "", fmt.Errorf("inbox: %q is not an item id", id)
	}
	if err := writeEventDetail(s.Home, id, d); err != nil {
		return "", err
	}
	err := publishAdmiralItem(s.Home, id, map[string]any{
		"kind":            KindEventProposal,
		"title":           "Run " + p.RoutineName + "?",
		KindEventProposal: p,
	})
	if err != nil {
		return "", err
	}
	s.Refresh()
	return id, nil
}

func writeEventDetail(home, id string, d EventDetail) error {
	dir := eventDetailDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".detail-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, id+".json"))
}

// EventProposal is the proposal of an event_proposal item.
func (it Item) EventProposal() (EventProposal, bool) {
	var p EventProposal
	return p, json.Unmarshal(it.fields[KindEventProposal], &p) == nil && p.Fleet != "" && p.Routine != "" && p.Trigger != "" && !p.ExpiresAt.IsZero()
}

func (p EventProposal) expired(now time.Time) bool { return !now.Before(p.ExpiresAt) }

// LaunchEventItem runs the routine an event proposal asks for and answers the item. The hook runs under the answer lock,
// so a second press finds the item answered; a refused launch, or one after the proposal expired, leaves it unanswered.
func (s *Service) LaunchEventItem(ctx context.Context, id string) (any, error) {
	if s.LaunchEvent == nil {
		return nil, &refusedError{http.StatusNotImplemented, "launching an event proposal is not wired"}
	}
	var ship any
	err := s.writeAnswered(AdmiralSession, id, KindEventProposal, func(it Item) (any, []outcomes.Record, error) {
		p, _ := it.EventProposal()
		if p.expired(time.Now()) {
			return nil, nil, &refusedError{http.StatusGone, "the proposal expired"}
		}
		got, status, msg := s.LaunchEvent(ctx, p)
		if status != 0 {
			return nil, nil, &refusedError{status, msg}
		}
		ship = got
		file := map[string]any{"version": 1, "id": id, "kind": KindEventProposal, "answered": time.Now().UTC().Format(time.RFC3339), "launched": true}
		return file, []outcomes.Record{{Event: "proposal_launched", Fields: map[string]any{"item": id, "routine": p.Routine, "fleet": p.Fleet}}}, nil
	})
	if err != nil {
		return nil, err
	}
	removeEventDetail(s.Home, id)
	s.Refresh()
	return ship, nil
}

// DismissEventProposal answers an event proposal with a dismissal.
func (s *Service) DismissEventProposal(id string) error {
	err := s.writeAnswered(AdmiralSession, id, KindEventProposal, func(it Item) (any, []outcomes.Record, error) {
		p, _ := it.EventProposal()
		file := map[string]any{"version": 1, "id": id, "kind": KindEventProposal, "answered": time.Now().UTC().Format(time.RFC3339), "dismissed": true}
		return file, []outcomes.Record{{Event: "proposal_dismissed", Fields: map[string]any{"item": id, "routine": p.Routine, "fleet": p.Fleet}}}, nil
	})
	if err == nil {
		removeEventDetail(s.Home, id)
		s.Refresh()
	}
	return err
}

func removeEventDetail(home, id string) {
	_ = os.Remove(filepath.Join(eventDetailDir(home), id+".json"))
}

// isEventProposal says whether item id, answered or not, is an event proposal, which the launch and dismiss routes dispatch on.
func (s *Service) isEventProposal(id string) bool {
	if !idPattern.MatchString(id) {
		return false
	}
	dir, err := openShipDir(s.Home, AdmiralSession)
	if err != nil {
		return false
	}
	defer dir.Close()
	it, err := readItem(dir, AdmiralSession, id)
	return err == nil && it.Kind == KindEventProposal
}

func (s *Service) serveEventDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idPattern.MatchString(id) || !s.isEventProposal(id) {
		writeError(w, http.StatusNotFound, "no event proposal "+id)
		return
	}
	b, err := os.ReadFile(filepath.Join(eventDetailDir(s.Home), id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusGone, "the event's detail is gone")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "the event's detail could not be read")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}
