package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []wait
}

type wait struct {
	d  time.Duration
	ch chan time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waits = append(c.waits, wait{d, ch})
	return ch
}

func (c *fakeClock) pending() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []time.Duration
	for _, w := range c.waits {
		out = append(out, w.d)
	}
	return out
}

// fire releases every pending wait and reports how long each was for.
func (c *fakeClock) fire() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []time.Duration
	for _, w := range c.waits {
		out = append(out, w.d)
		w.ch <- c.now
	}
	c.waits = nil
	return out
}

type rig struct {
	t        *testing.T
	clock    *fakeClock
	releases *httptest.Server
	requests atomic.Int32
	last     atomic.Pointer[http.Request]
	reply    atomic.Pointer[func(w http.ResponseWriter)]
	enabled  atomic.Bool
	changed  chan struct{}
	mux      *http.ServeMux
	checker  *Checker
}

func tag(tagName string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tagName, "html_url": "https://github.com/jbuffin/admiral/releases/tag/" + tagName, "body": "<script>alert(1)</script>"})
	}
}

func fail(code int) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) { http.Error(w, "nope", code) }
}

// notify says the setting may have changed; the signal coalesces like config.Manager's Subscribe.
func (r *rig) notify() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

func (r *rig) serve(f func(w http.ResponseWriter)) { r.reply.Store(&f) }

// start runs a checker for the running version and executable, with the check on and the releases server answering tag v0.9.2.
func start(t *testing.T, version, exe string) *rig {
	t.Helper()
	r := &rig{t: t, clock: &fakeClock{now: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)}, changed: make(chan struct{}, 1)}
	r.enabled.Store(true)
	r.serve(tag("v0.9.2"))
	r.releases = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		r.last.Store(req)
		(*r.reply.Load())(w)
	}))
	t.Cleanup(r.releases.Close)
	c := New(Options{BaseURL: r.releases.URL, Client: r.releases.Client(), Clock: r.clock, Version: version, Exe: exe,
		Enabled: r.enabled.Load, Changed: r.changed})
	r.checker = c
	r.mux = http.NewServeMux()
	c.Routes(r.mux)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return r
}

// waitWait blocks until the checker is waiting on the clock, then returns how long it asked for.
func (r *rig) nextWait() time.Duration {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p := r.clock.pending(); len(p) > 0 {
			return p[0]
		}
		time.Sleep(time.Millisecond)
	}
	r.t.Fatal("the checker never waited on the clock")
	return 0
}

// tick lets the next scheduled check happen and waits for its request to arrive.
func (r *rig) tick() {
	r.t.Helper()
	before := r.requests.Load()
	r.nextWait()
	r.clock.fire()
	r.waitFor(func() bool { return r.requests.Load() > before }, "a request to the releases server")
}

func (r *rig) waitFor(cond func() bool, what string) {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		r.clock.fire() // a short wait after a settings change needs releasing too
		time.Sleep(time.Millisecond)
	}
	r.t.Fatalf("timed out waiting for %s", what)
}

