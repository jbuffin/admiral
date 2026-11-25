package crewapi_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is the update checker's time, moved by hand.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []clockWait
}

type clockWait struct {
	d  time.Duration
	ch chan time.Time
}

func newClock() *fakeClock { return &fakeClock{now: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waits = append(c.waits, clockWait{d, ch})
	return ch
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
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

// fire releases every pending wait.
func (c *fakeClock) fire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.waits {
		w.ch <- c.now
	}
	c.waits = nil
}

// eventually polls cond for two seconds; a stub that never acts must fail the test, not hang it.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// clockIt gives the service a fake clock; call it before the first request.
func (p *pubFixture) clockIt(t *testing.T) *fakeClock {
	t.Helper()
	clk := newClock()
	p.svc.Clock = clk
	return clk
}

// peekUpdate is the update state GET .../crew shows on a member, or "" when the key is missing; it never fails the test.
func (p *pubFixture) peekUpdate(name string) string {
	status, body := p.do("GET", p.base(), nil)
	if status != 200 {
		return ""
	}
	var got struct {
		Members []map[string]any `json:"members"`
	}
	_ = json.Unmarshal([]byte(body), &got)
	for _, m := range got.Members {
		if m["name"] == name {
			s, _ := m["update"].(string)
			return s
		}
	}
	return ""
}

// runChecker starts the service's background check and stops it when the test ends.
func (p *pubFixture) runChecker() {
	p.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.svc.Run(ctx)
	}()
	p.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			p.t.Error("the background check did not stop when its context ended")
		}
	})
}

// listings returns the recorded calls that list a repo's crew/ folder.
func (g *ghFake) listings() []string {
	g.t.Helper()
	var out []string
	for _, c := range g.calls() {
		if c.Method == "GET" && strings.HasSuffix(c.Endpoint, "/contents/crew") {
			out = append(out, strings.ToLower(c.Endpoint))
		}
	}
	return out
}

// upstreamListing cans a repo's crew/ folder: the file stems with their blob SHAs.
func (g *ghFake) upstreamListing(full string, blobs map[string]string) {
	g.t.Helper()
	var names []string
	for n := range blobs {
		names = append(names, n)
	}
	sort.Strings(names)
	var list []map[string]any
	for _, n := range names {
		list = append(list, map[string]any{"name": n + ".md", "path": "crew/" + n + ".md", "type": "file", "sha": blobs[n]})
	}
	g.ok("GET", "repos/"+full+"/contents/crew", list)
}

// rateLimited makes a repo's crew/ listing answer GitHub's rate limit.
func (g *ghFake) rateLimited(full string) {
	g.t.Helper()
	g.answer("GET", "repos/"+full+"/contents/crew", 403, nil, "gh: API rate limit exceeded for user ID 1. Try again in 42 minutes. (HTTP 403)")
}

// att describes an imported member as it sits on disk.
type att struct {
	name         string // the local member
	repo         string // default hubot/crew
	upstreamName string // default: name
	blob         string // upstream_blob; default blobOf(upstreamName + "-old")
	tag          any    // the sidecar's tag
	text         string // the member's bytes; default memberFile(name, "")
}

func (a att) filled() att {
	if a.repo == "" {
		a.repo = "hubot/crew"
	}
	if a.upstreamName == "" {
		a.upstreamName = a.name
	}
	if a.blob == "" {
		a.blob = blobOf(a.upstreamName + "-old")
	}
	if a.text == "" {
		a.text = memberFile(a.name, "")
	}
	return a
}

func (a att) sidecar() map[string]any {
	a = a.filled()
	return map[string]any{
		"version": 1, "host": "github.com", "repo": a.repo, "path": "crew/" + a.upstreamName + ".md",
		"upstream_name": a.upstreamName, "commit": commit40, "tag": a.tag, "upstream_blob": a.blob,
		"content_sha256": hexSum(a.text), "imported_at": "2026-11-20T10:00:00Z", "updated_at": "2026-11-20T10:00:00Z",
		"from_elsewhere": "keep me",
	}
}

