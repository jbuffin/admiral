// Package push decides which events reach the engineer, and detects them from ship and Inbox changes.
package push

import (
	"regexp"
	"strings"

	"github.com/jbuffin/admiral/internal/textsafe"
)

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
	// ReportReply is AdmiralBot's reply to a ship's report turn.
	ReportReply Kind = "report_reply"
	// DigestReply is AdmiralBot's reply to a fleet digest turn.
	DigestReply Kind = "digest_reply"
)

var pushing = map[Kind]bool{
	ChatQuestion: true, ShipQuestion: true, InboxQuestion: true, WakeProposal: true, Findings: true,
	ReadyToMerge: true, Anchored: true, CodeApproval: true, RoutineNotify: true, ReportReply: true, DigestReply: true,
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
	// A tap lands on this fleet's Routines screen at Routine (Target "routine"), for a routine run with no ship to open.
	Routine string
	// A tap lands on the fleet's chat at this message (Target "chat").
	Message string
}

const (
	TargetPeek    = "peek"
	TargetShip    = "ship"
	TargetRoutine = "routine"
	TargetChat    = "chat"
)

// Reply is AdmiralBot's reply to a report turn, as the push describes it.
type Reply struct {
	Fleet, FleetName string
	Ship, Result     string // the ship's name and its card's result, as the card spells it
	Card, Message    string // the ship card's id and the reply's message id in the chat log
	Text             string // the reply's first message, markdown
}

// ReportReplyEvent is the event for a reply.
func ReportReplyEvent(r Reply) Event {
	return Event{Kind: ReportReply, Fleet: r.Fleet, FleetName: r.FleetName, Key: "report-reply-" + r.Card,
		Name: r.Ship + " " + strings.ReplaceAll(r.Result, "_", " "), Text: plainText(firstLine(r.Text)),
		Target: TargetChat, Message: r.Message}
}

// Digest is AdmiralBot's reply to a fleet digest turn, as the push describes it.
type Digest struct {
	Fleet, FleetName string
	Turn, Message    string // the digest turn's id and the reply's message id in the chat log
	Text             string // the reply's first message, markdown
}

// DigestReplyEvent is the event for a digest reply; it has its own key per digest run.
func DigestReplyEvent(d Digest) Event {
	return Event{Kind: DigestReply, Fleet: d.Fleet, FleetName: d.FleetName, Key: "digest-reply-" + d.Turn,
		Name: "Fleet digest", Text: plainText(firstLine(d.Text)), Target: TargetChat, Message: d.Message}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

var (
	mdLink = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	// Not #{1,6}: the tmux boundary test reads that as a tmux format.
	mdHeading = regexp.MustCompile(`^\s*(##?#?#?#?#?|>)\s+`)
	// Marks drop only where they pair around text, so `__init__` and `2 * 3 = 6` stay as written.
	mdBold   = regexp.MustCompile(`\*\*(\S(?:[^*]*\S)?)\*\*`)
	mdStar   = regexp.MustCompile(`\*(\S(?:[^*]*\S)?)\*`)
	mdStrike = regexp.MustCompile(`~~(\S(?:[^~]*\S)?)~~`)
	mdCode   = regexp.MustCompile("`([^`]+)`")
	mdUnder  = regexp.MustCompile(`(^|\s)_([^_]+)_(\s|[.,;:!?]|$)`)
)

// plainText turns a line of markdown into the text a lock screen shows: links keep their text, paired marks drop.
func plainText(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdHeading.ReplaceAllString(s, "")
	s = mdCode.ReplaceAllString(s, "$1")
	s = mdBold.ReplaceAllString(s, "$1")
	s = mdStar.ReplaceAllString(s, "$1")
	s = mdStrike.ReplaceAllString(s, "$1")
	return mdUnder.ReplaceAllString(s, "$1$2$3")
}

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
	Routine   string `json:"routine,omitempty"`
	Message   string `json:"message,omitempty"`
}

func PayloadOf(ev Event) Payload {
	return Payload{Fleet: ev.Fleet, FleetName: clean(ev.FleetName), Name: clean(ev.Name), Line: oneLine(ev.Text), Tag: ev.Key,
		Target: ev.Target, Session: ev.Session, Item: ev.Item, Crew: ev.Crew, Merge: ev.Merge, Routine: ev.Routine, Message: ev.Message}
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(clean(s))
	if r := []rune(s); len(r) > maxLine {
		s = string(r[:maxLine-1]) + "…"
	}
	return s
}

// clean drops control and bidi characters: the payload can show on a lock screen, where a reordering mark could dress one ask as another.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if textsafe.Hidden(r) {
			return -1
		}
		return r
	}, s)
}