// holdNoRequests keeps every wait firing for a whole window and fails if the releases server hears anything.
func (r *rig) holdNoRequests(window time.Duration) {
	r.t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		r.clock.fire()
		if n := r.requests.Load(); n != 0 {
			r.t.Fatalf("the releases server heard %d requests, want none", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type status struct {
	CurrentVersion  string  `json:"current_version"`
	LatestVersion   *string `json:"latest_version"`
	UpdateAvailable bool    `json:"update_available"`
	CheckedAt       *string `json:"checked_at"`
	Enabled         bool    `json:"enabled"`
	InstallMethod   string  `json:"install_method"`
	UpgradeCommands []struct {
		Label   string `json:"label"`
		Command string `json:"command"`
	} `json:"upgrade_commands"`
	RestartNote    string `json:"restart_note"`
	ExecutablePath string `json:"executable_path"`
	ReleaseURL     string `json:"release_url"`
}

func (r *rig) status() status {
	r.t.Helper()
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/update", nil))
	if rec.Code != 200 {
		r.t.Fatalf("GET /api/update: %d %s", rec.Code, rec.Body)
	}
	var s status
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		r.t.Fatalf("GET /api/update: %v in %s", err, rec.Body)
	}
	return s
}

func latest(s status) string {
	if s.LatestVersion == nil {
		return "<null>"
	}
	return *s.LatestVersion
}

const exe = "/Users/sam/.local/bin/admiral"

func TestTheFirstCheckComesAboutAMinuteAfterStartThenEveryTwelveHours(t *testing.T) {
	r := start(t, "0.9.1", exe)
	if d := r.nextWait(); d < 30*time.Second || d > 2*time.Minute {
		t.Errorf("the first check waits %v, want about a minute", d)
	}
	time.Sleep(20 * time.Millisecond)
	if n := r.requests.Load(); n != 0 {
		t.Fatalf("%d requests before the first wait ended", n)
	}
	r.tick()
	if d := r.nextWait(); d != 12*time.Hour {
		t.Errorf("the next check waits %v, want 12h", d)
	}
	r.tick()
	if d := r.nextWait(); d != 12*time.Hour {
		t.Errorf("the check after waits %v, want 12h", d)
	}
}

func TestACheckAsksTheLatestReleaseEndpointAndSaysWhoIsAsking(t *testing.T) {
	r := start(t, "0.9.1", exe)
	r.tick()
	req := r.last.Load()
	if req.Method != "GET" || req.URL.Path != "/repos/jbuffin/admiral/releases/latest" {
		t.Errorf("request %s %s, want GET /repos/jbuffin/admiral/releases/latest", req.Method, req.URL.Path)
	}
	if req.Header.Get("User-Agent") == "" || strings.Contains(req.Header.Get("User-Agent"), "Go-http-client") {
		t.Errorf("User-Agent %q, want one that names Admiral (GitHub rejects requests without one)", req.Header.Get("User-Agent"))
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("the check sent credentials; it is unauthenticated")
	}
}

func TestANewerReleaseIsReportedWithItsReleasePageAndWhenItWasChecked(t *testing.T) {
	r := start(t, "0.9.1", exe)
	r.tick()
	r.waitFor(func() bool { return r.status().LatestVersion != nil }, "the route to report the release")
	s := r.status()
	if latest(s) != "0.9.2" || !s.UpdateAvailable || !s.Enabled || s.CurrentVersion != "0.9.1" {
		t.Errorf("status %+v, want latest 0.9.2, update available, on, current 0.9.1", s)
	}
	if s.ReleaseURL != "https://github.com/jbuffin/admiral/releases/tag/v0.9.2" {
		t.Errorf("release_url %q, want the release's page", s.ReleaseURL)
	}
	if s.CheckedAt == nil || *s.CheckedAt != "2026-11-17T09:00:00Z" {
		t.Errorf("checked_at %v, want the clock's time as RFC 3339", s.CheckedAt)
	}
}

func TestBeforeAnyCheckTheRouteHasNoLatestButStillKnowsHowItWasInstalled(t *testing.T) {
	r := start(t, "0.9.1", exe)
	s := r.status()
	if s.LatestVersion != nil || s.CheckedAt != nil || s.UpdateAvailable || !s.Enabled {
		t.Errorf("status %+v before any check, want no latest, no checked_at, no update, check on", s)
	}
	if s.InstallMethod != "script" || s.ExecutablePath != exe || len(s.UpgradeCommands) == 0 || s.RestartNote == "" {
		t.Errorf("status %+v, want the script install of %s with its commands", s, exe)
	}
}

func TestTheRouteReportsTheInstallMethodOfTheExecutable(t *testing.T) {
	r := start(t, "0.9.1", "/opt/homebrew/Cellar/admiral/0.9.1/bin/admiral")
	if s := r.status(); s.InstallMethod != "homebrew" || s.ExecutablePath != "/opt/homebrew/Cellar/admiral/0.9.1/bin/admiral" {
		t.Errorf("method %q, path %q, want homebrew and the Cellar path", s.InstallMethod, s.ExecutablePath)
	}
}

func TestTheReleaseIsNotNewerThanTheRunningVersionIsNoUpdate(t *testing.T) {
	for _, running := range []string{"0.9.2", "0.9.2-3-gabc123-dirty", "dev", ""} {
		r := start(t, running, exe)
		r.tick()
		r.waitFor(func() bool { return r.status().CheckedAt != nil }, "the check to land")
		if s := r.status(); s.UpdateAvailable {
			t.Errorf("running %q against 0.9.2: update available", running)
		}
	}
}

func TestADevBuildAgainstAnyReleaseShowsNoUpdate(t *testing.T) {
	r := start(t, "dev", exe)
	r.serve(tag("v9.9.9"))
	r.tick()
	r.waitFor(func() bool { return r.status().CheckedAt != nil }, "the check to land")
	if s := r.status(); s.UpdateAvailable {
		t.Errorf("a dev build was told an update is available: %+v", s)
	}
}

func TestAFailedCheckKeepsTheLastGoodResultAndTheNextOneReplacesIt(t *testing.T) {
	r := start(t, "0.9.1", exe)
	r.tick()
	r.waitFor(func() bool { return r.status().LatestVersion != nil }, "the first result")
	good := r.status()

	for name, bad := range map[string]func(http.ResponseWriter){
		"server error": fail(500),
		"rate limited": fail(403),
		"not json":     func(w http.ResponseWriter) { _, _ = w.Write([]byte("<html>")) },
	} {
		r.serve(bad)
		before := r.requests.Load()
		r.tick()
		r.nextWait()
		s := r.status()
		if latest(s) != "0.9.2" || !s.UpdateAvailable || *s.CheckedAt != *good.CheckedAt {
			t.Errorf("after a %s: %+v, want the last good result kept", name, s)
		}
		if r.requests.Load() == before {
			t.Errorf("after a %s no request was made", name)
		}
	}

	r.clock.mu.Lock()
	r.clock.now = r.clock.now.Add(12 * time.Hour)
	r.clock.mu.Unlock()
	r.serve(tag("v0.9.3"))
	r.tick()
	r.waitFor(func() bool { return latest(r.status()) == "0.9.3" }, "the next good result")
	if s := r.status(); *s.CheckedAt != "2026-11-17T21:00:00Z" {
		t.Errorf("checked_at %v, want the time of the new good check", *s.CheckedAt)
	}
}

func TestATagThatIsNotVMajorMinorPatchIsIgnored(t *testing.T) {
	for _, bad := range []string{"v1.2", "1.2.3", "v1.2.3-rc.1", "v1.2.3.4", "latest", "", "v1.2.x", "v1.2.3\n"} {
		r := start(t, "0.9.1", exe)
		r.serve(tag(bad))
		r.tick()
		r.nextWait()
		if s := r.status(); s.LatestVersion != nil || s.UpdateAvailable {
			t.Errorf("tag %q: status %+v, want it ignored", bad, s)
		}
	}
}

func TestAMajorTenIsNewerThanMajorNine(t *testing.T) {
	r := start(t, "9.9.9", exe)
	r.serve(tag("v10.0.0"))
	r.tick()
	r.waitFor(func() bool { return r.status().UpdateAvailable }, "10.0.0 to count as newer than 9.9.9")
}

func TestAnOversizedResponseIsTreatedAsAFailedCheck(t *testing.T) {
	r := start(t, "0.9.1", exe)
	r.serve(func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"tag_name":"v9.9.9","pad":"`)
		chunk := strings.Repeat("a", 1<<20)
		for range 8 {
			fmt.Fprint(w, chunk)
		}
		fmt.Fprint(w, `"}`)
	})
	r.tick()
	r.nextWait()
	if s := r.status(); s.LatestVersion != nil || s.UpdateAvailable {
		t.Errorf("an 8 MiB response was read: %+v", s)
	}
}

func TestAReleaseUrlThatIsNotAnHttpsLinkIsNotPassedOn(t *testing.T) {
	r := start(t, "0.9.1", exe)
	r.serve(func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.9.2", "html_url": "javascript:alert(1)"})
	})
	r.tick()
	r.waitFor(func() bool { return r.status().LatestVersion != nil }, "the release to be reported")
	if u := r.status().ReleaseURL; !strings.HasPrefix(u, "https://") {
		t.Errorf("release_url %q, want an https link", u)
	}
}

