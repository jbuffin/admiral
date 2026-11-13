package routines

import (
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/standing"
)

// EventKind is one kind a source's catalog lists; Panel is set for a kind scoped to one of the source's panels.
type EventKind struct {
	Kind  string
	Title string
	Panel string
}

// On is a routine's When as a source event, instead of a cron schedule.
type On struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Panel  string `json:"panel,omitempty"`
}

// Event is one outside event as the scheduler sees it. Tier is never a field: Go computes it.
type Event struct {
	Fleet    string
	Source   string
	Kind     string
	Panel    string
	Ref      string
	Actor    string
	Author   string
	Viewer   string // the engineer's login on the source; empty when the source can't say
	Title    string // the item's title as the source read it; untrusted
	Verified bool
}

// Cost is one judge call's price on a trigger-log record.
type Cost struct {
	Call string  `json:"call"`
	USD  float64 `json:"usd"`
}

// UnknownLogin stands for a login a source could not attribute. GitHub logins have no parentheses, so it is no one's
// and no allowlist's.
const UnknownLogin = "(unknown)"

func unattributed(login string) bool { return login == "" || login == UnknownLogin }

// TierOf is the less trusted of the actor's and the author's tiers, so a stranger's item the engineer touched stays T3.
// A login the event doesn't carry, or can't attribute, is a stranger's for its own term, so T1 needs both logins to be
// the viewer's. GitHub names no actor for an item entering a view, so that event is T2 at best, by its author.
func TierOf(ev Event, limits standing.Limits) standing.Tier {
	tier := func(login string) standing.Tier {
		switch {
		case unattributed(login):
			return standing.T3
		case ev.Verified && ev.Viewer != "" && strings.EqualFold(login, ev.Viewer):
			return standing.T1
		case limits.SenderAllowed(login):
			return standing.T2
		}
		return standing.T3
	}
	if ev.Verified && ev.Kind == "new_in_view" {
		if !unattributed(ev.Author) && limits.SenderAllowed(ev.Author) {
			return standing.T2
		}
		return standing.T3
	}
	return max(tier(ev.Actor), tier(ev.Author))
}

// senderOf is who the event names as its sender: the actor when it is a login, else the author, else UnknownLogin.
func senderOf(ev Event) string {
	switch {
	case !unattributed(ev.Actor):
		return ev.Actor
	case !unattributed(ev.Author):
		return ev.Author
	}
	return UnknownLogin
}

// EventTop is the highest action an event routine reaches until restricted and normal ships launch from events.
const EventTop = standing.A1

// Why a decision ended where it did.
const (
	ReasonSkipFilter = "skip_filter"
	ReasonRepo       = "repo_not_allowed"
	ReasonTop        = "top_action"
	ReasonSpend      = "daily_spend_reached"
	ReasonSpendRead  = "daily_spend_unknown"
	ReasonQuiet      = "quiet_hours"
	ReasonHold       = "hold_fire"
	ReasonDedupe     = "dedupe_key_held"
)

// DecideInput is everything the decision reads for one (event, routine) pair.
type DecideInput struct {
	Routine           Routine
	Tier              standing.Tier
	Limits            standing.Limits
	Top               standing.Level
	HoldOn            bool
	Now               time.Time // already in the fleet's zone
	SpentTodayUSD     float64
	SpendUnknown      bool // the log could not be read, so spend and rates are unknown and count as reached
	Counts            standing.Counts
	LiveDedupeKeys    []string
	RestrictedHarness bool
	Sender            string
	Repo              string
	Labels            []string
	DedupeKey         string
}

// Decision is the outcome of one pair. Computed is the level before Top and the budgets; Level is the one acted on.
// Reasons lists, in decision order, each cause that held an action above A1 down, or the one that skipped the event.
type Decision struct {
	Outcome  Outcome
	Level    standing.Level
	Computed standing.Level
	Reasons  []string
}

// routineLevel maps a routine's Before acting onto the ladder.
func routineLevel(m Mode) standing.Level {
	switch m {
	case ReportOnly:
		return standing.A0
	case AskFirst:
		return standing.A1
	}
	return standing.A5
}

