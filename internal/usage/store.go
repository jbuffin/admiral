package usage

import (
	"sync"
	"time"
)

// Bounds keep a long run's memory flat: the store holds the figures of recent ships only.
const (
	maxStoreShips     = 512
	maxSessionsPerKey = 4
)

type sessionFigures struct {
	id   string
	fig  Figures
	seen time.Time
}

type shipFigures struct {
	sessions []sessionFigures // oldest first
	touched  time.Time
	// consumed holds the sessions Forget dropped, with the reading's time: a pane keeps its last line, and
	// reading it again must not bring the session back.
	consumed map[string]time.Time
}

type quotaEntry struct {
	quota Quota
	seen  time.Time
}

// Store holds the latest live figures in memory: per ship, and per harness login for the quota. Nothing is
// written to disk, so a restart starts empty.
type Store struct {
	mu     sync.Mutex
	now    func() time.Time
	ships  map[string]*shipFigures
	quotas map[string]quotaEntry
}

// NewStore reads the clock through now, which tests replace.
func NewStore(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{now: now, ships: map[string]*shipFigures{}, quotas: map[string]quotaEntry{}}
}

func cloneQuota(q Quota) Quota {
	if q.Primary != nil {
		w := *q.Primary
		q.Primary = &w
	}
	if q.Secondary != nil {
		w := *q.Secondary
		q.Secondary = &w
	}
	if q.Spend != nil {
		sp := *q.Spend
		q.Spend = &sp
	}
	if q.Credits != nil {
		c := *q.Credits
		q.Credits = &c
	}
	return q
}

func cloneFigures(f Figures) Figures {
	if f.Money != nil {
		m := *f.Money
		if m.PremiumRequestCost != nil {
			p := *m.PremiumRequestCost
			m.PremiumRequestCost = &p
		}
		f.Money = &m
	}
	if f.Quota != nil {
		q := cloneQuota(*f.Quota)
		f.Quota = &q
	}
	return f
}

// isZero looks at the money when a source reports it: a new session's first line can carry the context window's
// tokens with nothing spent yet.
func (f Figures) isZero() bool {
	if f.Money != nil {
		return f.Money.Amount == 0
	}
	return f.Tokens == (Tokens{})
}

// SetFigures records what a live source reported for a session of a ship. A session id the ship has not
// reported before starts fresh; the earlier sessions' last figures stay for LastLive.
func (s *Store) SetFigures(ship, sessionID string, f Figures, seen time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf := s.ships[ship]
	if sf == nil {
		if len(s.ships) >= maxStoreShips {
			s.evictLocked()
		}
		sf = &shipFigures{}
		s.ships[ship] = sf
	}
	sf.touched = s.now()
	if at, gone := sf.consumed[sessionID]; gone && !seen.After(at) {
		return
	}
	f = cloneFigures(f)
	for i := range sf.sessions {
		if sf.sessions[i].id == sessionID {
			sf.sessions[i].fig, sf.sessions[i].seen = f, seen
			return
		}
	}
	sf.sessions = append(sf.sessions, sessionFigures{sessionID, f, seen})
	if len(sf.sessions) > maxSessionsPerKey {
		sf.sessions = sf.sessions[len(sf.sessions)-maxSessionsPerKey:]
	}
}

func (s *Store) evictLocked() {
	var oldest string
	var at time.Time
	for name, sf := range s.ships {
		if oldest == "" || sf.touched.Before(at) {
			oldest, at = name, sf.touched
		}
	}
	delete(s.ships, oldest)
}

// Figures answers with the latest session's figures, even when they are zero, and how old they are.
func (s *Store) Figures(ship string) (Figures, time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf := s.ships[ship]
	if sf == nil || len(sf.sessions) == 0 {
		return Figures{}, 0, false
	}
	last := sf.sessions[len(sf.sessions)-1]
	return cloneFigures(last.fig), s.now().Sub(last.seen), true
}

// LastLive answers with the latest session that has spent anything. A /clear starts a session whose first
// line costs nothing, and the reset record is about the session that ended.
func (s *Store) LastLive(ship string) (Figures, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf := s.ships[ship]
	if sf == nil {
		return Figures{}, false
	}
	for i := len(sf.sessions) - 1; i >= 0; i-- {
		if !sf.sessions[i].fig.isZero() {
			return cloneFigures(sf.sessions[i].fig), true
		}
	}
	return Figures{}, false
}

// Forget drops a ship's sessions once a record has taken their figures, so the next round or a new ship of the
// same name starts from nothing. LastLive never answers twice with the same session.
func (s *Store) Forget(ship string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf := s.ships[ship]
	if sf == nil {
		return
	}
	if sf.consumed == nil || len(sf.consumed) >= 2*maxSessionsPerKey {
		sf.consumed = map[string]time.Time{}
	}
	for _, se := range sf.sessions {
		sf.consumed[se.id] = se.seen
	}
	sf.sessions = nil
}

// SetQuota replaces a login's quota as the source reported it.
func (s *Store) SetQuota(login string, q Quota, seen time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.quotas[login] = quotaEntry{cloneQuota(q), seen}
}

// Quota answers with the login's last-seen windows and their age. A window past its reset time reads as
// unused: it has begun a new one.
func (s *Store) Quota(login string) (Quota, time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.quotas[login]
	if !ok {
		return Quota{}, 0, false
	}
	q := cloneQuota(e.quota)
	now := s.now()
	passed := func(resetsAt int64) bool { return resetsAt > 0 && !now.Before(time.Unix(resetsAt, 0)) }
	for _, w := range []*Window{q.Primary, q.Secondary} {
		if w != nil && passed(w.ResetsAt) {
			w.UsedPercent = 0
		}
	}
	if q.Spend != nil && passed(q.Spend.ResetsAt) {
		q.Spend.UsedPercent, q.Spend.UsedUSD = 0, 0
	}
	return q, now.Sub(e.seen), true
}
