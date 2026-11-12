// Package push decides which events reach the engineer, and detects them from ship and Inbox changes.
package push

import "strings"

// Kind names an event. Only the kinds below ever push or alert; any other kind is counted and never heard.
type Kind string

const (
	ChatQuestion  Kind = "chat_question"
	ShipQuestion  Kind = "ship_question"
	InboxQuestion Kind = "inbox_question"
	WakeProposal  Kind = "wake_proposal"
	Findings      Kind = "findings"
	ReadyToMerge  Kind = "ready_to_merge"
	Anchored      Kind = "anchored"
	// CodeApproval and RoutineNotify have no producer yet; Watcher.Emit is their way in.
	CodeApproval  Kind = "code_approval"
	RoutineNotify Kind = "routine_notify"
)

var pushing = map[Kind]bool{
	ChatQuestion: true, ShipQuestion: true, InboxQuestion: true, WakeProposal: true, Findings: true,
	ReadyToMerge: true, Anchored: true, CodeApproval: true, RoutineNotify: true,
}

// Event is one new occurrence worth hearing about.
type Event struct {
	Kind Kind
	// Fleet is the fleet key; empty for an event that belongs to no fleet.
	Fleet     string
	FleetName string
	// Key identifies the item, so a repeat replaces the earlier notification instead of stacking.
	Key string
	// Name is the fleet, ship or source the event is about.
	Name string
	Text string
	// A tap lands on a Peek (Target "peek") for Session plus Item, Crew or Merge, or on the ship's own view
	// (Target "ship") for an anchored ship, which is never an Inbox entry.
	Target  string
	Session string
	Item    string
	Crew    string
	Merge   bool
}

const (
	TargetPeek = "peek"
	TargetShip = "ship"
)

type Outcome int

const (
	Nothing Outcome = iota
	// Push goes to the subscribed devices.
	Push
	// Alert is the chime and toast in the windows being watched.
	Alert
)

func (o Outcome) String() string {
	return [...]string{"nothing", "push", "alert"}[o]
}

// Decide: a fleet with Notify off hears nothing at all, a watched window gets the alert instead of a push, and an
// event with no fleet treats Notify as on.
func Decide(ev Event, watching, notify bool) Outcome {
	if !pushing[ev.Kind] || (ev.Fleet != "" && !notify) {
		return Nothing
	}
	if watching {
		return Alert
	}
	return Push
}

const maxLine = 120

// Payload is what a device is told: no code, diff, prompt or secret ever rides in it.
type Payload struct {
	Fleet     string `json:"fleet,omitempty"`
	FleetName string `json:"fleet_name,omitempty"`
	Name      string `json:"name"`
	Line      string `json:"line"`
	Tag       string `json:"tag"`
	Target    string `json:"target,omitempty"`
	Session   string `json:"session,omitempty"`
	Item      string `json:"item,omitempty"`
	Crew      string `json:"crew,omitempty"`
	Merge     bool   `json:"merge,omitempty"`
}

func PayloadOf(ev Event) Payload {
	return Payload{Fleet: ev.Fleet, FleetName: ev.FleetName, Name: ev.Name, Line: oneLine(ev.Text), Tag: ev.Key,
		Target: ev.Target, Session: ev.Session, Item: ev.Item, Crew: ev.Crew, Merge: ev.Merge}
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if r := []rune(s); len(r) > maxLine {
		s = string(r[:maxLine-1]) + "…"
	}
	return s
}
