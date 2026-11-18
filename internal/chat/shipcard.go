package chat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/ships"
)

// The results a ship card carries.
const (
	ShipSailedOff    = "sailed_off"
	ShipAnchored     = "anchored"
	ShipLost         = "lost"
	ShipMoored       = "moored"
	ShipReadyToMerge = "ready_to_merge"
	ShipReturned     = "returned"
)

// ShipSettleGrace is how long a clean end waits for a moor before the ship counts as sailed off; the ships service moors a ship with open PRs a few seconds after it ends.
const ShipSettleGrace = 15 * time.Second

// ShipCardPR is a PR a ship card links to.
type ShipCardPR struct {
	URL    string
	Number int
}

// ShipCard is the card a ship's result posts in its fleet's chat.
type ShipCard struct {
	Ship          string // the ship's session name, which names its conversation
	Name          string // the ship's display name
	Title         string
	FirstLaunched string // with Ship, the voyage: a reused name starts a new one
	Skill         string
	SkillKind     string
	Result        string
	PRs           []ShipCardPR
}

// PostShipCard appends the card to the fleet's chat log, once per voyage and result. It starts no turn and writes no digest line.
func (s *Service) PostShipCard(fleet string, c ShipCard) error {
	type pr struct {
		URL    string `json:"url"`
		Number int    `json:"number"`
	}
	prs := make([]pr, 0, len(c.PRs))
	for _, p := range c.PRs {
		prs = append(prs, pr{p.URL, p.Number})
	}
	body := map[string]any{
		"ship": c.Ship, "name": c.Name, "first_launched": c.FirstLaunched, "fleet": fleet, "skill": c.Skill, "skill_kind": c.SkillKind, "result": c.Result, "prs": prs,
	}
	if c.Title != "" {
		body["title"] = c.Title
	}
	card, err := json.Marshal(body)
	if err != nil {
		return err
	}
	id := c.id()
	_, err = s.history().Append(fleet, chatlog.Entry{ID: id, Kind: chatlog.KindShipCard, Text: c.Ship, Card: card})
	return err
}

// ShipRecord is one outcome-log line about a ship.
type ShipRecord struct {
	At       time.Time
	Ship     string
	Event    string // ended, moored, unmoored, returning, stopped
	Reason   string // an unmoored line's reason
	Clean    bool   // an ended line's exit was clean
	Returned bool   // a stopped line ended a return to port
	Scout    bool   // a stopped line ended a scout that ran to its end
}

// voyage is what the detector keeps of one ship's voyage: who it is, which cards it has settled and the record times that
// decide the ones still pending. The outcome log names a ship only by session, so identity comes from the snapshot.
type voyage struct {
	Ship          string
	Name          string
	Title         string
	Fleet         string
	Via           string
	Skill         string
	SkillKind     string
	Scout         bool
	Commitment    string
	Routine       string
	Round         int  // the highest round seen: a feedback round or a wake keeps the voyage
	Reported      bool // its report turn is queued, or its first event passed without one
	FirstLaunched string
	PRs           []ShipCardPR
	Done          map[string]bool
	EndedAt       time.Time // the latest clean end
	MooredAt      time.Time
	UnmooredAt    time.Time // the PRs all closed
	StoppedAt     time.Time
	ScoutEndedAt  time.Time // a scout's own end
	ReturnedAt    time.Time
	ReturningAt   time.Time
	ReadyHeld     bool // moored at the seed: its first answered poll is not news
	LastSeen      time.Time
}

type shipCardState struct {
	Seeded   bool
	Scouts   bool // saved by a build that reads a scout's stop
	Voyages  map[string]*voyage
	lastSent []byte
}

// ShipCards works out which ship cards are due from the ship snapshot and the outcome log, and keeps what it has seen under home.
type ShipCards struct {
	mu     sync.Mutex
	path   string
	post   func(fleet string, c ShipCard) error
	report func(fleet string, c ShipCard) error
	st     shipCardState
}

// NewShipCards reads its seen state from home, so a restart repeats nothing.
func NewShipCards(home string, post func(fleet string, c ShipCard) error) *ShipCards {
	d := &ShipCards{path: filepath.Join(home, "ship-cards.json"), post: post}
	if b, err := os.ReadFile(d.path); err == nil {
		var saved struct {
			Seeded  bool
			Scouts  bool
			Voyages map[string]*voyage
		}
		if json.Unmarshal(b, &saved) == nil {
			d.st.Seeded, d.st.Scouts, d.st.Voyages, d.st.lastSent = saved.Seeded, saved.Scouts, saved.Voyages, b
		}
	}
	if d.st.Voyages == nil {
		d.st.Voyages = map[string]*voyage{}
	}
	return d
}

