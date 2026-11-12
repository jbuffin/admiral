// Package usageapi serves the usage totals and quota the Usage views read: the outcome log's `usage` and
// `advised` records, plus the live store's figures for ships still at sea. It writes nothing.
package usageapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/usage"
)

type Service struct {
	Home   string
	Config func() *config.Config
	Live   *usage.Store
	Ships  func() []ships.Ship
	Now    func() time.Time
	Zone   *time.Location
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/usage", s.serveUsage)
	mux.HandleFunc("GET /api/ships/{session}/usage", s.serveShip)
}

func (s *Service) Connected(*server.Client) {}

var periodNames = []string{"today", "7d", "30d", "month"}

// periodStarts are local-time starts: today is from local midnight, 7d and 30d add the 6 and 29 days before it.
func periodStarts(now time.Time, zone *time.Location) map[string]time.Time {
	n := now.In(zone)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, zone)
	return map[string]time.Time{
		"today": today,
		"7d":    today.AddDate(0, 0, -6),
		"30d":   today.AddDate(0, 0, -29),
		"month": time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, zone),
	}
}

type recTokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

type recMoney struct {
	Amount     float64  `json:"amount"`
	Basis      string   `json:"basis"`
	Incomplete bool     `json:"incomplete"`
	Premium    *float64 `json:"premium_requests"`
}

type record struct {
	Ts      time.Time  `json:"ts"`
	Ship    string     `json:"ship"`
	Event   string     `json:"event"`
	Fleet   string     `json:"fleet"`
	Harness string     `json:"harness"`
	Tokens  *recTokens `json:"tokens"`
	Money   *recMoney  `json:"money"`
	Cost    *float64   `json:"cost"`
}

func (r record) figures() usage.Figures {
	var f usage.Figures
	if r.Tokens != nil {
		f.Tokens = usage.Tokens{Input: r.Tokens.Input, Output: r.Tokens.Output, Reasoning: r.Tokens.Reasoning,
			CacheRead: r.Tokens.CacheRead, CacheWrite: r.Tokens.CacheWrite}
	}
	if r.Money != nil {
		f.Money = &usage.Money{Amount: r.Money.Amount, Basis: usage.Basis(r.Money.Basis), Incomplete: r.Money.Incomplete, PremiumRequestCost: r.Money.Premium}
	}
	return f
}

var (
	usageMark    = []byte(`"event":"usage"`)
	advisedMark  = []byte(`"event":"advised"`)
	launchedMark = []byte(`"event":"launched"`)
)

// scan calls fn with every record at or after from whose line holds one of marks. A line is judged on its
// first chunk, before decoding: an `ended` line can be megabytes.
func (s *Service) scan(from time.Time, marks [][]byte, fn func(record)) {
	files, err := outcomes.MonthFiles(filepath.Join(s.Home, "outcomes"))
	if err != nil {
		return
	}
	for _, mf := range files {
		if end := mf.End(); !end.IsZero() && !end.After(from) {
			continue
		}
		scanFile(mf.Path, from, marks, fn)
	}
}

func hasMark(b []byte, marks [][]byte) bool {
	for _, m := range marks {
		if bytes.Contains(b, m) {
			return true
		}
	}
	return false
}

func scanFile(path string, from time.Time, marks [][]byte, fn func(record)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		chunk, err := r.ReadSlice('\n')
		line := chunk
		if err == bufio.ErrBufferFull {
			line, err = longLine(r, chunk, marks)
		}
		if len(line) > 0 && hasMark(line, marks) {
			var rec record
			if json.Unmarshal(line, &rec) == nil && !rec.Ts.Before(from) {
				fn(rec)
			}
		}
		if err != nil {
			return
		}
	}
}

