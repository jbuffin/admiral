package chat

import (
	"errors"
	"time"

	"github.com/jbuffin/admiral/internal/ships"
)

var errNotBuilt = errors.New("ship cards: not built")

// The results a ship card carries.
const (
	ShipSailedOff    = "sailed_off"
	ShipAnchored     = "anchored"
	ShipLost         = "lost"
	ShipMoored       = "moored"
	ShipReadyToMerge = "ready_to_merge"
	ShipReturned     = "returned"
)

// ShipCardPR is a PR a ship card links to.
type ShipCardPR struct {
	URL    string
	Number int
}

// ShipCard is the card a ship's result posts in its fleet's chat.
type ShipCard struct {
	Ship          string // the ship's session name, which names its conversation
	FirstLaunched string // with Ship, the voyage: a reused name starts a new one
	Skill         string
	SkillKind     string
	Result        string
	PRs           []ShipCardPR
}

// PostShipCard appends the card to the fleet's chat log, once per voyage and result. It starts no turn and writes no digest line.
func (s *Service) PostShipCard(fleet string, c ShipCard) error {
	return errNotBuilt
}

// ShipRecord is one outcome-log line about a ship.
type ShipRecord struct {
	At       time.Time
	Ship     string
	Event    string // ended, moored, unmoored, stopped
	Reason   string // an unmoored line's reason
	Clean    bool   // an ended line's exit was clean
	Returned bool   // a stopped line ended a return to port
}

// ShipCards works out which ship cards are due from the ship snapshot and the outcome log, and keeps what it has seen under home.
type ShipCards struct{}

// NewShipCards reads its seen state from home, so a restart repeats nothing.
func NewShipCards(home string, post func(fleet string, c ShipCard) error) *ShipCards {
	return &ShipCards{}
}

// Observe posts the card each event in the snapshot and records is due. On the first pass of a home it posts nothing for
// ships already moored or anchored.
func (d *ShipCards) Observe(snap []ships.Ship, records []ShipRecord, now time.Time) error {
	return errNotBuilt
}
