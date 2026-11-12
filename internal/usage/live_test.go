package usage

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func readTestdata(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseStatuslineKeptSample(t *testing.T) {
	r, ok := ParseStatusline(readTestdata(t, "testdata/claude-statusline-kept.json"))
	if !ok {
		t.Fatal("ok = false for the kept sample")
	}
	if r.SessionID != "5b1f0c2e-7a44-4a1e-9d0a-1c2d3e4f5a6b" {
		t.Errorf("session = %q", r.SessionID)
	}
	if want := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC); !r.Seen.Equal(want) {
		t.Errorf("seen = %v, want the kept line's own time %v", r.Seen, want)
	}
	if r.Figures == nil || r.Figures.Money == nil || r.Figures.Money.Amount != 1.2345 || r.Figures.Money.Basis != BasisEstimate {
		t.Fatalf("figures = %+v, want 1.2345 dollars as an estimate", r.Figures)
	}
	if got := r.Figures.Tokens; got.Input != 15234 || got.Output != 4521 {
		t.Errorf("tokens = %+v, want the context window's totals", got)
	}
	q := r.Quota
	if q == nil || q.Primary == nil || q.Secondary == nil || q.Spend == nil {
		t.Fatalf("quota = %+v, want five_hour, seven_day and spend_limit", q)
	}
	if *q.Primary != (Window{UsedPercent: 23.5, WindowMinutes: 300, ResetsAt: 1900000000}) {
		t.Errorf("five_hour = %+v", *q.Primary)
	}
	if *q.Secondary != (Window{UsedPercent: 41.2, WindowMinutes: 10080, ResetsAt: 1900500000}) {
		t.Errorf("seven_day = %+v", *q.Secondary)
	}
	if q.Spend.UsedPercent != 50 || q.Spend.ResetsAt != 1900200000 || q.Spend.UsedUSD != 12.5 || q.Spend.LimitUSD != 25 || q.Spend.Period != "monthly" {
		t.Errorf("spend_limit = %+v", *q.Spend)
	}
}

// The raw status-line JSON Claude sends has the same field names as the kept line.
func TestParseStatuslineRawSample(t *testing.T) {
	r, ok := ParseStatusline(readTestdata(t, "../cli/testdata/claude-statusline.json"))
	if !ok || r.Figures == nil || r.Figures.Money.Amount != 1.2345 || r.Quota == nil || r.Quota.Primary == nil {
		t.Fatalf("raw sample: ok=%v reading=%+v", ok, r)
	}
	if r.Quota.Spend != nil {
		t.Errorf("spend = %+v, want nil: the sample has none", r.Quota.Spend)
	}
}

func TestParseStatuslineMissingWindowsStayMissing(t *testing.T) {
	r, ok := ParseStatusline([]byte(`{"time":"2026-11-12T10:00:00Z","session_id":"s1","rate_limits":{"five_hour":{"used_percentage":10,"resets_at":1900000000}}}`))
	if !ok || r.Quota == nil || r.Quota.Primary == nil {
		t.Fatalf("ok=%v reading=%+v", ok, r)
	}
	if r.Quota.Secondary != nil || r.Quota.Spend != nil {
		t.Errorf("quota = %+v, want only the five-hour window", r.Quota)
	}
	if r.Figures != nil && r.Figures.Money != nil {
		t.Errorf("money = %+v, want none: the line has no cost", r.Figures.Money)
	}
}

func TestParseStatuslineWithoutRateLimitsHasNoQuota(t *testing.T) {
	r, ok := ParseStatusline([]byte(`{"time":"2026-11-12T10:00:00Z","session_id":"s1","cost":{"total_cost_usd":0}}`))
	if !ok || r.Quota != nil {
		t.Errorf("ok=%v quota=%+v, want a reading with no quota", ok, r.Quota)
	}
}

func TestParseStatuslineGarbageKeepsNothing(t *testing.T) {
	for _, in := range []string{``, `not json`, `[]`, `null`, `{}`, `{"rate_limits":"high"}`} {
		if r, ok := ParseStatusline([]byte(in)); ok {
			t.Errorf("%q: ok with %+v, want nothing kept", in, r)
		}
	}
}