// longLine finishes a line that overran the buffer: it is kept only when its first chunk holds a mark and it
// fits outcomes.MaxLine, and otherwise read through unbuffered.
func longLine(r *bufio.Reader, first []byte, marks [][]byte) ([]byte, error) {
	keep := hasMark(first, marks)
	var line []byte
	if keep {
		line = append(line, first...)
	}
	for {
		chunk, err := r.ReadSlice('\n')
		if keep {
			if len(line)+len(chunk) > outcomes.MaxLine+1 {
				keep, line = false, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if err != bufio.ErrBufferFull {
			return bytes.TrimSuffix(line, []byte("\n")), err
		}
	}
}

// acc sums one spender's figures. Money adds within one basis; a record with none makes the sum incomplete.
type acc struct {
	tokens    usage.Tokens
	hasTokens bool
	money     *usage.Money
	noMoney   bool
	// Premium requests are summed here, not in money: usage.Money.Add drops them when one side lacks them.
	premium        float64
	hasPremium     bool
	premiumMissing bool
}

func (a *acc) add(f usage.Figures, hasTokens bool) {
	if hasTokens {
		a.tokens, a.hasTokens = a.tokens.Add(f.Tokens), true
	}
	a.addMoney(f.Money)
}

func (a *acc) addMoney(m *usage.Money) {
	if m == nil {
		a.noMoney = true
		return
	}
	if p := m.PremiumRequestCost; p != nil {
		a.premium, a.hasPremium = a.premium+*p, true
	} else {
		a.premiumMissing = true
	}
	a.sumMoney(*m)
}

func (a *acc) sumMoney(m usage.Money) {
	m.PremiumRequestCost = nil
	if a.money == nil {
		a.money = &m
	} else if sum, ok := a.money.Add(m); ok {
		a.money = &sum
	} else {
		a.money.Incomplete = true
	}
}

func (a *acc) merge(o *acc) {
	if o == nil {
		return
	}
	if o.hasTokens {
		a.tokens, a.hasTokens = a.tokens.Add(o.tokens), true
	}
	if o.money != nil {
		a.sumMoney(*o.money)
	}
	if o.hasPremium {
		a.premium, a.hasPremium = a.premium+o.premium, true
	}
	a.premiumMissing = a.premiumMissing || o.premiumMissing
	a.noMoney = a.noMoney || o.noMoney
}

// round keeps float sums from printing as 0.12000000000000001.
func round(x float64) float64 { return math.Round(x*1e9) / 1e9 }

type tokensOut struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

type moneyOut struct {
	Amount          float64  `json:"amount"`
	Basis           string   `json:"basis"`
	Incomplete      bool     `json:"incomplete"`
	PremiumRequests *float64 `json:"premium_requests,omitempty"`
}

func (a *acc) tokensOut() *tokensOut {
	if a == nil || !a.hasTokens {
		return nil
	}
	t := a.tokens
	return &tokensOut{t.Input, t.Output, t.Reasoning, t.CacheRead, t.CacheWrite}
}

func (a *acc) moneyOut() *moneyOut {
	if a == nil || a.money == nil {
		return nil
	}
	out := &moneyOut{Amount: round(a.money.Amount), Basis: string(a.money.Basis), Incomplete: a.money.Incomplete || a.noMoney || (a.hasPremium && a.premiumMissing)}
	if a.hasPremium {
		v := round(a.premium)
		out.PremiumRequests = &v
	}
	return out
}

type spenderOut struct {
	Tokens *tokensOut `json:"tokens"`
	Money  *moneyOut  `json:"money"`
}

func (a *acc) spenderOut() *spenderOut {
	if a == nil {
		return nil
	}
	return &spenderOut{a.tokensOut(), a.moneyOut()}
}

type spenders struct {
	ships, chat, workers *acc
}

type harnessOut struct {
	Harness string `json:"harness"`
	*spenderOut
	Spenders struct {
		Ships   *spenderOut `json:"ships,omitempty"`
		Chat    *spenderOut `json:"chat,omitempty"`
		Workers *spenderOut `json:"workers,omitempty"`
	} `json:"spenders"`
}

type periodOut struct {
	From      string       `json:"from"`
	Harnesses []harnessOut `json:"harnesses"`
}

type windowOut struct {
	ID            string   `json:"id"`
	UsedPercent   float64  `json:"used_percent"`
	WindowMinutes int      `json:"window_minutes,omitempty"`
	ResetsAt      string   `json:"resets_at,omitempty"`
	UsedUSD       *float64 `json:"used_usd,omitempty"`
	LimitUSD      *float64 `json:"limit_usd,omitempty"`
	Period        string   `json:"period,omitempty"`
}

type quotaOut struct {
	Harness    string      `json:"harness"`
	Label      string      `json:"label"`
	HasData    bool        `json:"has_data"`
	AgeSeconds *int64      `json:"age_seconds,omitempty"`
	Windows    []windowOut `json:"windows"`
}

type fleetOut struct {
	Key     string                `json:"key"`
	Name    string                `json:"name"`
	Periods map[string]*periodOut `json:"periods"`
	Quota   []quotaOut            `json:"quota"`
}

func (a *acc) hasAny() bool { return a != nil && (a.hasTokens || a.money != nil || a.noMoney) }

// build is one fleet's periods from the buckets[period][harness].
func build(starts map[string]time.Time, buckets map[string]map[string]*spenders) map[string]*periodOut {
	out := map[string]*periodOut{}
	for _, p := range periodNames {
		po := &periodOut{From: starts[p].Format(time.RFC3339), Harnesses: []harnessOut{}}
		names := make([]string, 0, len(buckets[p]))
		for h := range buckets[p] {
			names = append(names, h)
		}
		sort.Strings(names)
		for _, h := range names {
			sp := buckets[p][h]
			var total acc
			total.merge(sp.ships)
			total.merge(sp.chat)
			total.merge(sp.workers)
			ho := harnessOut{Harness: h, spenderOut: total.spenderOut()}
			if sp.ships.hasAny() {
				ho.Spenders.Ships = sp.ships.spenderOut()
			}
			if sp.chat.hasAny() {
				ho.Spenders.Chat = sp.chat.spenderOut()
			}
			if sp.workers.hasAny() {
				ho.Spenders.Workers = sp.workers.spenderOut()
			}
			po.Harnesses = append(po.Harnesses, ho)
		}
		out[p] = po
	}
	return out
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) zone() *time.Location {
	if s.Zone != nil {
		return s.Zone
	}
	return time.Local
}

func (s *Service) fleets() []config.Fleet {
	if s.Config == nil {
		return nil
	}
	return s.Config().Fleets
}

// aggregate reads the log once for every fleet in want.
func (s *Service) aggregate(want []config.Fleet) []fleetOut {
	now := s.now()
	starts := periodStarts(now, s.zone())
	earliest := starts["30d"]
	if starts["month"].Before(earliest) {
		earliest = starts["month"]
	}
	buckets := map[string]map[string]map[string]*spenders{}
	for _, f := range want {
		b := map[string]map[string]*spenders{}
		for _, p := range periodNames {
			b[p] = map[string]*spenders{}
		}
		buckets[f.Key] = b
	}
	slot := func(fleet, period, harness string) *spenders {
		b, ok := buckets[fleet]
		if !ok {
			return nil
		}
		sp := b[period][harness]
		if sp == nil {
			sp = &spenders{&acc{}, &acc{}, &acc{}}
			b[period][harness] = sp
		}
		return sp
	}
	s.scan(earliest, [][]byte{usageMark, advisedMark}, func(r record) {
		if r.Event != "usage" && r.Event != "advised" {
			return
		}
		if r.Harness == "" || buckets[r.Fleet] == nil {
			return
		}
		var f usage.Figures
		var pick func(*spenders) *acc
		hasTokens := false
		switch {
		case r.Event == "advised":
			if r.Cost == nil {
				return
			}
			f = usage.Figures{Money: &usage.Money{Amount: *r.Cost, Basis: usage.BasisEstimate}}
			pick = func(sp *spenders) *acc { return sp.workers }
		case r.Ship == "_chat":
			f, hasTokens = r.figures(), r.Tokens != nil
			pick = func(sp *spenders) *acc { return sp.chat }
		default:
			f, hasTokens = r.figures(), r.Tokens != nil
			pick = func(sp *spenders) *acc { return sp.ships }
		}
		for _, p := range periodNames {
			if !r.Ts.Before(starts[p]) {
				pick(slot(r.Fleet, p, r.Harness)).add(f, hasTokens)
			}
		}
	})
	if s.Live != nil && s.Ships != nil {
		for _, sh := range s.Ships() {
			fig, _, ok := s.Live.Figures(sh.Session)
			if !ok || sh.Harness == "" || (fig.Tokens == (usage.Tokens{}) && fig.Money == nil) {
				continue
			}
			for _, p := range periodNames {
				if sp := slot(sh.Fleet, p, sh.Harness); sp != nil {
					sp.ships.add(fig, true)
				}
			}
		}
	}
	out := make([]fleetOut, 0, len(want))
	for _, f := range want {
		out = append(out, fleetOut{Key: f.Key, Name: f.Name, Periods: build(starts, buckets[f.Key]), Quota: s.quota(f)})
	}
	return out
}

// quota is one entry per login of the fleet's docks that has a plan quota. The label never carries the login's path.
func (s *Service) quota(f config.Fleet) []quotaOut {
	out := []quotaOut{}
	for _, h := range f.Harnesses {
		if h.Name != "claude" && h.Name != "codex" {
			continue
		}
		q := quotaOut{Harness: h.Name, Label: h.Name, Windows: []windowOut{}}
		if s.Live != nil {
			if quota, age, ok := s.Live.Quota(config.IdentityDir(h.Name, h.Env)); ok {
				secs := int64(age.Seconds())
				q.HasData, q.AgeSeconds = true, &secs
				q.Windows = windows(quota)
			}
		}
		out = append(out, q)
	}
	return out
}

func resetAt(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

func windows(q usage.Quota) []windowOut {
	out := []windowOut{}
	for _, w := range []struct {
		id  string
		win *usage.Window
	}{{"primary", q.Primary}, {"secondary", q.Secondary}} {
		if w.win != nil {
			out = append(out, windowOut{ID: w.id, UsedPercent: w.win.UsedPercent, WindowMinutes: w.win.WindowMinutes, ResetsAt: resetAt(w.win.ResetsAt)})
		}
	}
	if sp := q.Spend; sp != nil {
		used, limit := sp.UsedUSD, sp.LimitUSD
		out = append(out, windowOut{ID: "spend", UsedPercent: sp.UsedPercent, ResetsAt: resetAt(sp.ResetsAt), UsedUSD: &used, LimitUSD: &limit, Period: sp.Period})
	}
	return out
}

func (s *Service) serveUsage(w http.ResponseWriter, r *http.Request) {
	fleets := s.fleets()
	if r.URL.Query().Has("fleet") {
		key := r.URL.Query().Get("fleet")
		for _, f := range fleets {
			if f.Key == key {
				writeJSON(w, http.StatusOK, s.aggregate([]config.Fleet{f})[0])
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fleets": s.aggregate(fleets)})
}

type shipOut struct {
	Session    string     `json:"session"`
	Harness    string     `json:"harness"`
	Source     string     `json:"source"`
	Tokens     *tokensOut `json:"tokens"`
	Money      *moneyOut  `json:"money"`
	AgeSeconds *int64     `json:"age_seconds,omitempty"`
	At         string     `json:"at,omitempty"`
}

// firstLaunch is the time of the name's last fresh `launched` record: a woken round writes `woken`, so that
// record is this ship's own first launch, and a ship of the same name before it is not this one.
func (s *Service) firstLaunch(name string) time.Time {
	files, err := outcomes.MonthFiles(filepath.Join(s.Home, "outcomes"))
	if err != nil {
		return time.Time{}
	}
	for i := len(files) - 1; i >= 0; i-- {
		var last time.Time
		scanFile(files[i].Path, time.Time{}, [][]byte{launchedMark}, func(r record) {
			if r.Event == "launched" && r.Ship == name && !r.Ts.Before(last) {
				last = r.Ts
			}
		})
		if !last.IsZero() {
			return last
		}
	}
	return time.Time{}
}

// serveShip sums the ship's `usage` records from its first launch on, every moored round's included, plus its
// live figures while it runs. A ship with no `launched` record counts from the start of the log.
func (s *Service) serveShip(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("session")
	var ship *ships.Ship
	if s.Ships != nil {
		for _, sh := range s.Ships() {
			if sh.Session == name {
				sh := sh
				ship = &sh
				break
			}
		}
	}
	if ship == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var total acc
	var latest time.Time
	records := 0
	s.scan(s.firstLaunch(name), [][]byte{usageMark}, func(rec record) {
		if rec.Event != "usage" || rec.Ship != name {
			return
		}
		total.add(rec.figures(), rec.Tokens != nil)
		records++
		if rec.Ts.After(latest) {
			latest = rec.Ts
		}
	})
	out := shipOut{Session: name, Harness: ship.Harness, Source: "none"}
	if records > 0 {
		out.Source, out.At = "record", latest.UTC().Format(time.RFC3339)
	}
	if s.Live != nil {
		if fig, age, ok := s.Live.Figures(name); ok {
			total.add(fig, true)
			secs := int64(age.Seconds())
			out.Source, out.AgeSeconds = "live", &secs
		}
	}
	out.Tokens, out.Money = total.tokensOut(), total.moneyOut()
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