const voyageKeepFor = 30 * 24 * time.Hour

var pullNumber = regexp.MustCompile(`/pull/(\d+)`)

// Observe posts the card each event in the snapshot and records is due. On the first pass of a home it posts nothing for
// ships already moored or anchored.
func (d *ShipCards) Observe(snap []ships.Ship, records []ShipRecord, now time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	seeding := !d.st.Seeded
	var errs []error
	mooredNow, inSnap := map[*voyage]bool{}, map[*voyage]bool{}
	for _, s := range snap {
		if s.Fleet == "" {
			continue
		}
		v := d.voyageOf(s, now)
		if v == nil {
			continue
		}
		inSnap[v] = true
		switch s.Status {
		case ships.StatusMoored:
			mooredNow[v] = true
			errs = append(errs, d.emit(v, ShipMoored, seeding))
			if seeding {
				v.ReadyHeld = true
			}
			if s.PolledAt == "" {
				break
			}
			held := v.ReadyHeld
			v.ReadyHeld = false
			if s.Via == "admiralbot" && s.StackReady {
				errs = append(errs, d.emit(v, ShipReadyToMerge, seeding || held))
			}
		case ships.StatusAnchored:
			errs = append(errs, d.emit(v, ShipAnchored, seeding))
		}
	}
	if !seeding {
		slices.SortStableFunc(records, func(a, b ShipRecord) int { return a.At.Compare(b.At) })
		for _, r := range records {
			if v := d.voyageAt(r.Ship, r.At); v != nil {
				v.apply(r)
			}
		}
		for _, v := range d.st.Voyages {
			// An older build read a scout's stop as a plain one, so a stop already logged was never news.
			if !d.st.Scouts && !v.ScoutEndedAt.IsZero() {
				v.Done[ShipSailedOff] = true
			}
			errs = append(errs, d.settle(v, mooredNow[v], inSnap[v], now))
		}
	}
	for k, v := range d.st.Voyages {
		if now.Sub(v.LastSeen) > voyageKeepFor {
			delete(d.st.Voyages, k)
		}
	}
	d.st.Seeded, d.st.Scouts = true, true
	errs = append(errs, d.save())
	return errors.Join(errs...)
}

func (d *ShipCards) voyageOf(s ships.Ship, now time.Time) *voyage {
	stamp := s.FirstLaunched
	if stamp == "" {
		stamp = s.Launched
	}
	if _, err := time.Parse(time.RFC3339, stamp); err != nil {
		return nil
	}
	key := s.Session + "|" + stamp
	v := d.st.Voyages[key]
	if v == nil {
		v = &voyage{Ship: s.Session, Name: s.Name, FirstLaunched: stamp, Done: map[string]bool{}}
		d.st.Voyages[key] = v
	}
	v.Name, v.Fleet, v.Via, v.SkillKind, v.Scout, v.LastSeen = s.Name, s.Fleet, s.Via, s.SkillKind, s.Scout, now
	v.Commitment, v.Routine, v.Round = s.Commitment, s.Routine, max(v.Round, s.Round)
	if s.Titled { // before that a ship's title is its prompt's first line
		v.Title = s.Title
	}
	if s.Skill != nil {
		v.Skill = *s.Skill
	}
	if len(s.PRs) > 0 {
		v.PRs = v.PRs[:0:0]
		for _, p := range s.PRs {
			v.PRs = append(v.PRs, ShipCardPR{URL: p.URL, Number: prNumber(p.URL)})
		}
	}
	return v
}

// voyageAt is the voyage of the ship that a record at the time belongs to: the latest one launched by then, since a name is reused.
func (d *ShipCards) voyageAt(ship string, at time.Time) *voyage {
	var best *voyage
	var bestStart time.Time
	for _, v := range d.st.Voyages {
		if v.Ship != ship {
			continue
		}
		start, _ := time.Parse(time.RFC3339, v.FirstLaunched)
		if start.After(at) || best != nil && !start.After(bestStart) {
			continue
		}
		best, bestStart = v, start
	}
	return best
}

func (v *voyage) apply(r ShipRecord) {
	switch r.Event {
	case "ended":
		if r.Clean && r.At.After(v.EndedAt) {
			v.EndedAt = r.At
		}
	case "moored":
		if r.At.After(v.MooredAt) {
			v.MooredAt = r.At
		}
	case "unmoored":
		switch r.Reason {
		case "merged", "closed", "prs_done":
			if v.UnmooredAt.IsZero() {
				v.UnmooredAt = r.At
			}
		}
	case "returning":
		if r.At.After(v.ReturningAt) {
			v.ReturningAt = r.At
		}
	case "stopped":
		if r.Returned {
			v.ReturnedAt = r.At
		} else if r.Scout {
			v.Scout = true
			if r.At.After(v.ScoutEndedAt) {
				v.ScoutEndedAt = r.At
			}
		} else if r.At.After(v.StoppedAt) {
			v.StoppedAt = r.At
		}
	}
}