func TestParseCodexTokenUsageRecordedFrame(t *testing.T) {
	thread, f, ok := ParseCodexTokenUsage(readTestdata(t, "testdata/codex-tokenusage-recorded.json"))
	if !ok || thread != "00000000-0000-4000-8000-0000000000a1" {
		t.Fatalf("ok=%v thread=%q", ok, thread)
	}
	// cached input sits inside inputTokens, reasoning inside outputTokens: the classes are disjoint
	want := Tokens{Input: 25020 - 12544, Output: 129 - 67, Reasoning: 67, CacheRead: 12544}
	if f.Tokens != want {
		t.Errorf("tokens = %+v, want %+v", f.Tokens, want)
	}
	if f.Money != nil || f.Quota != nil {
		t.Errorf("figures = %+v, want tokens only: Codex reports no money here", f)
	}
}

func TestParseCodexTokenUsageIgnoresOtherFramesAndGarbage(t *testing.T) {
	for _, in := range []string{``, `nope`, `{"method":"item/started","params":{}}`, `{"method":"thread/tokenUsage/updated","params":{}}`,
		string(readTestdata(t, "testdata/codex-ratelimits-unverified.json"))} {
		if _, _, ok := ParseCodexTokenUsage([]byte(in)); ok {
			t.Errorf("%q: ok, want nothing kept", in)
		}
	}
}

// UNVERIFIED sample: built from the documented app-server schema, not captured.
func TestParseCodexRateLimitsFrame(t *testing.T) {
	q, ok := ParseCodexRateLimits(readTestdata(t, "testdata/codex-ratelimits-unverified.json"))
	if !ok || q.Primary == nil || q.Secondary == nil {
		t.Fatalf("ok=%v quota=%+v", ok, q)
	}
	if *q.Primary != (Window{UsedPercent: 42, WindowMinutes: 300, ResetsAt: 1900000000}) {
		t.Errorf("primary = %+v", *q.Primary)
	}
	if *q.Secondary != (Window{UsedPercent: 8, WindowMinutes: 10080, ResetsAt: 1900500000}) {
		t.Errorf("secondary = %+v", *q.Secondary)
	}
	if q.PlanType != "plus" {
		t.Errorf("plan = %q", q.PlanType)
	}
}

func TestParseCodexRateLimitsMissingWindowStaysMissing(t *testing.T) {
	q, ok := ParseCodexRateLimits([]byte(`{"method":"account/rateLimits/updated","params":{"rateLimits":{"primary":{"usedPercent":5,"windowDurationMins":300,"resetsAt":1900000000},"secondary":null}}}`))
	if !ok || q.Primary == nil || q.Secondary != nil {
		t.Errorf("ok=%v quota=%+v, want only the primary window", ok, q)
	}
}

func TestParseCodexRateLimitsGarbageKeepsNothing(t *testing.T) {
	for _, in := range []string{``, `nope`, `{"method":"account/rateLimits/updated","params":{}}`,
		`{"method":"thread/tokenUsage/updated","params":{"rateLimits":{"primary":{"usedPercent":1}}}}`} {
		if q, ok := ParseCodexRateLimits([]byte(in)); ok {
			t.Errorf("%q: ok with %+v, want nothing kept", in, q)
		}
	}
}

// The store

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Unix(1800000000, 0)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func dollars(in int64, amount float64) Figures {
	return Figures{Tokens: Tokens{Input: in}, Money: &Money{Amount: amount, Basis: BasisEstimate}}
}

func TestStoreKnowsNothingYet(t *testing.T) {
	s := NewStore(newClock().now)
	if _, _, ok := s.Figures("a-1"); ok {
		t.Error("figures for a ship nothing reported")
	}
	if _, _, ok := s.Quota("/home/u/.claude"); ok {
		t.Error("quota for a login nothing reported")
	}
	if _, ok := s.LastLive("a-1"); ok {
		t.Error("last live figures for a ship nothing reported")
	}
}

