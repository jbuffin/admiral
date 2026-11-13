// Package commitments fires the promises AdmiralBot recorded: a launch or a reminder, with no model turn.
package commitments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ledger"
)

// Clock is the time seam; After is one timer to the next due time.
type Clock struct {
	Now   func() time.Time
	After func(d time.Duration) <-chan time.Time
}

// ShipRef is a ship as the runner sees it.
type ShipRef struct {
	Session string
	Name    string
}

// Ships is the part of the ships service the runner uses.
type Ships interface {
	Launch(ctx context.Context, req launcher.Request) (ShipRef, *launcher.Error)
	Holder(key string) (ShipRef, bool)
}

// SettleGrace is how long a clean `ended` waits for a `moored` before the ship counts as sailed off; the ships service moors a ship with open PRs a few seconds after it ends.
const SettleGrace = 15 * time.Second

// pollEvery bounds how late the runner sees a ship event: the outcome log is written after the status change that wakes it.
const pollEvery = 30 * time.Second

// idleEvery is the wake when nothing is due and no ship is watched.
const idleEvery = 10 * time.Minute

// agedAfter is how old a commitment with no trigger gets before the engineer is asked whether they still want it.
const agedAfter = 14 * 24 * time.Hour

// Card outcomes.
const (
	OutcomeLaunched = "launched"
	OutcomeReminded = "reminded"
	OutcomeCantRun  = "cant_run"
	OutcomeDropped  = "dropped"
	OutcomeAged     = "aged"
)

// Card is what the chat shows for a commitment's outcome. ID is commitment-<commitment id>-<outcome>.
type Card struct {
	ID         string
	Commitment string // the commitment's id
	Text       string // the commitment's text
	Outcome    string
	Ship       string // the launched ship's session, for OutcomeLaunched
	Why        string // for OutcomeCantRun and OutcomeDropped
}

// Cards posts a card to a fleet's chat. A second post of one ID adds nothing.
type Cards interface {
	PostCard(fleet string, c Card) error
}

// Runner fires a fleet's commitments when their trigger comes.
type Runner struct {
	Home        string
	Ledger      *ledger.Ledger
	Fleets      func() []string
	SourceFleet func(source string) (string, bool)
	Clock       Clock
	Ships       Ships
	Cards       Cards
	// Ready closes once the host has listed the ships, so a restart finds the ship a launch already made.
	Ready <-chan struct{}
	// GitHubFor is the client of a fleet's GitHub host, nil when the fleet has none.
	GitHubFor func(fleet, host string) GitHub
	// Logf reports a GitHub failure; nil means stderr.
	Logf func(format string, args ...any)
	// ReadFile reads an outcome-log month file; nil means os.ReadFile.
	ReadFile func(path string) ([]byte, error)

	mu  sync.Mutex // one tick at a time
	gmu sync.Mutex // guards answers and failing, which the GitHub poll writes between ticks
	// answers is what GitHub last said about each subject; failing marks the subjects whose latest poll got no answer.
	answers  map[string]ghAnswer
	failing  map[string]bool
	logs     map[string]cachedLog // outcome-log month files by path, under mu
	keptLogs map[string]bool      // the month files read this tick, under mu
	wake     chan struct{}
	once     sync.Once
	dirty    atomic.Bool
}

func (r *Runner) init() { r.once.Do(func() { r.wake = make(chan struct{}, 1) }) }

// Changed wakes the runner: a ship, a resource or the ledger changed.
func (r *Runner) Changed() {
	r.init()
	r.dirty.Store(true)
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run fires what is due, then sleeps until the next time or a Changed, until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	r.init()
	if r.Ready != nil {
		select {
		case <-ctx.Done():
			return
		case <-r.Ready:
		}
	}
	go r.pollLoop(ctx)
	for ctx.Err() == nil {
		r.dirty.Store(false)
		d := r.tick(ctx).Sub(r.Clock.Now())
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-r.Clock.After(d):
		}
	}
}

// Tick fires what is due now.
func (r *Runner) Tick(ctx context.Context) { r.tick(ctx) }

// tick returns the instant to look again.
func (r *Runner) tick(ctx context.Context) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Clock.Now()
	next := now.Add(idleEvery)
	earlier := func(t time.Time) {
		if t.After(now) && t.Before(next) {
			next = t
		}
	}
	for _, fleet := range r.Fleets() {
		list, err := r.Ledger.Commitments(fleet)
		if err != nil {
			continue
		}
		var log *shipLog
		for _, c := range list {
			if ctx.Err() != nil {
				return now.Add(idleEvery)
			}
			var fire bool
			switch {
			case c.Waiting:
				fire = true
				earlier(now.Add(pollEvery))
			case c.Trigger.Kind == ledger.TriggerTime:
				at, err := time.Parse(time.RFC3339, c.Trigger.Subject)
				if err != nil {
					continue
				}
				if fire = !now.Before(at); !fire {
					earlier(at)
				}
			case isGitHubKind(c.Trigger.Kind):
				a, ok := r.answerOf(fleet, c.Trigger.Subject)
				if !ok || !a.judges(c) {
					continue
				}
				switch what, why := ghVerdict(c.Trigger.Kind, a); what {
				case verdictFire:
					fire = true
				case verdictDrop:
					r.drop(fleet, c, why)
				}
			case c.Trigger.Kind == ledger.TriggerNone && !c.Shown:
				// Only the engineer cancels it: the card asks once and the commitment stays open.
				if at := c.TS.Add(agedAfter); now.Before(at) {
					earlier(at)
				} else if r.post(fleet, c, OutcomeAged, "", "") == nil {
					_ = r.Ledger.MarkShown(fleet, c.ID)
				}
			case c.Trigger.Kind == ledger.TriggerShipSailed || c.Trigger.Kind == ledger.TriggerShipAnchored:
				if log == nil {
					log = r.readLog(list)
				}
				v := log.verdict(c, now)
				earlier(v.recheck)
				earlier(now.Add(pollEvery))
				switch v.what {
				case verdictFire:
					fire = true
				case verdictDrop:
					r.drop(fleet, c, v.why)
				}
			}
			if fire {
				r.fire(ctx, fleet, c)
			}
		}
	}
	r.pruneLogs()
	return next
}

