package main

import (
	"github.com/jbuffin/admiral/internal/campaigns"
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
		Cards: func(fleet string) []routines.CardFact {
			var out []routines.CardFact
			for _, c := range chatSvc.PostedShipCards(fleet) {
				out = append(out, routines.CardFact{At: c.At, Ship: c.Ship, Name: c.Name, Result: c.Result, PRs: c.PRs})
			}
			return out
		},
		Ships: func(fleet string) []routines.ShipFact {
			var out []routines.ShipFact
			for _, sh := range shipList() {
				if sh.Fleet != fleet || sh.Status != ships.StatusMoored {
					continue
				}
				f := routines.ShipFact{Ship: sh.Session, Ready: sh.StackReady}
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
				who := it.Name
				if who == "" {
					who = it.Session
				}
				out = append(out, routines.InboxFact{Fleet: it.Fleet, Title: "a " + it.Kind + " from " + who, Created: it.Created})
			}
			return out
		},
		Children: func(fleet string) []routines.ChildFact {
			var out []routines.ChildFact
			for _, f := range followSvc.Store.List() {
				if f.Fleet != fleet || f.Ended() {
					continue
				}
				var walk func(n *campaigns.Node)
				walk = func(n *campaigns.Node) {
					for _, c := range n.Children {
						if c.Parent {
							walk(c)
							continue
						}
						out = append(out, routines.ChildFact{Campaign: f.ID, Child: c.URL, Title: c.Title, Closed: c.Closed, Ready: c.Status == campaigns.Ready})
					}
				}
				if root := followSvc.Snapshot(f.ID).Campaign.Root; root != nil {
					walk(root)
				}
			}
			return out
		},
	}
}
