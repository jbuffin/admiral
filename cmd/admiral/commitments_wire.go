package main

import (
	"context"

	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ships"
)

// commitmentShips adapts the ship service to what the commitment runner needs.
type commitmentShips struct{ svc *ships.Service }

func (c commitmentShips) Launch(ctx context.Context, req launcher.Request) (commitments.ShipRef, *launcher.Error) {
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