func TestAReleaseUrlOutsideTheProjectsReleasesIsReplacedByTheTagsPage(t *testing.T) {
	for _, u := range []string{"https://evil.example/releases/v0.9.2", "https://github.com/other/repo/releases/tag/v0.9.2", "https://github.com/jbuffin/admiral-evil/releases/x"} {
		r := start(t, "0.9.1", exe)
		r.serve(func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.9.2", "html_url": u})
		})
		r.tick()
		r.waitFor(func() bool { return r.status().LatestVersion != nil }, "the release to be reported")
		if got := r.status().ReleaseURL; got != "https://github.com/jbuffin/admiral/releases/tag/v0.9.2" {
			t.Errorf("html_url %q: release_url %q, want the tag's page", u, got)
		}
	}
}

func TestTurningTheCheckOffAndOnAgainDoesNotServeTheOldResult(t *testing.T) {
	r := start(t, "0.9.1", exe)
	r.tick()
	r.waitFor(func() bool { return r.status().UpdateAvailable }, "the update")
	r.serve(fail(500))
	r.enabled.Store(false)
	r.notify()
	// The route hides the result while off whatever the checker holds, so wait on the checker itself.
	r.waitFor(func() bool {
		r.checker.mu.Lock()
		defer r.checker.mu.Unlock()
		return r.checker.latest == ""
	}, "the checker to drop its result")
	r.enabled.Store(true)
	r.notify()
	r.waitFor(func() bool { return r.requests.Load() >= 2 }, "the check after turning on")
	r.nextWait()
	if s := r.status(); s.LatestVersion != nil || s.CheckedAt != nil {
		t.Errorf("status %+v after off and on with the new check failing, want the old result gone", s)
	}
}