func TestStoreNewerFiguresReplaceOlder(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	s.SetFigures("a-1", "sess-1", dollars(100, 0.5), c.now())
	s.SetFigures("a-1", "sess-1", dollars(250, 1.25), c.now())
	f, _, ok := s.Figures("a-1")
	if !ok || f.Tokens.Input != 250 || f.Money.Amount != 1.25 {
		t.Errorf("figures = %+v, ok=%v, want the latest report", f, ok)
	}
}

func TestStoreFiguresAgeComesFromTheClock(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	s.SetFigures("a-1", "sess-1", dollars(1, 0.1), c.now())
	c.advance(90 * time.Second)
	if _, age, _ := s.Figures("a-1"); age != 90*time.Second {
		t.Errorf("age = %v, want 90s", age)
	}
	s.SetFigures("a-1", "sess-1", dollars(2, 0.2), c.now())
	if _, age, _ := s.Figures("a-1"); age != 0 {
		t.Errorf("age = %v after a fresh report, want 0", age)
	}
}

// A report carries the time its source saw the figures, so a hung session keeps ageing.
func TestStoreAgeIsFromTheSeenTimeNotTheArrival(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	s.SetQuota("login", Quota{Primary: &Window{UsedPercent: 10, ResetsAt: c.now().Add(time.Hour).Unix()}}, c.now().Add(-5*time.Minute))
	if _, age, _ := s.Quota("login"); age != 5*time.Minute {
		t.Errorf("age = %v, want 5m", age)
	}
}

func TestStoreNewSessionUnderTheSameNameStartsFresh(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	s.SetFigures("a-1", "sess-1", dollars(500, 3), c.now())
	s.SetFigures("a-1", "sess-2", dollars(0, 0), c.now())
	f, _, _ := s.Figures("a-1")
	if f.Tokens.Input != 0 || f.Money.Amount != 0 {
		t.Errorf("figures = %+v, want the new session's own, not the old one's carried over", f)
	}
}

// The chat's /clear starts a new session whose first status line costs $0; the reset record is about the old one.
func TestStoreLastLiveSkipsAFreshSessionAfterAClear(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	s.SetFigures("_chat", "sess-1", dollars(500, 3), c.now())
	s.SetFigures("_chat", "sess-2", dollars(0, 0), c.now())
	f, ok := s.LastLive("_chat")
	if !ok || f.Money.Amount != 3 || f.Tokens.Input != 500 {
		t.Errorf("last live = %+v, ok=%v, want the pre-clear session's last figures", f, ok)
	}
	s.SetFigures("_chat", "sess-2", dollars(40, 0.2), c.now())
	if f, _ := s.LastLive("_chat"); f.Money.Amount != 0.2 {
		t.Errorf("last live = %+v, want the new session's once it has spent", f)
	}
}

func TestStoreLastLiveIsTheLatestSessionsFigures(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	s.SetFigures("a-1", "sess-1", dollars(10, 0.1), c.now())
	s.SetFigures("a-1", "sess-1", dollars(20, 0.4), c.now())
	if f, ok := s.LastLive("a-1"); !ok || f.Money.Amount != 0.4 {
		t.Errorf("last live = %+v, ok=%v", f, ok)
	}
}

func TestStoreQuotaIsKeyedByLogin(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	later := c.now().Add(time.Hour).Unix()
	s.SetQuota("/home/u/.claude", Quota{Primary: &Window{UsedPercent: 30, ResetsAt: later}}, c.now())
	s.SetQuota("/home/u/.claude-work", Quota{Primary: &Window{UsedPercent: 80, ResetsAt: later}}, c.now())
	a, _, _ := s.Quota("/home/u/.claude")
	b, _, _ := s.Quota("/home/u/.claude-work")
	if a.Primary.UsedPercent != 30 || b.Primary.UsedPercent != 80 {
		t.Errorf("quotas = %v and %v, want each login's own", a.Primary.UsedPercent, b.Primary.UsedPercent)
	}
	if _, _, ok := s.Quota("/home/u/.claude-other"); ok {
		t.Error("quota for a third login")
	}
}

