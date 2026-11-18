package routines

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
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
	Name   string
	Result string
	PRs    []string
}

// ShipFact is a ship's current merge state.
type ShipFact struct {
	Ship         string
	Ready        bool
	MergeReasons []string
}

// InboxFact is an Inbox item; Fleet is the fleet key.
type InboxFact struct {
	Fleet, Title string
	Created      time.Time
}

// ChildFact is a followed campaign's child as it stands now.
type ChildFact struct {
	Campaign, Child, Title string
	Closed, Ready          bool
}

// FleetFacts gathers a fleet's facts from narrow readers; each reader is the real service in main and a stub in a test.
type FleetFacts struct {
	Store    *Store
	Cards    func(fleet string) []CardFact
	Ships    func(fleet string) []ShipFact
	Inbox    func() []InboxFact
	Children func(fleet string) []ChildFact
}

func inWindow(at, from, to time.Time) bool { return at.After(from) && !at.After(to) }

// Facts is every section with a line for the window (from, to]. It also stores the campaign snapshot the next window is compared with.
func (f *FleetFacts) Facts(fleet string, from, to time.Time) ([]FactSection, error) {
	var cards []CardFact
	if f.Cards != nil {
		cards = f.Cards(fleet)
	}
	slices.SortStableFunc(cards, func(a, b CardFact) int { return a.At.Compare(b.At) })
	merge := map[string]ShipFact{}
	if f.Ships != nil {
		for _, sh := range f.Ships(fleet) {
			merge[sh.Ship] = sh
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
			if sh, ok := merge[c.Ship]; ok {
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
	camp, err := f.campaigns(fleet)
	if err != nil {
		return nil, err
	}
	out = append(out, camp)
	var lit []FactSection
	for _, s := range out {
		if len(s.Lines) > 0 {
			lit = append(lit, s)
		}
	}
	return lit, nil
}

// inbox is current state, so it counts as a change only when an item of the fleet was created in the window.
func (f *FleetFacts) inbox(fleet string, from, to time.Time) FactSection {
	if f.Inbox == nil {
		return FactSection{}
	}
	var mine []InboxFact
	fresh := false
	for _, it := range f.Inbox() {
		if it.Fleet != fleet {
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
		line += fmt.Sprintf("; the oldest, %s, since %s", oldest.Title, oldest.Created.UTC().Format("2006-01-02"))
	}
	return FactSection{Title: "Inbox", Lines: []string{line}}
}

type childState struct{ Closed, Ready bool }

// childSnapshot is what the last digest saw of a fleet's campaign children, since the campaign store keeps current state only.
type childSnapshot struct {
	Taken    bool                  `json:"taken"`
	Children map[string]childState `json:"children"`
}

func (f *FleetFacts) campaigns(fleet string) (FactSection, error) {
	if f.Children == nil || f.Store == nil {
		return FactSection{}, nil
	}
	path := filepath.Join(f.Store.dir(fleet), "digest-campaigns.json")
	var prev childSnapshot
	if err := readJSON(path, &prev); err != nil {
		return FactSection{}, err
	}
	now := childSnapshot{Taken: true, Children: map[string]childState{}}
	var lines []string
	for _, c := range f.Children(fleet) {
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
	if err := writeFile(path, now); err != nil {
		return FactSection{}, err
	}
	return FactSection{Title: "Campaign children", Lines: lines}, nil
}
