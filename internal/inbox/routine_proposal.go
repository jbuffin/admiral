package inbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// KindRoutineProposal lives only in the AdmiralSession directory: an Ask first run of a routine, to launch or dismiss before it expires.
const KindRoutineProposal = "routine_proposal"

// RoutineProposal is the item's routine_proposal object.
type RoutineProposal struct {
	Fleet       string    `json:"fleet"`
	Routine     string    `json:"routine"`
	RoutineName string    `json:"routine_name"`
	Trigger     string    `json:"trigger"`
	Action      string    `json:"action"`
	Reason      string    `json:"reason,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// LaunchRoutineFunc runs the proposal's routine once. status is 0 on success, else the refusal's HTTP status with msg.
type LaunchRoutineFunc func(ctx context.Context, p RoutineProposal) (ship any, status int, msg string)

// PublishRoutineProposal writes the item routine-<trigger> once and closes the routine's earlier open one.
func (s *Service) PublishRoutineProposal(p RoutineProposal) (string, error) {
	if p.Fleet == "" || p.Routine == "" || p.Trigger == "" || p.ExpiresAt.IsZero() {
		return "", fmt.Errorf("inbox: a routine proposal needs a fleet, a routine, a trigger and an expiry")
	}
	// the trigger id is bounded and unique; a fleet key is neither, and an id is at most 64 characters
	id := "routine-" + p.Trigger
	for _, it := range scan(s.Home) {
		if old, ok := it.RoutineProposal(); ok && it.Session == AdmiralSession && it.ID != id && old.Fleet == p.Fleet && old.Routine == p.Routine {
			s.closeRoutineProposal(it.ID)
		}
	}
	err := publishAdmiralItem(s.Home, id, map[string]any{
		"kind":              KindRoutineProposal,
		"title":             "Run " + p.RoutineName + "?",
		KindRoutineProposal: p,
	})
	if err != nil {
		return "", err
	}
	s.Refresh()
	return id, nil
}

// closeRoutineProposal answers a proposal that a newer run replaced; one already answered is left as it is.
func (s *Service) closeRoutineProposal(id string) {
	_ = s.writeAnswered(AdmiralSession, id, KindRoutineProposal, func(Item) (any, []outcomes.Record, error) {
		return map[string]any{"version": 1, "id": id, "kind": KindRoutineProposal, "answered": time.Now().UTC().Format(time.RFC3339), "expired": true}, nil, nil
	})
}

// RoutineProposal is the proposal of a routine_proposal item.
func (it Item) RoutineProposal() (RoutineProposal, bool) {
	var p RoutineProposal
	return p, json.Unmarshal(it.fields[KindRoutineProposal], &p) == nil && p.Fleet != "" && p.Routine != "" && p.Trigger != "" && !p.ExpiresAt.IsZero()
}

func (p RoutineProposal) expired(now time.Time) bool { return !now.Before(p.ExpiresAt) }

// LaunchRoutineItem runs the routine a proposal asks for and answers the item. The hook runs under the answer lock,
// so a second press finds the item answered; a refused launch, or one after the proposal expired, leaves it unanswered.
func (s *Service) LaunchRoutineItem(ctx context.Context, id string) (any, error) {
	return s.LaunchRoutineItemGuarded(ctx, id, nil)
}

// LaunchRoutineItemGuarded is LaunchRoutineItem for a caller with a Guard, checked under the lock before the hook runs.
func (s *Service) LaunchRoutineItemGuarded(ctx context.Context, id string, g *Guard) (any, error) {
	if s.LaunchRoutine == nil {
		return nil, &refusedError{http.StatusNotImplemented, "launching a routine proposal is not wired"}
	}
	var ship any
	err := s.writeAnswered(AdmiralSession, id, KindRoutineProposal, func(it Item) (any, []outcomes.Record, error) {
		if err := g.check(it); err != nil {
			return nil, nil, err
		}
		p, _ := it.RoutineProposal()
		if p.expired(s.now()) {
			return nil, nil, &refusedError{http.StatusGone, "the proposal expired"}
		}
		got, status, msg := s.LaunchRoutine(ctx, p)
		if status != 0 {
			return nil, nil, &refusedError{status, msg}
		}
		ship = got
		file := map[string]any{"version": 1, "id": id, "kind": KindRoutineProposal, "answered": time.Now().UTC().Format(time.RFC3339), "launched": true}
		return file, []outcomes.Record{{Event: "proposal_launched", Fields: map[string]any{"item": id, "routine": p.Routine, "fleet": p.Fleet}}}, nil
	})
	if err != nil {
		return nil, err
	}
	s.settled(AdmiralSession, id, g)
	s.Refresh()
	return ship, nil
}

// DismissRoutineProposal answers a routine proposal with a dismissal.
func (s *Service) DismissRoutineProposal(id string) error {
	return s.DismissRoutineProposalGuarded(id, nil)
}

// DismissRoutineProposalGuarded is DismissRoutineProposal for a caller with a Guard.
func (s *Service) DismissRoutineProposalGuarded(id string, g *Guard) error {
	var dismissed RoutineProposal
	err := s.writeAnswered(AdmiralSession, id, KindRoutineProposal, func(it Item) (any, []outcomes.Record, error) {
		if err := g.check(it); err != nil {
			return nil, nil, err
		}
		p, _ := it.RoutineProposal()
		dismissed = p
		file := map[string]any{"version": 1, "id": id, "kind": KindRoutineProposal, "answered": time.Now().UTC().Format(time.RFC3339), "dismissed": true}
		return file, []outcomes.Record{{Event: "proposal_dismissed", Fields: map[string]any{"item": id, "routine": p.Routine, "fleet": p.Fleet}}}, nil
	})
	if err == nil {
		s.settled(AdmiralSession, id, g)
		if s.DismissRoutine != nil {
			s.DismissRoutine(dismissed)
		}
		s.Refresh()
	}
	return err
}

// isRoutineProposal says whether item id, answered or not, is a routine proposal, which the launch and dismiss routes dispatch on.
func (s *Service) isRoutineProposal(id string) bool { return s.isAdmiralKind(id, KindRoutineProposal) }

func (s *Service) isAdmiralKind(id, kind string) bool {
	if !idPattern.MatchString(id) {
		return false
	}
	dir, err := openShipDir(s.Home, AdmiralSession)
	if err != nil {
		return false
	}
	defer dir.Close()
	it, err := readItem(dir, AdmiralSession, id)
	return err == nil && it.Kind == kind
}

func (s *Service) fleetOf(key string) (FleetInfo, bool) {
	if s.Fleet == nil {
		return FleetInfo{}, false
	}
	return s.Fleet(key)
}

// MovedOn says whether a routine proposal closed without the engineer: it expired, or the routine's next run replaced it.
func (s *Service) MovedOn(session, id string) bool {
	if session != AdmiralSession || !idPattern.MatchString(id) {
		return false
	}
	// A launch holds answerMu across its hook, so waiting here shows its answer file instead of an expiry it crossed.
	s.answerMu.Lock()
	defer s.answerMu.Unlock()
	dir, err := openShipDir(s.Home, session)
	if err != nil {
		return false
	}
	defer dir.Close()
	it, err := readItem(dir, session, id)
	if err != nil {
		return false
	}
	p, ok := it.RoutineProposal()
	if !ok {
		return false
	}
	if !Answered(dir, id) {
		return p.expired(s.now())
	}
	f, err := dir.OpenFile(answerName(id), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	var a struct {
		Expired bool `json:"expired"`
	}
	b, err := io.ReadAll(io.LimitReader(f, maxItem))
	return err == nil && json.Unmarshal(b, &a) == nil && a.Expired
}
