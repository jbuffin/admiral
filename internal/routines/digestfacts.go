package routines

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/campaigns"
)

// The chat card results a digest reports, spelled as chat.ShipSailedOff, ShipAnchored and ShipMoored.
const (
	cardSailedOff = "sailed_off"
	cardAnchored  = "anchored"
	cardMoored    = "moored"
)

// CardFact is a ship card the fleet's chat log holds, at the instant it was posted.
type CardFact struct {
	At     time.Time
	Ship   string // the session
	Voyage string // with Ship, the voyage: a reused session name starts a new one
	Name   string
	Result string
	PRs    []string
}

// ShipFact is a ship's current state; only a moored ship has a merge state to show.
type ShipFact struct {
	Ship, Voyage string
	Moored       bool
	Ready        bool
	MergeReasons []string
}

// InboxFact is an Inbox item; Fleet is the fleet key and From names who raised it.
type InboxFact struct {
	Fleet, Kind, From string
	Created           time.Time
}

// kindsInRoutineRuns are the Inbox items the routine-runs section already reports, so the Inbox section leaves them out.
var kindsInRoutineRuns = []string{"routine_proposal", "event_proposal"}

// ChildFact is a followed campaign's child as it stands now; Ended is when its campaign's follow ended, zero while followed.
type ChildFact struct {
	Campaign, Child, Title string
	Closed, Ready          bool
	Ended                  time.Time
}

// ChildFacts is the children of a campaign's tree, nested parents' children included, in tree order.
func ChildFacts(campaign string, ended time.Time, root *campaigns.Node) []ChildFact {
	var out []ChildFact
	var walk func(n *campaigns.Node)
	walk = func(n *campaigns.Node) {
		for _, c := range n.Children {
			if c.Parent {
				walk(c)
				continue
			}
			out = append(out, ChildFact{Campaign: campaign, Child: c.URL, Title: c.Title, Closed: c.Closed, Ready: c.Status == campaigns.Ready, Ended: ended})
		}
	}
	if root != nil {
		walk(root)
	}
	return out
}

// FleetFacts gathers a fleet's facts from narrow readers; each reader is the real service in main and a stub in a test.
// Cards reads the ship cards logged after since.
type FleetFacts struct {
	Store    *Store
	Cards    func(fleet string, since time.Time) []CardFact
	Ships    func(fleet string) []ShipFact
	Inbox    func() []InboxFact
	Children func(fleet string) []ChildFact
}

func inWindow(at, from, to time.Time) bool { return at.After(from) && !at.After(to) }

// Facts is every section with a line for the window (from, to] of one routine. Its commit stores the campaign snapshot
// the routine's next window is compared with.
func (f *FleetFacts) Facts(fleet, routine string, from, to time.Time) ([]FactSection, func(), error) {
	var cards []CardFact
	if f.Cards != nil {
		cards = f.Cards(fleet, from)
	}
	slices.SortStableFunc(cards, func(a, b CardFact) int { return a.At.Compare(b.At) })
	merge := map[[2]string]ShipFact{}
	if f.Ships != nil {
		for _, sh := range f.Ships(fleet) {
			if sh.Moored {
				merge[[2]string{sh.Ship, sh.Voyage}] = sh
			}
		}
	}
	var ended, moored []string
	for _, c := range cards {
		if !inWindow(c.At, from, to) {
			continue
		}
		switch {
		case c.Result == cardSailedOff:
			ended = append(ended, c.Name+" sailed off")
		case c.Result == cardAnchored:
			ended = append(ended, c.Name+" anchored")
		case c.Result == cardMoored && len(c.PRs) > 0:
			line := c.Name + " moored with " + strings.Join(c.PRs, ", ")
			if sh, ok := merge[[2]string{c.Ship, c.Voyage}]; ok {
				switch {
				case sh.Ready:
					line += "; ready to merge"
				case len(sh.MergeReasons) > 0:
					line += "; merge problems: " + strings.ReplaceAll(strings.Join(sh.MergeReasons, ", "), "_", " ")
				}
			}
			moored = append(moored, line)
		}
	}
	out := []FactSection{{Title: "Ships that sailed off or anchored", Lines: ended}, {Title: "Ships moored with a PR", Lines: moored}}
	out = append(out, f.inbox(fleet, from, to))
	camp, commit, err := f.campaigns(fleet, routine, from)
	if err != nil {
		return nil, nil, err
	}
	out = append(out, camp)
	var lit []FactSection
	for _, s := range out {
		if len(s.Lines) > 0 {
			lit = append(lit, s)
		}
	}
	return lit, commit, nil
}

// inbox is current state, so it counts as a change only when an item of the fleet was created in the window.
func (f *FleetFacts) inbox(fleet string, from, to time.Time) FactSection {
	if f.Inbox == nil {
		return FactSection{}
	}
	var mine []InboxFact
	fresh := false
	for _, it := range f.Inbox() {
		if it.Fleet != fleet || slices.Contains(kindsInRoutineRuns, it.Kind) {
			continue
		}
		mine = append(mine, it)
		fresh = fresh || inWindow(it.Created, from, to)
	}
	if !fresh {
		return FactSection{}
	}
	oldest := mine[0]
	for _, it := range mine[1:] {
		if !it.Created.IsZero() && (oldest.Created.IsZero() || it.Created.Before(oldest.Created)) {
			oldest = it
		}
	}
	line := fmt.Sprintf("%d waiting for the engineer", len(mine))
	if !oldest.Created.IsZero() {
		line += fmt.Sprintf("; the oldest, a %s from %s, since %s", oldest.Kind, oldest.From, oldest.Created.UTC().Format("2006-01-02"))
	}
	return FactSection{Title: "Inbox", Lines: []string{line}}
}

type childState struct{ Closed, Ready bool }

// childSnapshot is what the last digest saw of a fleet's campaign children, since the campaign store keeps current state only.
type childSnapshot struct {
	Taken    bool                  `json:"taken"`
	Children map[string]childState `json:"children"`
}

func (f *FleetFacts) campaigns(fleet, routine string, from time.Time) (FactSection, func(), error) {
	noop := func() {}
	if f.Children == nil || f.Store == nil {
		return FactSection{}, noop, nil
	}
	path := filepath.Join(f.Store.dir(fleet), "digest-campaigns-"+routine+".json")
	var prev childSnapshot
	if err := readJSON(path, &prev); err != nil {
		return FactSection{}, nil, err
	}
	now := childSnapshot{Taken: true, Children: map[string]childState{}}
	var lines []string
	for _, c := range f.Children(fleet) {
		// a follow ends in the read that sees its last child closed, so only one that ended before the window is old news
		if !c.Ended.IsZero() && !c.Ended.After(from) {
			continue
		}
		key := c.Campaign + "|" + c.Child
		cur := childState{c.Closed, c.Ready}
		now.Children[key] = cur
		if !prev.Taken {
			continue
		}
		was := prev.Children[key]
		switch {
		case cur.Closed && !was.Closed:
			lines = append(lines, c.Title+" closed")
		case cur.Ready && !was.Ready && !cur.Closed:
			lines = append(lines, c.Title+" became ready")
		}
	}
	// a failed write only repeats these changes in the routine's next digest
	commit := func() { _ = writeFile(path, now) }
	return FactSection{Title: "Campaign children", Lines: lines}, commit, nil
}