// attach writes a member and an attached sidecar beside it and returns the member's bytes.
func (p *pubFixture) attach(a att) string {
	p.t.Helper()
	a = a.filled()
	writeFile(p.t, filepath.Join(p.crewDir(), a.name+".md"), a.text)
	p.writeSidecarMap(a.name, a.sidecar())
	return a.text
}

func (p *pubFixture) writeSidecarMap(name string, m map[string]any) {
	p.t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		p.t.Fatal(err)
	}
	writeFile(p.t, filepath.Join(p.crewDir(), name+".shared.json"), string(b))
}

func (p *pubFixture) sidecarOf(name string) map[string]any {
	p.t.Helper()
	return readSidecarMap(p.t, filepath.Join(p.crewDir(), name+".shared.json"))
}

func (p *pubFixture) memberBytes(name string) string {
	p.t.Helper()
	return readFile(p.t, filepath.Join(p.crewDir(), name+".md"))
}

// updates asks GET .../crew/updates and returns its status and the member to state map.
func (p *pubFixture) updates() (int, map[string]string) {
	p.t.Helper()
	status, out := p.getJSON("/updates")
	states := map[string]string{}
	if m, ok := out["updates"].(map[string]any); ok {
		for k, v := range m {
			states[k], _ = v.(string)
		}
	}
	return status, states
}

// updateOf is the update state GET .../crew shows on a member; the key must be there.
func (p *pubFixture) updateOf(name string) string {
	p.t.Helper()
	m := p.rawMember(name)
	v, present := m["update"]
	if !present {
		p.t.Errorf("member %s has no update key in %v", name, m)
		return "<missing>"
	}
	s, _ := v.(string)
	return s
}

func (p *pubFixture) wantCalls(what string, want int) {
	p.t.Helper()
	if n := len(p.gh.calls()); n != want {
		p.t.Errorf("%s: %d gh calls, want %d: %v", what, n, want, endpoints(p.gh.calls()))
	}
}

func endpoints(cs []ghCall) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Method+" "+c.Endpoint)
	}
	return out
}

// oldScout is the member as imported; newScout is the file upstream now.
var (
	oldScout = memberFile("scout", "")
	newScout = "---\nname: scout\nrole: triager\npick-me-when: Pick scout v2.\n---\n\nWork as scout, better.\n"
	newSHA   = strings.Repeat("b", 40)
)

// updatable is a fleet with scout imported, and the upstream file moved on to newScout at newSHA, tagged v2.
type updatable struct {
	*pubFixture
	clk *fakeClock
	up  upstream
}

func newUpdatable(t *testing.T, o impOpts) *updatable {
	t.Helper()
	p := newImp(t, o)
	clk := p.clockIt(t)
	p.attach(att{name: "scout", text: oldScout, tag: "v1"})
	up := upstream{owner: "hubot", repo: "crew", name: "scout", text: newScout, commit: newSHA, tags: []string{"v2"}}
	p.gh.seed(up)
	p.gh.upstreamListing("hubot/crew", map[string]string{"scout": up.blob()})
	return &updatable{pubFixture: p, clk: clk, up: up}
}

func (u *updatable) preview(name string) (int, map[string]any) {
	u.t.Helper()
	return u.post("/members/"+name+"/update/preview", nil)
}

// apply posts the body a page would send from a preview, with overrides.
func (u *updatable) apply(name string, pv map[string]any, extra map[string]any) (int, map[string]any) {
	u.t.Helper()
	body := map[string]any{"commit": pv["commit"], "sha256": pv["sha256"], "current_sha256": pv["current_sha256"]}
	for k, v := range extra {
		body[k] = v
	}
	return u.post("/members/"+name+"/update", body)
}

func (u *updatable) previewOK(name string) map[string]any {
	u.t.Helper()
	status, pv := u.preview(name)
	if status != 200 {
		u.t.Fatalf("update preview %s: %d %v", name, status, pv)
	}
	return pv
}
