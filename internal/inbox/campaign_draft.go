package inbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/outcomes"
)

// KindCampaignDraft lives only in the AdmiralSession directory: a campaign AdmiralBot drafted, for the engineer to create or dismiss. It never expires.
const KindCampaignDraft = "campaign_draft"

const (
	maxDraftTitle = 100
	maxDraftBody  = 64 << 10
)

// CampaignDraft is the item's campaign_draft object. Via says who drafted it.
type CampaignDraft struct {
	Fleet string `json:"fleet"`
	Repo  string `json:"repo"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Via   string `json:"via"`
}

// CreatedCampaign is the campaign a draft became.
type CreatedCampaign struct {
	ID  string
	URL string
}

// CreateCampaignFunc files the draft as a campaign. status is 0 on success, else the refusal's HTTP status with msg.
// A non-empty URL with a status means the parent issue exists and msg says what failed after it.
type CreateCampaignFunc func(ctx context.Context, d CampaignDraft) (created CreatedCampaign, status int, msg string)

// CampaignCard is the outcome Create or Dismiss posts to the fleet's chat. ID is the item's; a dismissed draft has no URL.
// Failed says what went wrong after the parent issue was filed.
type CampaignCard struct {
	ID      string
	Title   string
	URL     string
	Created bool
	Failed  string
}

// CampaignDraftRequest is what the engineer sends to Create: the draft as they edited it.
type CampaignDraftRequest struct {
	Repo  string `json:"repo"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// CampaignDraft is the draft of a campaign_draft item.
func (it Item) CampaignDraft() (CampaignDraft, bool) {
	var d CampaignDraft
	return d, json.Unmarshal(it.fields[KindCampaignDraft], &d) == nil && d.Fleet != "" && d.Repo != "" && d.Title != "" && d.Via != ""
}

// validDraftText says what is wrong with a draft's repo, title or body, or "" when both are fine.
func validDraftText(repo, title, body string) string {
	switch {
	case strings.TrimSpace(repo) == "":
		return "repo: write owner/name"
	case strings.TrimSpace(title) == "":
		return "title: write a title for the campaign"
	case utf8.RuneCountInString(title) > maxDraftTitle:
		return fmt.Sprintf("title: at most %d characters", maxDraftTitle)
	case strings.TrimSpace(body) == "":
		return "body: write what the campaign is for"
	case len(body) > maxDraftBody:
		return fmt.Sprintf("body: at most %d bytes", maxDraftBody)
	}
	return ""
}

// fleetRepo is repo as the fleet spells it, if the fleet may file a campaign there.
func (s *Service) fleetRepo(fleet, repo string) (string, bool) {
	if s.FleetRepos == nil {
		return "", false
	}
	repo = strings.TrimSpace(repo)
	for _, r := range s.FleetRepos(fleet) {
		if strings.EqualFold(r, repo) {
			return r, true
		}
	}
	return "", false
}

// serveCampaignDraft is for AdmiralBot only. It writes an item and nothing else: no GitHub write, no model call.
func (s *Service) serveCampaignDraft(w http.ResponseWriter, r *http.Request) {
	fleet, ok := actor.FleetOf(r.Context())
	if !ok || fleet != r.PathValue("fleet") {
		writeError(w, http.StatusForbidden, "only AdmiralBot drafts a campaign, and only for its own fleet")
		return
	}
	var req CampaignDraftRequest
	if !decode(w, r, 256<<10, &req) {
		return
	}
	if msg := validDraftText(req.Repo, req.Title, req.Body); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	repo, ok := s.fleetRepo(fleet, req.Repo)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("%s is not one of fleet %s's repos (%s)", strings.TrimSpace(req.Repo), fleet, strings.Join(s.reposOf(fleet), ", ")))
		return
	}
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	id := "campaign-draft-" + hex.EncodeToString(rnd[:])
	d := CampaignDraft{Fleet: fleet, Repo: repo, Title: strings.TrimSpace(req.Title), Body: req.Body, Via: actor.Via}
	if err := publishAdmiralItem(s.Home, id, map[string]any{"kind": KindCampaignDraft, "title": "AdmiralBot drafted a campaign", KindCampaignDraft: d}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the draft: "+err.Error())
		return
	}
	s.Refresh()
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Service) reposOf(fleet string) []string {
	if s.FleetRepos == nil {
		return nil
	}
	return s.FleetRepos(fleet)
}

// partialCreateError is a Create that failed after the parent issue was filed: the item is answered with the parent.
type partialCreateError struct {
	status int
	msg    string
	parent string
}

func (e *partialCreateError) Error() string { return e.msg }

