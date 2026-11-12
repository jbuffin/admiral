package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// KindWakeProposal lives only in the AdmiralSession directory: a stranger's feedback on a moored ship's PR, to launch or dismiss.
const KindWakeProposal = "wake_proposal"

// WakeProposalEvent is one event of a stranger's batch. Body is plain text, never rendered.
type WakeProposalEvent struct {
	ID   string    `json:"id"`
	Kind string    `json:"kind"`
	URL  string    `json:"url,omitempty"`
	At   time.Time `json:"at"`
	Body string    `json:"body,omitempty"`
}

// WakeProposal is the item's wake_proposal object. Ship is the moored ship's name and Session its session, which wake takes.
type WakeProposal struct {
	Ship    string              `json:"ship"`
	Session string              `json:"session"`
	PR      string              `json:"pr"`
	Login   string              `json:"login"`
	Events  []WakeProposalEvent `json:"events"`
}

// LaunchProposalFunc starts the feedback round for p. status is 0 on success, else the refusal's HTTP status with msg.
type LaunchProposalFunc func(ctx context.Context, p WakeProposal) (ship any, status int, msg string)

// WriteWakeProposal writes the item once: an item or answer already there for id is left as it is.
func WriteWakeProposal(home, id string, p WakeProposal) error {
	return publishAdmiralItem(home, id, map[string]any{
		"kind":           KindWakeProposal,
		"title":          "Wake " + p.Ship + " for @" + p.Login + "'s review?",
		KindWakeProposal: p,
	})
}

// refusedError is the launch hook's refusal, with the status the route answers.
type refusedError struct {
	status int
	msg    string
}

func (e *refusedError) Error() string { return e.msg }

var errStaleEvents = &refusedError{http.StatusConflict, "the item's events are not the ones the request names"}

// LaunchProposalItem starts the round a proposal asks for and answers the item. The hook runs under the answer lock,
// so a second press finds the item answered; a refused launch leaves the item to launch later. shown, when set, must be the item's event IDs.
func (s *Service) LaunchProposalItem(ctx context.Context, id string, shown []string) (any, error) {
	if s.LaunchProposal == nil {
		return nil, &refusedError{http.StatusNotImplemented, "launching a proposal is not wired"}
	}
	var ship any
	err := s.writeAnswered(AdmiralSession, id, KindWakeProposal, func(it Item) (any, []outcomes.Record, error) {
		p, _ := it.WakeProposal()
		if shown != nil {
			var have []string
			for _, e := range p.Events {
				have = append(have, e.ID)
			}
			slices.Sort(have)
			want := slices.Clone(shown)
			slices.Sort(want)
			if !slices.Equal(have, want) {
				return nil, nil, errStaleEvents
			}
		}
		got, status, msg := s.LaunchProposal(ctx, p)
		if status != 0 {
			return nil, nil, &refusedError{status, msg}
		}
		ship = got
		file := map[string]any{"version": 1, "id": id, "kind": KindWakeProposal, "answered": time.Now().UTC().Format(time.RFC3339), "launched": true, "ship": p.Ship}
		return file, []outcomes.Record{{Event: "proposal_launched", Ship: p.Session, Fields: map[string]any{"item": id, "pr": p.PR}}}, nil
	})
	if err != nil {
		return nil, err
	}
	s.Refresh()
	return ship, nil
}

// DismissProposal answers a proposal with a dismissal. Its event IDs stay recorded in the ship's wake state, so the comment never comes back.
func (s *Service) DismissProposal(id string) error {
	err := s.writeAnswered(AdmiralSession, id, KindWakeProposal, func(it Item) (any, []outcomes.Record, error) {
		p, _ := it.WakeProposal()
		file := map[string]any{"version": 1, "id": id, "kind": KindWakeProposal, "answered": time.Now().UTC().Format(time.RFC3339), "dismissed": true}
		return file, []outcomes.Record{{Event: "proposal_dismissed", Ship: p.Session, Fields: map[string]any{"item": id, "pr": p.PR}}}, nil
	})
	if err == nil {
		s.Refresh()
	}
	return err
}

func (s *Service) serveLaunchProposal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Events []string `json:"events"`
	}
	if !decode(w, r, 1<<20, &req) {
		return
	}
	ship, err := s.LaunchProposalItem(r.Context(), id, req.Events)
	if err != nil {
		respondProposal(w, id, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ship": ship})
}

func (s *Service) serveDismissProposal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct{}
	if !decode(w, r, 1<<20, &req) {
		return
	}
	if err := s.DismissProposal(id); err != nil {
		respondProposal(w, id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func respondProposal(w http.ResponseWriter, id string, err error) {
	var refused *refusedError
	if errors.As(err, &refused) {
		writeError(w, refused.status, refused.msg)
		return
	}
	respond(w, AdmiralSession, id, ReviewResult{}, err)
}

// WakeProposal is the proposal of a wake_proposal item.
func (it Item) WakeProposal() (WakeProposal, bool) {
	var p WakeProposal
	return p, json.Unmarshal(it.fields[KindWakeProposal], &p) == nil && p.Ship != ""
}

// RemoveWakeProposals deletes every proposal for ship, answered or not, with its answer file.
func RemoveWakeProposals(home, ship string) error {
	dir, err := os.OpenRoot(filepath.Join(home, "inbox", AdmiralSession))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer dir.Close()
	names, err := listDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		id, ok := strings.CutSuffix(name, ".json")
		if !ok || !strings.HasPrefix(id, "wake-") || strings.HasSuffix(id, ".answer") || !idPattern.MatchString(id) {
			continue
		}
		f, err := dir.Open(name)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(f, maxItem))
		f.Close()
		var item struct {
			Kind     string `json:"kind"`
			Proposal struct {
				Ship string `json:"ship"`
			} `json:"wake_proposal"`
		}
		if json.Unmarshal(b, &item) != nil || item.Kind != KindWakeProposal || item.Proposal.Ship != ship {
			continue
		}
		for _, n := range []string{name, answerName(id)} {
			if err := dir.Remove(n); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
