package main

import (
	"time"

	"github.com/jbuffin/admiral/internal/campaigns/follow"
	"github.com/jbuffin/admiral/internal/chat"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/ships"
)

// newFleetFacts reads a fleet digest's facts from the chat log, the ships, the Inbox and the followed campaigns.
func newFleetFacts(store *routines.Store, chatSvc *chat.Service, shipList func() []ships.Ship, inboxSvc *inbox.Service, followSvc *follow.Service) *routines.FleetFacts {
	return &routines.FleetFacts{
		Store: store,
		Cards: func(fleet string, since time.Time) []routines.CardFact {
			var out []routines.CardFact
			for _, c := range chatSvc.PostedShipCards(fleet, since) {
				out = append(out, routines.CardFact{At: c.At, Ship: c.Ship, Voyage: c.Voyage, Name: c.Name, Result: c.Result, PRs: c.PRs})
			}
			return out
		},
		Ships: func(fleet string) []routines.ShipFact {
			var out []routines.ShipFact
			for _, sh := range shipList() {
				if sh.Fleet != fleet {
					continue
				}
				voyage := sh.FirstLaunched // the card detector's voyage stamp falls back to the launch the same way
				if voyage == "" {
					voyage = sh.Launched
				}
				f := routines.ShipFact{Ship: sh.Session, Voyage: voyage, Moored: sh.Status == ships.StatusMoored, Ready: sh.StackReady}
				for _, pr := range sh.PRs {
					f.MergeReasons = append(f.MergeReasons, pr.MergeReasons...)
				}
				out = append(out, f)
			}
			return out
		},
		Inbox: func() []routines.InboxFact {
			var out []routines.InboxFact
			for _, it := range inboxSvc.List() {
				from := it.Name
				if from == "" {
					from = it.Session
				}
				out = append(out, routines.InboxFact{Fleet: it.Fleet, Kind: it.Kind, From: from, Created: it.Created})
			}
			return out
		},
		Children: func(fleet string) []routines.ChildFact {
			var out []routines.ChildFact
			for _, f := range followSvc.Store.List() {
				if f.Fleet == fleet {
					out = append(out, routines.ChildFacts(f.ID, f.EndedAt, followSvc.Snapshot(f.ID).Campaign.Root)...)
				}
			}
			return out
		},
	}
}
