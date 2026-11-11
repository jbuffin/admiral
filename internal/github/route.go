// Package github routes GitHub items to fleets.
package github

import "github.com/jbuffin/admiral/internal/config"

// RouteFleet is what routing needs from one fleet, in config-file order.
type RouteFleet struct {
	Key    string
	Repos  []string // lowercase host/owner/repo identities of checkouts under its search roots
	Owners []string // org or user logins (compare case-insensitively)
}

// Router decides which fleet owns an item's repo.
type Router struct{}

// NewRouter builds a router; fleets[0] is the default fleet.
func NewRouter(fleets []RouteFleet) *Router { return &Router{} }

// RouterFromFleets reads each fleet's checkouts and builds a router.
func RouterFromFleets(fleets []config.Fleet) *Router { return &Router{} }

// Route returns the one fleet key that owns the item's repo.
func (r *Router) Route(host, nameWithOwner string) string { return "" }

// Warnings returns the conflict warnings that fleet shows.
func (r *Router) Warnings(fleetKey string) []string { return nil }