// The quota belongs to the login, not to a session: it stays when the ship that reported it is long gone.
func TestStoreQuotaOutlivesTheSessionAndReportsItsAge(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	s.SetFigures("a-1", "sess-1", dollars(1, 0.1), c.now())
	s.SetQuota("login", Quota{Secondary: &Window{UsedPercent: 61, ResetsAt: c.now().Add(72 * time.Hour).Unix()}}, c.now())
	c.advance(26 * time.Hour)
	q, age, ok := s.Quota("login")
	if !ok || q.Secondary.UsedPercent != 61 || age != 26*time.Hour {
		t.Errorf("quota=%+v age=%v ok=%v, want the last reading, 26h old", q, age, ok)
	}
}

func TestStoreNewerQuotaReplacesOlder(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	later := c.now().Add(time.Hour).Unix()
	s.SetQuota("login", Quota{Primary: &Window{UsedPercent: 10, ResetsAt: later}, Secondary: &Window{UsedPercent: 5, ResetsAt: later}}, c.now())
	s.SetQuota("login", Quota{Primary: &Window{UsedPercent: 20, ResetsAt: later}}, c.now())
	q, _, _ := s.Quota("login")
	if q.Primary.UsedPercent != 20 || q.Secondary != nil {
		t.Errorf("quota = %+v, want the second report as it came", q)
	}
}

// A window whose reset time has passed is a new window: nothing is used in it yet.
func TestStoreQuotaWindowPastItsResetReadsAsReset(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	s.SetQuota("login", Quota{
		Primary:   &Window{UsedPercent: 95, WindowMinutes: 300, ResetsAt: c.now().Add(time.Hour).Unix()},
		Secondary: &Window{UsedPercent: 70, WindowMinutes: 10080, ResetsAt: c.now().Add(48 * time.Hour).Unix()},
		Spend:     &SpendLimit{UsedPercent: 90, UsedUSD: 22.5, LimitUSD: 25, ResetsAt: c.now().Add(2 * time.Hour).Unix()},
	}, c.now())
	c.advance(3 * time.Hour)
	q, _, _ := s.Quota("login")
	if q.Primary.UsedPercent != 0 {
		t.Errorf("five-hour used = %v, want 0 after its reset", q.Primary.UsedPercent)
	}
	if q.Spend.UsedPercent != 0 {
		t.Errorf("spend used = %v, want 0 after its reset", q.Spend.UsedPercent)
	}
	if q.Secondary.UsedPercent != 70 {
		t.Errorf("seven-day used = %v, want 70: that window has not reset", q.Secondary.UsedPercent)
	}
	if q.Primary.WindowMinutes != 300 {
		t.Errorf("window minutes = %d, want it kept", q.Primary.WindowMinutes)
	}
}

func TestStoreQuotaAskedBeforeTheResetIsAsReported(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	s.SetQuota("login", Quota{Primary: &Window{UsedPercent: 95, ResetsAt: c.now().Add(time.Hour).Unix()}}, c.now())
	c.advance(59 * time.Minute)
	if q, _, _ := s.Quota("login"); q.Primary.UsedPercent != 95 {
		t.Errorf("used = %v, want 95", q.Primary.UsedPercent)
	}
}

// Quota callers may keep what they got; the reset reading must not rewrite the stored one.
func TestStoreQuotaReadsDoNotChangeTheStoredReport(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	s.SetQuota("login", Quota{Primary: &Window{UsedPercent: 95, ResetsAt: c.now().Add(time.Hour).Unix()}}, c.now())
	q, _, _ := s.Quota("login")
	q.Primary.UsedPercent = 1
	if again, _, _ := s.Quota("login"); again.Primary.UsedPercent != 95 {
		t.Errorf("used = %v after a caller edited its copy, want 95", again.Primary.UsedPercent)
	}
}

