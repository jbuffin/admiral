package follow

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/repofinder"
)

// Follow follows the campaign a reference names, or returns the one already followed; created says which.
func (s *Service) Follow(ctx context.Context, reference string) (Followed, bool, *launcher.Error) {
	ref, err := campaigns.ParseIssueRef(reference)
	if err != nil {
		return Followed{}, false, orderError(http.StatusBadRequest, err.Error())
	}
	host := hostOf(ref)
	url := fmt.Sprintf("https://%s/%s/%s/issues/%d", host, ref.Owner, ref.Repo, ref.Number)
	if have, ok := s.Store.Lookup(url); ok {
		return have, false, nil
	}
	fleet := ""
	if s.Route != nil {
		fleet = s.Route(host, ref.Owner+"/"+ref.Repo)
	}
	if fleet == "" {
		return Followed{}, false, orderError(http.StatusUnprocessableEntity, fmt.Sprintf("No fleet owns %s/%s, so Admiral can't read it", ref.Owner, ref.Repo))
	}
	tree, err := s.readTree(ctx, fleet, url)
	if err != nil {
		return Followed{}, false, orderError(http.StatusUnprocessableEntity, err.Error())
	}
	if tree.URL != "" {
		url = tree.URL
	}
	got, created, err := s.Store.Follow(Followed{URL: url, Fleet: fleet, FollowedAt: s.now(), Tree: tree, ReadAt: s.now()})
	if err != nil {
		return Followed{}, false, orderError(http.StatusInternalServerError, err.Error())
	}
	if created && allClosed(tree) {
		_ = s.Store.End(got.ID, s.now())
		got, _ = s.Store.Get(got.ID)
	}
	return got, created, nil
}

// Snapshot is the campaign as a commitment pass reads it: the latest good tree, built the way the page's is.
func (s *Service) Snapshot(id string) campaigns.Read {
	f, ok := s.Store.Get(id)
	if !ok {
		return campaigns.Read{ID: id}
	}
	c := s.view(f)
	out := campaigns.Read{ID: id, Found: true, Followed: !f.Ended(), Campaign: c, Orders: map[string]campaigns.Order{}}
	for _, n := range c.Ready {
		clone, _ := repofinder.Identity(s.repoFor(f, n))
		out.Orders[n.URL] = campaigns.Order{Text: launchText(n), CloneRepo: clone}
	}
	return out
}