// settle posts the cards a voyage's records have made due. Each sail-off waits out ShipSettleGrace: the ship's own next
// record, a moor or the `returning` of a return to port, can follow its trigger by a second or more.
func (d *ShipCards) settle(v *voyage, mooredNow, inSnap bool, now time.Time) error {
	var errs []error
	if !v.ReturnedAt.IsZero() {
		errs = append(errs, d.emit(v, ShipReturned, false))
		return errors.Join(errs...)
	}
	switch {
	case !v.ScoutEndedAt.IsZero():
		// A scout's end is a kill, so only its own `stopped` mark tells it from an engineer's Stop ship.
		if !inSnap && now.Sub(v.ScoutEndedAt) >= ShipSettleGrace {
			errs = append(errs, d.emit(v, ShipSailedOff, false))
		}
	case !v.UnmooredAt.IsZero():
		// A return to port writes `unmoored` and then `returning`; the ship is not sailing off.
		if v.ReturningAt.Before(v.UnmooredAt) && now.Sub(v.UnmooredAt) >= ShipSettleGrace {
			errs = append(errs, d.emit(v, ShipSailedOff, false))
		}
	case !v.EndedAt.IsZero() && v.MooredAt.Before(v.EndedAt) && !mooredNow && !inSnap && (v.StoppedAt.IsZero() || v.StoppedAt.Before(v.EndedAt)):
		// A clean-ended ship stays in the snapshot until settle has moored it or let it sail off.
		returning := !v.ReturningAt.IsZero() && now.Sub(v.ReturningAt) < ShipSettleGrace
		if now.Sub(v.EndedAt) >= ShipSettleGrace && !returning {
			errs = append(errs, d.emit(v, ShipSailedOff, false))
		}
	}
	return errors.Join(errs...)
}

// emit posts the card unless the voyage has settled that result; silent only records it. A failed post is tried again by the next pass.
func (d *ShipCards) emit(v *voyage, result string, silent bool) error {
	if v.Done[result] {
		return nil
	}
	reports := v.reportsOn(result)
	if reports && silent {
		v.Reported = true
	}
	if !silent {
		card := ShipCard{Ship: v.Ship, Name: v.Name, Title: v.Title, FirstLaunched: v.FirstLaunched, Skill: v.Skill, SkillKind: v.cardKind(), Result: result, PRs: v.PRs}
		if reports && d.report != nil {
			if err := d.report(v.Fleet, card); err != nil {
				return err
			}
			v.Reported = true
		}
		if err := d.post(v.Fleet, ShipCard{Ship: v.Ship, Name: v.Name, Title: v.Title, FirstLaunched: v.FirstLaunched, Skill: v.Skill, SkillKind: v.cardKind(), Result: result, PRs: v.PRs}); err != nil {
			return err
		}
	}
	v.Done[result] = true
	return nil
}

// reportsOn says whether the result is a voyage's first reporting event. Only the ships AdmiralBot launched from the
// chat report (scouts have their own report), and a sail-off after a moor is the end, which posts its card only.
func (v *voyage) reportsOn(result string) bool {
	if v.Reported || v.Via != "admiralbot" || v.Commitment != "" || v.Routine != "" || v.Scout || v.Round > 0 {
		return false
	}
	switch result {
	case ShipAnchored, ShipMoored:
		return true
	case ShipSailedOff:
		return !v.Done[ShipMoored] && v.UnmooredAt.IsZero()
	}
	return false
}

// cardKind is the kind the card reads, decided here so every client agrees.
func (v *voyage) cardKind() string {
	switch {
	case v.Scout:
		return "scout"
	case v.SkillKind == "commodore":
		return v.SkillKind
	case v.Via == "admiralbot" && v.Skill == "":
		return "quick"
	case v.SkillKind == "":
		if v.Skill != "" {
			return "skill"
		}
		return "none"
	}
	return v.SkillKind
}

func (d *ShipCards) save() error {
	b, err := json.Marshal(struct {
		Seeded  bool
		Scouts  bool
		Voyages map[string]*voyage
	}{d.st.Seeded, d.st.Scouts, d.st.Voyages})
	if err != nil || slices.Equal(b, d.st.lastSent) {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, d.path); err != nil {
		return err
	}
	d.st.lastSent = b
	return nil
}

func prNumber(url string) int {
	if m := pullNumber.FindStringSubmatch(url); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}
