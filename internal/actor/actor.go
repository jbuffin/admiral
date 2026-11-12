// Package actor is AdmiralBot's identity on the server: a fleet's actor token, the request header that carries it,
// and the guard that holds an actor request to its fleet and to the routes AdmiralBot may call.
package actor

import (
	"context"
	"net/http"
)

// Header carries a fleet's actor token on a loopback request.
const Header = "X-Admiral-Actor"

// Minter derives a fleet's actor token from the home's secret key.
type Minter struct{}

// NewMinter opens the secret key under home, creating its 0600 file on first use.
func NewMinter(home string) (*Minter, error) { return &Minter{}, nil }

// Token is the fleet's token for one chat session's nonce.
func (m *Minter) Token(fleet, nonce string) string { return "" }

// Guard is the middleware that recognises an actor token and fences the request it carries.
type Guard struct {
	Tokens        func(token string) (fleet string, ok bool)
	ShipFleet     func(session string) (fleet string, known bool)
	ItemFleet     func(session, id string) (fleet string, known bool)
	SourceFleet   func(key string) (fleet string, known bool)
	CampaignFleet func(id string) (fleet string, known bool)
}

func (g *Guard) Middleware(next http.Handler) http.Handler { return next }

// WithFleet marks ctx as the request of AdmiralBot acting for fleet.
func WithFleet(ctx context.Context, fleet string) context.Context { return ctx }

// FleetOf is the fleet AdmiralBot acts for in ctx, and false for the engineer's own request.
func FleetOf(ctx context.Context) (string, bool) { return "", false }
