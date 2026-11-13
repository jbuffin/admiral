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
			if hint, ok := repofinder.Hint(req.CloneRepo, req.Text); ok {
				for _, e := range repofinder.List(repofinder.FleetRoots(fleet.SearchRoots, fleet.CloneRoot)) {
					if slices.Contains(e.Remotes, hint) {
						req.Clone, req.CloneRepo, req.Dir = false, "", e.Path
						break
					}
				}
			}
		}
	}
	sh, lerr := c.svc.Launch(ctx, req)
	if lerr != nil {
		return commitments.ShipRef{}, lerr
	}
	return commitments.ShipRef{Session: sh.Session, Name: sh.Name}, nil
}

func (c commitmentShips) Holder(key string) (commitments.ShipRef, bool) {
	sh, ok := c.svc.Holder(key)
	return commitments.ShipRef{Session: sh.Session, Name: sh.Name}, ok
}