// Live figures are never written, so a restart is a new store that knows nothing.
func TestStoreRestartHasNothing(t *testing.T) {
	c := newClock()
	old := NewStore(c.now)
	old.SetFigures("a-1", "sess-1", dollars(1, 0.1), c.now())
	old.SetQuota("login", Quota{Primary: &Window{UsedPercent: 1, ResetsAt: c.now().Add(time.Hour).Unix()}}, c.now())
	fresh := NewStore(c.now)
	if _, _, ok := fresh.Figures("a-1"); ok {
		t.Error("a new store has figures")
	}
	if _, _, ok := fresh.Quota("login"); ok {
		t.Error("a new store has a quota")
	}
}

func TestStoreConcurrentUpdatesAreSafe(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ship, login := fmt.Sprintf("ship-%d", i%3), fmt.Sprintf("login-%d", i%2)
			for n := 0; n < 200; n++ {
				s.SetFigures(ship, fmt.Sprintf("sess-%d", n%4), dollars(int64(n), float64(n)), c.now())
				s.SetQuota(login, Quota{Primary: &Window{UsedPercent: float64(n % 100), ResetsAt: c.now().Add(time.Hour).Unix()}}, c.now())
				s.Figures(ship)
				s.LastLive(ship)
				s.Quota(login)
			}
		}()
	}
	wg.Wait()
	if _, _, ok := s.Quota("login-0"); !ok {
		t.Error("no quota after the updates")
	}
}

// A record that took a ship's figures forgets them: LastLive answers for one exit only.
func TestStoreForgetDropsTheShipsSessions(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	s.SetFigures("a-1", "sess-1", dollars(500, 3), c.now())
	s.SetFigures("a-2", "sess-9", dollars(5, 0.5), c.now())
	s.Forget("a-1")
	if _, ok := s.LastLive("a-1"); ok {
		t.Error("LastLive answered after Forget")
	}
	if _, _, ok := s.Figures("a-1"); ok {
		t.Error("Figures answered after Forget")
	}
	if f, ok := s.LastLive("a-2"); !ok || f.Money.Amount != 0.5 {
		t.Errorf("another ship's figures = %+v ok=%v, want untouched", f, ok)
	}
}

// Claude's pane keeps its last status line, and the poll reads it again after the record took it.
func TestStoreForgetIgnoresTheSameReadingReadAgain(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	read := c.now()
	s.SetFigures("_chat", "sess-1", dollars(500, 3), read)
	s.Forget("_chat")
	s.SetFigures("_chat", "sess-1", dollars(500, 3), read)
	if _, ok := s.LastLive("_chat"); ok {
		t.Error("the forgotten session came back from the same reading")
	}
	s.SetFigures("_chat", "sess-1", dollars(600, 3.5), read.Add(time.Second))
	if f, ok := s.LastLive("_chat"); !ok || f.Money.Amount != 3.5 {
		t.Errorf("last live = %+v ok=%v, want a newer reading of the session to count", f, ok)
	}
}

func TestStoreKeepsOnlyTheNewestFourSessionsOfAShip(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	for i := 1; i <= 5; i++ {
		s.SetFigures("a-1", fmt.Sprintf("sess-%d", i), dollars(int64(10*i), float64(i)), c.now())
	}
	s.SetFigures("a-1", "sess-6", dollars(0, 0), c.now())
	s.SetFigures("a-1", "sess-7", dollars(0, 0), c.now())
	s.SetFigures("a-1", "sess-8", dollars(0, 0), c.now())
	s.SetFigures("a-1", "sess-9", dollars(0, 0), c.now())
	if _, ok := s.LastLive("a-1"); ok {
		t.Error("an older session outlived the four-session bound")
	}
}

func TestStoreEvictsTheLeastRecentlyUpdatedShipAtItsBound(t *testing.T) {
	c := newClock()
	s := NewStore(c.now)
	for i := 0; i < maxStoreShips; i++ {
		c.advance(time.Second)
		s.SetFigures(fmt.Sprintf("s-%d", i), "x", dollars(1, 1), c.now())
	}
	c.advance(time.Second)
	s.SetFigures("one-more", "x", dollars(1, 1), c.now())
	if _, _, ok := s.Figures("s-0"); ok {
		t.Error("the oldest ship survived the 513th")
	}
	if _, _, ok := s.Figures("s-1"); !ok {
		t.Error("a newer ship was evicted")
	}
}