func (r *Runner) post(fleet string, c ledger.Commitment, outcome, ship, why string) error {
	return r.Cards.PostCard(fleet, Card{ID: "commitment-" + c.ID + "-" + outcome, Commitment: c.ID, Text: c.Text, Outcome: outcome, Ship: ship, Why: why})
}

func (r *Runner) drop(fleet string, c ledger.Commitment, why string) {
	if r.post(fleet, c, OutcomeDropped, "", why) == nil {
		_ = r.Ledger.Drop(fleet, c.ID, why)
	}
}

func (r *Runner) cantRun(fleet string, c ledger.Commitment, why string) {
	if r.post(fleet, c, OutcomeCantRun, "", why) == nil {
		_ = r.Ledger.Done(fleet, c.ID, why)
	}
}

func (r *Runner) fire(ctx context.Context, fleet string, c ledger.Commitment) {
	if c.Action != ledger.ActionLaunch {
		if r.post(fleet, c, OutcomeReminded, "", "") == nil {
			_ = r.Ledger.Done(fleet, c.ID, "reminded")
		}
		return
	}
	key := "commitment:" + c.ID
	if held, ok := r.Ships.Holder(key); ok {
		r.launched(fleet, c, held)
		return
	}
	var req launcher.Request
	if err := json.Unmarshal(c.Launch, &req); err != nil {
		r.cantRun(fleet, c, "its stored launch could not be read: "+err.Error())
		return
	}
	if req.Fleet != fleet {
		r.cantRun(fleet, c, "its launch belongs to another fleet")
		return
	}
	if req.Source != "" {
		if own, ok := r.SourceFleet(req.Source); !ok || own != fleet {
			r.cantRun(fleet, c, "its order source "+req.Source+" is not in this fleet")
			return
		}
	}
	req.Via, req.CommitmentKey, req.Autopilot, req.QuietGate, req.Unattended = c.Via, key, true, true, true
	// To AdmiralBot a ship of another fleet is only that fleet, so a refusal's text names no foreign ship.
	req.ByFleet = fleet
	// The launcher runs this under its lock, so a cancel that lands before it launches nothing.
	cancelled := false
	req.Before = func(string) error {
		if r.open(fleet, c.ID) {
			return nil
		}
		cancelled = true
		return errors.New("the commitment was cancelled")
	}
	ship, lerr := r.Ships.Launch(ctx, req)
	switch {
	case lerr != nil && cancelled:
	case lerr != nil && lerr.Wait != "":
		if !c.Waiting || !sameWait(c.WaitReason, lerr.Wait) {
			_ = r.Ledger.MarkFired(fleet, c.ID, lerr.Wait)
		}
	case lerr != nil:
		r.cantRun(fleet, c, lerr.Msg)
	default:
		r.launched(fleet, c, ship)
	}
}

// usedPercent is the figure in a quota wait that moves every refresh without the reason changing.
var usedPercent = regexp.MustCompile(` is at \d+%`)

// sameWait says two wait reasons differ only in a quota figure, so the ledger keeps the first rather than logging each refresh.
func sameWait(stored, now string) bool {
	return usedPercent.ReplaceAllString(stored, " is at N%") == usedPercent.ReplaceAllString(now, " is at N%")
}

func (r *Runner) open(fleet, id string) bool {
	list, err := r.Ledger.Commitments(fleet)
	if err != nil {
		return false
	}
	for _, c := range list {
		if c.ID == id {
			return true
		}
	}
	return false
}

func (r *Runner) launched(fleet string, c ledger.Commitment, ship ShipRef) {
	// The ship exists, so the commitment closes even when its card cannot be written: otherwise the next tick launches again.
	if err := r.post(fleet, c, OutcomeLaunched, ship.Session, ""); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: commitment %s launched %s but its chat card cannot be written: %v\n", c.ID, ship.Session, err)
	}
	name := ship.Name
	if name == "" {
		name = ship.Session
	}
	// A cancel that landed while the launch ran leaves nothing to close: the ship is there.
	_ = r.Ledger.Done(fleet, c.ID, "launched "+name)
}