func TestWithTheCheckOffNoRequestIsMadeAndTheRouteSaysOff(t *testing.T) {
	off := start(t, "0.9.1", exe)
	off.enabled.Store(false)
	off.notify()
	off.holdNoRequests(300 * time.Millisecond)
	s := off.status()
	if s.Enabled || s.UpdateAvailable || s.LatestVersion != nil {
		t.Errorf("status %+v with the check off, want off, no latest, no update", s)
	}
}

func TestACheckThatIsOffAtStartNeverMakesTheFirstRequest(t *testing.T) {
	r := &rig{t: t, clock: &fakeClock{now: time.Now()}, changed: make(chan struct{}, 1)}
	r.serve(tag("v0.9.2"))
	r.releases = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		(*r.reply.Load())(w)
	}))
	t.Cleanup(r.releases.Close)
	c := New(Options{BaseURL: r.releases.URL, Client: r.releases.Client(), Clock: r.clock, Version: "0.9.1", Exe: exe,
		Enabled: func() bool { return false }, Changed: r.changed})
	r.mux = http.NewServeMux()
	c.Routes(r.mux)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	r.holdNoRequests(300 * time.Millisecond)
	if s := r.status(); s.Enabled || s.UpdateAvailable {
		t.Errorf("status %+v, want off and no update", s)
	}
}

func TestTurningTheCheckOffHidesTheUpdateAndStopsRequests(t *testing.T) {
	r := start(t, "0.9.1", exe)
	r.tick()
	r.waitFor(func() bool { return r.status().UpdateAvailable }, "the update")
	r.enabled.Store(false)
	r.notify()
	r.waitFor(func() bool { return !r.status().Enabled }, "the route to say the check is off")
	if s := r.status(); s.UpdateAvailable {
		t.Errorf("status %+v with the check off, want no update", s)
	}
	n := r.requests.Load()
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		r.clock.fire()
		if r.requests.Load() != n {
			t.Fatal("a request was made with the check off")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTurningTheCheckBackOnRunsACheckWithoutWaitingTwelveHours(t *testing.T) {
	r := start(t, "0.9.1", exe)
	r.enabled.Store(false)
	r.notify()
	r.holdNoRequests(100 * time.Millisecond)
	r.enabled.Store(true)
	r.notify()
	// waitFor releases every short wait, but not by letting a 12 hour interval pass: the check must come from the change.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && r.requests.Load() == 0 {
		for _, d := range r.clock.pending() {
			if d >= time.Hour {
				t.Fatalf("turning the check on left it waiting %v", d)
			}
		}
		r.clock.fire()
		time.Sleep(time.Millisecond)
	}
	if r.requests.Load() == 0 {
		t.Fatal("no check ran after the check was turned on")
	}
	r.waitFor(func() bool { return r.status().UpdateAvailable }, "the update after turning the check on")
	if !r.status().Enabled {
		t.Error("the route still says off")
	}
}
