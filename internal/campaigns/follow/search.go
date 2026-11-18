package follow

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/github"
)

type searchResult struct {
	Title     string       `json:"title"`
	Reference string       `json:"reference"`
	URL       string       `json:"url"`
	State     string       `json:"state"`
	Parent    bool         `json:"parent"`
	Progress  progressView `json:"progress"`
	Status    string       `json:"status"` // followed, unfollowed, ended, or empty for a campaign not followed
}

var bareNumber = regexp.MustCompile(`^#(\d{1,9})$`)

// serveSearch finds issues in the fleet's repos and owners for the Add campaign modal.
func (s *Service) serveSearch(w http.ResponseWriter, r *http.Request) {
	fleet := r.PathValue("key")
	if s.FleetScope == nil {
		writeError(w, http.StatusNotFound, "no such fleet")
		return
	}
	host, claims, ok := s.FleetScope(fleet)
	if !ok {
		writeError(w, http.StatusNotFound, "no such fleet "+fleet)
		return
	}
	rows, err := s.search(r.Context(), fleet, host, claims, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": rows})
}

func (s *Service) search(ctx context.Context, fleet, host string, claims github.FleetClaims, text string) ([]searchResult, error) {
	terms := make([]string, 0, len(claims.Repos)+len(claims.Owners))
	for _, repo := range claims.Repos {
		terms = append(terms, "repo:"+repo)
	}
	for _, owner := range claims.Owners {
		terms = append(terms, "user:"+owner)
	}
	if len(terms) == 0 {
		return []searchResult{}, nil
	}
	// A bare #n is never a reference the follow route accepts, so it is read here: search the number, keep that number.
	number := 0
	if m := bareNumber.FindStringSubmatch(text); m != nil {
		number, _ = strconv.Atoi(m[1])
		text = m[1]
	}
	ref, refErr := campaigns.ParseIssueRef(text)

	client, ok := s.Clients(fleet, host)
	if !ok {
		return nil, fmt.Errorf("fleet %s has no GitHub source for %s", fleet, host)
	}
	token, err := client.Token(ctx)
	if err != nil {
		return nil, err
	}

	scope := "(" + strings.Join(terms, " OR ") + ")"
	var plain, parents []github.IssueHit
	if text == "" {
		parents, err = client.SearchIssues(ctx, token, "is:issue is:open has:sub-issue "+scope)
	} else {
		q := "is:issue " + scope + " " + text
		var perr, qerr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			plain, qerr = client.SearchIssues(ctx, token, q)
		}()
		go func() {
			defer wg.Done()
			parents, perr = client.SearchIssues(ctx, token, "is:issue has:sub-issue "+scope+" "+text)
		}()
		wg.Wait()
		if err = qerr; err == nil {
			err = perr
		}
	}
	if err != nil {
		return nil, err
	}

	rows := []searchResult{}
	seen := map[string]bool{}
	add := func(h github.IssueHit, parent bool) {
		key := strings.ToLower(fmt.Sprintf("%s#%d", h.Repo, h.Number))
		if seen[key] || (number > 0 && h.Number != number) || !s.routesTo(host, h.Repo, fleet) {
			return
		}
		seen[key] = true
		rows = append(rows, s.row(h, parent || h.SubIssues > 0))
	}
	for _, h := range parents {
		add(h, true)
	}
	for _, h := range plain {
		add(h, false)
	}

	if refErr == nil && strings.EqualFold(hostOf(ref), host) && s.routesTo(host, ref.Owner+"/"+ref.Repo, fleet) &&
		!seen[strings.ToLower(fmt.Sprintf("%s/%s#%d", ref.Owner, ref.Repo, ref.Number))] {
		h, err := client.LookupIssue(ctx, token, ref.Owner, ref.Repo, ref.Number)
		if err != nil {
			return nil, err
		}
		if h != nil {
			rows = append([]searchResult{s.row(*h, h.SubIssues > 0)}, rows...)
		}
	}
	return rows, nil
}

// routesTo is false for a repo another fleet holds, which a user: scope can reach.
func (s *Service) routesTo(host, repo, fleet string) bool {
	return s.Route == nil || s.Route(host, repo) == fleet
}

func (s *Service) row(h github.IssueHit, parent bool) searchResult {
	row := searchResult{Title: h.Title, Reference: fmt.Sprintf("%s#%d", h.Repo, h.Number), URL: h.URL, State: "open", Parent: parent,
		Progress: progressView{Closed: h.SubClosed, Total: h.SubIssues}}
	if h.Closed {
		row.State = "closed"
	}
	if f, ok := s.Store.Lookup(h.URL); ok {
		row.Status = "followed"
		if f.Ended() {
			row.Status = f.endState()
		}
	}
	return row
}
