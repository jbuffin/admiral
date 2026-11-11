// Package github routes GitHub items to fleets.
package github

import (
	"fmt"
	"strings"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/repofinder"
)

// RouteFleet is what routing needs from one fleet, in config-file order.
type RouteFleet struct {
	Key    string
	Repos  []string // lowercase host/owner/repo identities of checkouts under its search roots
	Owners []string // org or user logins (compare case-insensitively)
}

// Router decides which fleet owns an item's repo. A checkout beats an owner
// claim, which beats the default fleet (the first one). Where two fleets claim
// the same repo or owner, the first in the file wins and each claimant gets a
// warning.
type Router struct {
	def      string
	repoFor  map[string]string // host/owner/repo -> first fleet with it checked out
	ownerFor map[string]string // lowercase owner -> first fleet listing it
	warnings map[string][]string
}

// NewRouter builds a router; fleets[0] is the default fleet.
func NewRouter(fleets []RouteFleet) *Router {
	r := &Router{
		repoFor:  map[string]string{},
		ownerFor: map[string]string{},
		warnings: map[string][]string{},
	}
	if len(fleets) > 0 {
		r.def = fleets[0].Key
	}
	repoClaims := map[string][]string{}
	ownerClaims := map[string][]string{}
	var repoOrder, ownerOrder []string
	for _, f := range fleets {
		for _, id := range f.Repos {
			id = strings.ToLower(id)
			repoClaims[id], repoOrder = claim(repoClaims[id], repoOrder, id, f.Key)
		}
		for _, o := range f.Owners {
			o = strings.ToLower(strings.TrimSpace(o))
			ownerClaims[o], ownerOrder = claim(ownerClaims[o], ownerOrder, o, f.Key)
		}
	}
	for _, id := range repoOrder {
		r.repoFor[id] = repoClaims[id][0]
		r.warn(repoClaims[id], func(self, others string) string {
			return fmt.Sprintf("%s is checked out in fleets %s and %s; %s takes its items", id, self, others, repoClaims[id][0])
		})
	}
	for _, o := range ownerOrder {
		r.ownerFor[o] = ownerClaims[o][0]
		r.warn(ownerClaims[o], func(self, others string) string {
			return fmt.Sprintf("owner %s is listed by fleets %s and %s; %s takes its items", o, self, others, ownerClaims[o][0])
		})
	}
	return r
}

// claim records that fleet key claims name, once per fleet.
func claim(keys, order []string, name, key string) ([]string, []string) {
	if len(keys) == 0 {
		order = append(order, name)
	}
	for _, k := range keys {
		if k == key {
			return keys, order
		}
	}
	return append(keys, key), order
}

// warn adds a warning to each of the claiming fleets when there are two or more.
func (r *Router) warn(claimants []string, msg func(self, others string) string) {
	if len(claimants) < 2 {
		return
	}
	for i, self := range claimants {
		others := append(append([]string{}, claimants[:i]...), claimants[i+1:]...)
		r.warnings[self] = append(r.warnings[self], msg(self, strings.Join(others, ", ")))
	}
}

// RouterFromFleets reads each fleet's checkouts and builds a router. Search
// roots are not projects, and remotes on every host are kept.
func RouterFromFleets(fleets []config.Fleet) *Router {
	rf := make([]RouteFleet, 0, len(fleets))
	for _, f := range fleets {
		var repos []string
		for _, e := range repofinder.List(f.SearchRoots) {
			if e.Kind == repofinder.KindCheckout {
				repos = append(repos, e.Remotes...)
			}
		}
		rf = append(rf, RouteFleet{Key: f.Key, Repos: repos, Owners: f.Owners})
	}
	return NewRouter(rf)
}

// Route returns the one fleet key that owns the item's repo, or "" with no fleets.
func (r *Router) Route(host, nameWithOwner string) string {
	name := strings.ToLower(nameWithOwner)
	if k, ok := r.repoFor[strings.ToLower(host)+"/"+name]; ok {
		return k
	}
	owner, _, _ := strings.Cut(name, "/")
	if k, ok := r.ownerFor[owner]; ok {
		return k
	}
	return r.def
}

// Warnings returns the conflict warnings that fleet shows.
func (r *Router) Warnings(fleetKey string) []string { return r.warnings[fleetKey] }
