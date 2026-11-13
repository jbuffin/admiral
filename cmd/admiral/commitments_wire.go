package main

import (
	"context"

	"slices"

	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/repofinder"
	"github.com/jbuffin/admiral/internal/ships"
)

// commitmentShips adapts the ship service to what the commitment runner needs.
type commitmentShips struct {
	svc    *ships.Service
	config func() *config.Config
}

func (c commitmentShips) Launch(ctx context.Context, req launcher.Request) (commitments.ShipRef, *launcher.Error) {
	// A clone launch is refused where the fleet already holds the repo, and a campaign child names its repo to clone.
	if req.Clone && req.Dir == "" && c.config != nil {
		if fleet, ok := c.config().Fleet(req.Fleet); ok {
			if dir, ok := checkoutFor(req, repofinder.FleetRoots(fleet.SearchRoots, fleet.CloneRoot)); ok {
				req.Clone, req.CloneRepo, req.Dir = false, "", dir
			}
		}
	}
	sh, lerr := c.svc.Launch(ctx, req)
	if lerr != nil {
		return commitments.ShipRef{}, lerr
	}
	return commitments.ShipRef{Session: sh.Session, Name: sh.Name, Status: sh.Status}, nil
}

func (c commitmentShips) Holder(key string) (commitments.ShipRef, bool) {
	sh, ok := c.svc.Holder(key)
	return commitments.ShipRef{Session: sh.Session, Name: sh.Name, Status: sh.Status}, ok
}

// cloneHint is the repo a clone launch is for: a campaign child names its own, which is never read from its text.
func cloneHint(req launcher.Request) (string, bool) {
	if req.CloneRepo != "" {
		if id, ok := repofinder.Identity(req.CloneRepo); ok {
			return id, true
		}
		return repofinder.Identity("https://" + req.CloneRepo)
	}
	return repofinder.Hint("", req.Text)
}

// checkoutFor is the checkout under roots that holds the repo a clone launch is for.
func checkoutFor(req launcher.Request, roots []string) (string, bool) {
	hint, ok := cloneHint(req)
	if !ok {
		return "", false
	}
	for _, e := range repofinder.List(roots) {
		if slices.Contains(e.Remotes, hint) {
			return e.Path, true
		}
	}
	return "", false
}