// Decide runs the skip filters, the repo allowlist, the effective action, Top, spend, quiet hours, rates and dedupe, in that order.
// Every budget only lowers an action to A1, so a card (A0) and a proposal (A1) never name one.
func Decide(in DecideInput) Decision {
	if in.Limits.Skipped(standing.SkipInput{Routine: in.Routine.ID, Labels: in.Labels, Sender: in.Sender, DedupeKey: in.DedupeKey}) {
		return Decision{Outcome: Skipped, Reasons: []string{ReasonSkipFilter}}
	}
	// an event with no repo fails closed against a non-empty allowlist
	if !in.Limits.RepoAllowed(in.Repo) {
		return Decision{Outcome: Skipped, Reasons: []string{ReasonRepo}}
	}
	effective := func(m Mode) standing.Level {
		return in.Limits.Effective(standing.Event{Tier: in.Tier, Routine: routineLevel(m), RestrictedHarness: in.RestrictedHarness}).Level
	}
	computed := effective(Effective(in.Routine.Before, in.HoldOn))
	d := Decision{Computed: computed, Level: min(computed, in.Top)}
	if in.HoldOn && Effective(in.Routine.Before, true) != in.Routine.Before && effective(in.Routine.Before) > standing.A1 {
		d.Reasons = append(d.Reasons, ReasonHold)
	}
	if computed > standing.A1 {
		if in.Top < computed {
			d.Reasons = append(d.Reasons, ReasonTop)
		}
		if in.SpendUnknown {
			d.Reasons = append(d.Reasons, ReasonSpendRead)
		} else if in.Limits.DailySpendReached(in.SpentTodayUSD) {
			d.Reasons = append(d.Reasons, ReasonSpend)
		}
		if in.Limits.Held(in.Now) {
			d.Reasons = append(d.Reasons, ReasonQuiet)
		}
		if v := in.Limits.CheckRates(in.Counts); !v.OK {
			d.Reasons = append(d.Reasons, v.Reason)
		}
		if standing.DedupeHeld(in.LiveDedupeKeys, in.DedupeKey) {
			d.Reasons = append(d.Reasons, ReasonDedupe)
		}
		for _, r := range d.Reasons {
			if r != ReasonTop && r != ReasonHold {
				d.Level = min(d.Level, standing.A1)
			}
		}
	}
	switch {
	case d.Level <= standing.A0:
		d.Outcome = Reported
	case d.Level == standing.A1:
		d.Outcome = Proposed
	default:
		d.Outcome = Launched
	}
	return d
}

// Enrichment is what a Lookout adds to an event before the decision.
type Enrichment struct {
	DedupeKey string
	Repo      string
	Labels    []string
	Summary   string
	Costs     []Cost
	// Failure is the kind of a Lookout call that failed, such as timeout or bad_reply; HandleEvent records it as lookout_<kind>.
	Failure string
}

// EventActs receives each decision that ends in a card (Reported) or a proposal (Proposed), with its saved record.
type EventActs interface {
	Act(fleet string, r Routine, ev Event, rec Trigger, d Decision, en Enrichment)
}

// EventLoad is what the decision reads of the ships: how many worker ships run and the dedupe_keys they hold.
type EventLoad interface {
	WorkerShips(fleet string) int
	DedupeKeys(fleet string) []string
}

