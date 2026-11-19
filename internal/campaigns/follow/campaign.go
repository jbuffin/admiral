package follow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/repofinder"
)

// Owner is the fleet a reference's campaign belongs to: the one that follows it, else the one that routes its repo. It reads nothing.
func (s *Service) Owner(reference string) (string, *launcher.Error) {
	ref, err := campaigns.ParseIssueRef(reference)
	if err != nil {
		return "", orderError(http.StatusBadRequest, err.Error())
	}
	host := hostOf(ref)
	if have, ok := s.Store.Lookup(fmt.Sprintf("https://%s/%s/%s/issues/%d", host, ref.Owner, ref.Repo, ref.Number)); ok {
		return have.Fleet, nil
	}
	if s.Route != nil {
		if fleet := s.Route(host, ref.Owner+"/"+ref.Repo); fleet != "" {
			return fleet, nil
		}
	}
	return "", orderError(http.StatusUnprocessableEntity, fmt.Sprintf("No fleet owns %s/%s, so Admiral can't read it", ref.Owner, ref.Repo))
}

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

// FollowAgain is the engineer's follow: like Follow, and a campaign they unfollowed is followed again on a fresh read of its tree.
// A campaign whose tree closed stays ended.
func (s *Service) FollowAgain(ctx context.Context, reference string) (Followed, bool, *launcher.Error) {
	got, created, lerr := s.Follow(ctx, reference)
	if lerr != nil || created || !got.Ended() || got.endState() != endUnfollowed {
		return got, created, lerr
	}
	tree, err := s.readTree(ctx, got.Fleet, got.URL)
	if err != nil {
		return Followed{}, false, orderError(http.StatusUnprocessableEntity, err.Error())
	}
	applied, err := s.Store.Resume(got.ID, tree, s.now())
	if err != nil {
		return Followed{}, false, orderError(http.StatusInternalServerError, err.Error())
	}
	if applied && allClosed(tree) {
		_ = s.Store.End(got.ID, s.now())
	}
	got, _ = s.Store.Get(got.ID)
	s.changed()
	return got, false, nil
}

// FollowDrafted follows the campaign a drafting ship of fleet filed, or follows it again when the fleet unfollowed it, and returns its id and
// the title Admiral read. A campaign that belongs to another fleet, or to none, is refused with the reason as the error.
func (s *Service) FollowDrafted(fleet, reference string) (id, title string, err error) {
	owner, lerr := s.Owner(reference)
	if lerr != nil {
		return "", "", errors.New(lerr.Msg)
	}
	if owner != fleet {
		return "", "", fmt.Errorf("That campaign belongs to fleet %s, not %s", owner, fleet)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	got, _, lerr := s.FollowAgain(ctx, reference)
	if lerr != nil {
		return "", "", errors.New(lerr.Msg)
	}
	if got.Tree != nil {
		title = got.Tree.Title
	}
	return got.ID, title, nil
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