// CreateCampaignItem files the draft with the engineer's edits and answers the item with the parent's URL. The hook runs
// outside answerMu, and an item has one action at a time, so a second press gets ErrBusy and never files a second parent.
// A refusal before any GitHub write leaves the item unanswered.
func (s *Service) CreateCampaignItem(ctx context.Context, id string, req CampaignDraftRequest) (CreatedCampaign, error) {
	if s.CreateCampaign == nil {
		return CreatedCampaign{}, &refusedError{http.StatusNotImplemented, "creating a campaign from a draft is not wired"}
	}
	it, err := s.open(AdmiralSession, id)
	if err != nil {
		return CreatedCampaign{}, err
	}
	if it.Kind != KindCampaignDraft {
		return CreatedCampaign{}, invalid("item %s is a %s, not a campaign draft", id, it.Kind)
	}
	draft, _ := it.CampaignDraft()
	if msg := validDraftText(req.Repo, req.Title, req.Body); msg != "" {
		return CreatedCampaign{}, &refusedError{http.StatusBadRequest, msg}
	}
	repo, ok := s.fleetRepo(draft.Fleet, req.Repo)
	if !ok {
		return CreatedCampaign{}, &refusedError{http.StatusUnprocessableEntity, fmt.Sprintf("%s is not one of fleet %s's repos", strings.TrimSpace(req.Repo), draft.Fleet)}
	}
	draft.Repo, draft.Title, draft.Body = repo, strings.TrimSpace(req.Title), req.Body

	release, err := s.beginAction(AdmiralSession, id)
	if err != nil {
		return CreatedCampaign{}, err
	}
	defer release()

	// A parent filed by an earlier press whose answer didn't save is answered again, never filed twice.
	created, status, msg := s.filedParent(id)
	if created.URL == "" {
		created, status, msg = s.CreateCampaign(ctx, draft)
	}
	if status != 0 && created.URL == "" {
		return CreatedCampaign{}, &refusedError{status, msg}
	}
	s.rememberParent(id, created, status, msg)
	card := CampaignCard{ID: id, Title: draft.Title, URL: created.URL, Created: true}
	if status != 0 {
		card.Failed = msg
	}
	werr := s.writeAnswered(AdmiralSession, id, KindCampaignDraft, func(Item) (any, []outcomes.Record, error) {
		file := map[string]any{"version": 1, "id": id, "kind": KindCampaignDraft, "answered": time.Now().UTC().Format(time.RFC3339), "created": true, "url": created.URL}
		if status != 0 {
			file["failed"] = msg
		}
		return file, []outcomes.Record{{Event: "campaign_draft_created", Fields: map[string]any{"item": id, "fleet": draft.Fleet}}}, nil
	})
	if werr == nil || errors.Is(werr, ErrAnswered) {
		s.forgetParent(id)
	}
	// The campaign exists whether or not the answer was written, so the chat hears of it either way.
	if s.PostCampaignCard != nil {
		s.PostCampaignCard(draft.Fleet, card)
	}
	if werr != nil && !errors.Is(werr, ErrAnswered) {
		return CreatedCampaign{}, fmt.Errorf("filed %s but could not answer the item: %w", created.URL, werr)
	}
	s.Refresh()
	if status != 0 {
		return created, &partialCreateError{status, msg, created.URL}
	}
	return created, nil
}

// DismissCampaignDraft answers the item with a dismissal and posts the thrown-away card.
func (s *Service) DismissCampaignDraft(id string) error {
	release, err := s.beginAction(AdmiralSession, id)
	if err != nil {
		return err
	}
	defer release()
	var dismissed CampaignDraft
	err = s.writeAnswered(AdmiralSession, id, KindCampaignDraft, func(it Item) (any, []outcomes.Record, error) {
		dismissed, _ = it.CampaignDraft()
		file := map[string]any{"version": 1, "id": id, "kind": KindCampaignDraft, "answered": time.Now().UTC().Format(time.RFC3339), "dismissed": true}
		return file, []outcomes.Record{{Event: "campaign_draft_dismissed", Fields: map[string]any{"item": id, "fleet": dismissed.Fleet}}}, nil
	})
	if err != nil {
		return err
	}
	if s.PostCampaignCard != nil {
		s.PostCampaignCard(dismissed.Fleet, CampaignCard{ID: id, Title: dismissed.Title})
	}
	s.Refresh()
	return nil
}

func (s *Service) serveCreateCampaign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req CampaignDraftRequest
	if !decode(w, r, 256<<10, &req) {
		return
	}
	created, err := s.CreateCampaignItem(r.Context(), id, req)
	var partial *partialCreateError
	if errors.As(err, &partial) {
		writeJSON(w, partial.status, map[string]any{"error": partial.msg, "parent": partial.parent, "answered": id})
		return
	}
	if err != nil {
		respondProposal(w, id, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"campaign": map[string]string{"id": created.ID, "url": created.URL}})
}

// isCampaignDraft says whether item id, answered or not, is a campaign draft, which the dismiss route dispatches on.
func (s *Service) isCampaignDraft(id string) bool { return s.isAdmiralKind(id, KindCampaignDraft) }

type filedCampaign struct {
	created CreatedCampaign
	status  int
	msg     string
}

func (s *Service) filedParent(id string) (CreatedCampaign, int, string) {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	f := s.filed[id]
	return f.created, f.status, f.msg
}

// rememberParent keeps a filed parent in memory until its answer is written.
func (s *Service) rememberParent(id string, created CreatedCampaign, status int, msg string) {
	if created.URL == "" {
		return
	}
	s.actMu.Lock()
	defer s.actMu.Unlock()
	if s.filed == nil {
		s.filed = map[string]filedCampaign{}
	}
	s.filed[id] = filedCampaign{created, status, msg}
}

func (s *Service) forgetParent(id string) {
	s.actMu.Lock()
	delete(s.filed, id)
	s.actMu.Unlock()
}