// RepoOfRef is owner/name for a github.com ref, else empty.
func RepoOfRef(ref string) string {
	u, err := url.Parse(ref)
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// eventRoutines are the fleet's enabled routines whose When is this event and whose Do launches a ship: a hand-edited
// prompt never reaches the acts.
func (s *Scheduler) eventRoutines(ev Event) []Routine {
	list, err := s.Store.List(ev.Fleet)
	if err != nil {
		return nil
	}
	var out []Routine
	for _, r := range list {
		if r.Enabled && r.On != nil && r.Do.Launch != nil && r.Do.Prompt == "" && r.On.Source == ev.Source && r.On.Kind == ev.Kind && r.On.Panel == ev.Panel {
			out = append(out, r)
		}
	}
	return out
}

// HandleEvent matches ev to the fleet's enabled event routines, decides each pair and records it.
// It takes no scheduler lock: no event launches yet, and a tick must not hold events up.
func (s *Scheduler) HandleEvent(ev Event) {
	if s.Own != nil && s.Own(ev.Fleet, ev.Ref) {
		return
	}
	f, ok := s.Store.Fleets(ev.Fleet)
	if !ok {
		return
	}
	matches := s.eventRoutines(ev)
	if len(matches) == 0 {
		return
	}
	now := s.Clock.Now()
	zone := s.zone()
	sender := senderOf(ev)
	recs, readErr := s.Store.Records(ev.Fleet)
	if readErr != nil {
		log.Printf("routines: event on %s: the trigger log is unreadable (%v), so spend and rates count as reached", ev.Fleet, readErr)
	}
	spent, counts := spendAndCounts(recs, sender, now, zone)
	spendReason := ""
	switch {
	case readErr != nil:
		spendReason = ReasonSpendRead
	case f.Limits.DailySpendReached(spent):
		spendReason = ReasonSpend
	}
	// the Lookout is asked only when the cap is open and some routine's own filters still let the event through
	var en Enrichment
	if spendReason == "" && s.Enrich != nil {
		for _, r := range matches {
			if !f.Limits.Skipped(standing.SkipInput{Routine: r.ID, Sender: sender}) {
				en = s.Enrich(ev)
				break
			}
		}
	}
	if repo := RepoOfRef(ev.Ref); ev.Verified && repo != "" { // only the built-in source's ref is Admiral's own reading, and it wins over the Lookout's
		en.Repo = repo
	}
	for _, c := range en.Costs {
		spent += c.USD
	}
	var live []string
	switch l := s.Live.(type) {
	case nil:
	case interface {
		Load(fleet string, recs []Trigger) (int, []string)
	}:
		counts.WorkerShips, live = l.Load(ev.Fleet, recs)
	default:
		counts.WorkerShips, live = l.WorkerShips(ev.Fleet), l.DedupeKeys(ev.Fleet)
	}
	holdOn, err := s.Store.HoldOn(ev.Fleet)
	if err != nil {
		holdOn = true
	}
	tier := TierOf(ev, f.Limits)
	costs := en.Costs // one record carries them, or the spend would count once per routine
	for _, r := range matches {
		d := Decide(DecideInput{
			Routine: r, Tier: tier, Limits: f.Limits, Top: EventTop, HoldOn: holdOn, Now: now.In(zone),
			SpentTodayUSD: spent, SpendUnknown: readErr != nil, Counts: counts, LiveDedupeKeys: live,
			Sender: sender, Repo: en.Repo, Labels: en.Labels, DedupeKey: en.DedupeKey,
		})
		if spendReason != "" && d.Outcome != Skipped && !slices.Contains(d.Reasons, spendReason) {
			d.Reasons = append(d.Reasons, spendReason) // the Lookout was not asked
		}
		if en.Failure != "" && d.Outcome != Skipped {
			d.Reasons = append(d.Reasons, "lookout_"+en.Failure)
		}
		rec, ok := s.record(ev.Fleet, Trigger{
			Routine: r.ID, Outcome: d.Outcome, Tier: fmt.Sprintf("T%d", tier), At: now, Event: ev.Ref, Sender: sender, Rule: r.ID,
			Reason: strings.Join(d.Reasons, ", "), Action: describe(r), DedupeKey: en.DedupeKey, Costs: costs,
		})
		if ok {
			costs = nil
			if s.Acts != nil && (d.Outcome == Reported || d.Outcome == Proposed) {
				s.Acts.Act(ev.Fleet, r, ev, rec, d, en)
			}
		}
	}
}

// spendAndCounts reads today's spend and the rate counts a launch is checked against from a fleet's records.
// WorkerShips is left for the caller: the log doesn't know which ships are still here.
func spendAndCounts(recs []Trigger, sender string, now time.Time, zone *time.Location) (float64, standing.Counts) {
	var spent float64
	var c standing.Counts
	for _, r := range recs {
		today := sameDay(r.At.In(zone), now.In(zone))
		if today {
			for _, cost := range r.Costs {
				spent += cost.USD
			}
		}
		if r.Outcome != Launched {
			continue
		}
		if age := now.Sub(r.At); age >= 0 && age < time.Hour {
			c.LaunchesLastHour++
		}
		if sender != "" && strings.EqualFold(r.Sender, sender) && today {
			c.SenderLaunchesToday++
		}
	}
	return spent, c
}

// Records is every trigger-log record of the fleet, newest first, with undo and failure marks applied.
func (s *Store) Records(fleet string) ([]Trigger, error) {
	if _, err := s.fleet(fleet); err != nil {
		return nil, err
	}
	s.mu.Lock()
	lines, err := s.readLog(fleet)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	undone := map[string]time.Time{}
	failed := map[string]string{}
	for _, l := range lines {
		if l.UndoOf != "" {
			undone[l.UndoOf] = l.At
		}
		if l.FailOf != "" {
			failed[l.FailOf] = l.Reason
		}
	}
	out := []Trigger{}
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if l.UndoOf != "" || l.FailOf != "" {
			continue
		}
		if reason, ok := failed[l.ID]; ok {
			l.Outcome, l.Reason = Failed, reason
		}
		if at, ok := undone[l.ID]; ok {
			l.Undone, l.UndoneAt = true, at
		}
		out = append(out, l.Trigger)
	}
	return out, nil
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// SpendToday sums the costs recorded on now's calendar day in zone.
func (s *Store) SpendToday(fleet string, now time.Time, zone *time.Location) (float64, error) {
	recs, err := s.Records(fleet)
	if err != nil {
		return 0, err
	}
	spent, _ := spendAndCounts(recs, "", now, zone)
	return spent, nil
}

// CountsFor reads the rate counts a launch would be checked against from the fleet's log.
// WorkerShips is left for the caller: the log doesn't know which ships are still here.
func (s *Store) CountsFor(fleet, sender string, now time.Time, zone *time.Location) (standing.Counts, error) {
	recs, err := s.Records(fleet)
	if err != nil {
		return standing.Counts{}, err
	}
	_, c := spendAndCounts(recs, sender, now, zone)
	return c, nil
}
