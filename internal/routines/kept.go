package routines

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"slices"
	"sync"
	"time"
)

const (
	keptDraftTTL     = time.Hour
	maxKeptPerFleet  = 50
	keptDraftIDBytes = 16
)

type keptDraft struct {
	id        string
	draft     routineDraft
	problems  []problem
	expiresAt time.Time
}

// keptDrafts holds routine drafts in memory only: a restart drops them, as an expiry does.
type keptDrafts struct {
	mu      sync.Mutex
	byFleet map[string][]keptDraft
}

func (k *keptDrafts) keep(fleet string, now time.Time, d routineDraft, problems []problem) (keptDraft, error) {
	var raw [keptDraftIDBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return keptDraft{}, err
	}
	kd := keptDraft{id: base64.RawURLEncoding.EncodeToString(raw[:]), draft: d, problems: problems, expiresAt: now.Add(keptDraftTTL)}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.byFleet == nil {
		k.byFleet = map[string][]keptDraft{}
	}
	for f, list := range k.byFleet {
		list = slices.DeleteFunc(list, func(x keptDraft) bool { return !now.Before(x.expiresAt) })
		if len(list) == 0 {
			delete(k.byFleet, f)
		} else {
			k.byFleet[f] = list
		}
	}
	list := append(k.byFleet[fleet], kd)
	if len(list) > maxKeptPerFleet {
		list = list[len(list)-maxKeptPerFleet:]
	}
	k.byFleet[fleet] = list
	return kd, nil
}

func (k *keptDrafts) get(fleet, id string, now time.Time) (keptDraft, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, x := range k.byFleet[fleet] {
		if x.id == id && now.Before(x.expiresAt) {
			return x, true
		}
	}
	return keptDraft{}, false
}

func (s *Service) serveKeptDraft(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if _, err := s.Store.fleet(key); err != nil {
		fail(w, err)
		return
	}
	kd, ok := s.drafts.get(key, r.PathValue("id"), s.Store.now())
	if !ok {
		writeError(w, http.StatusNotFound, "no such draft")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": kd.id, "draft": kd.draft, "problems": kd.problems,
		"expires_at": kd.expiresAt.UTC().Format(time.RFC3339)})
}
