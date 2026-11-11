package follow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ships"
)

const ghScript = `#!/bin/sh
d="$FAKEGH_DIR"
mkdir -p "$d/calls"
printf '%s\0' "$@" > "$d/calls/$$.argv"
owner= repo= number= after= q=
for a in "$@"; do
	case "$a" in
	owner=*) owner=${a#owner=} ;;
	repo=*) repo=${a#repo=} ;;
	number=*) number=${a#number=} ;;
	after=*) after=${a#after=} ;;
	q=*) q=1 ;;
	esac
done
if [ -n "$q" ]; then key=search; else key=${owner}_${repo}_${number}; fi
[ -n "$after" ] && key=${key}_${after}
if [ -e "$d/delay" ]; then sleep "$(cat "$d/delay")"; fi
if [ -e "$d/resp/$key.fail" ]; then cat "$d/resp/$key.fail" >&2; exit 1; fi
if [ -e "$d/resp/$key.json" ]; then cat "$d/resp/$key.json"; exit 0; fi
echo "no canned answer for $key" >&2
exit 1
`

// iss is one issue of the fixture tree.
type iss struct {
	num       int
	repo      string // owner/name; acme/widgets when empty
	closed    bool
	labels    []string
	assignees []string
	blockedBy []int
	kids      []iss
}

const rootURL = "https://github.com/acme/widgets/issues/20"

func issURL(n int) string { return fmt.Sprintf("https://github.com/acme/widgets/issues/%d", n) }

func (i iss) repoName() string {
	if i.repo == "" {
		return "acme/widgets"
	}
	return i.repo
}

func (i iss) node() map[string]any {
	state := "OPEN"
	if i.closed {
		state = "CLOSED"
	}
	labels, assignees, blockers, kids := []any{}, []any{}, []any{}, []any{}
	for _, l := range i.labels {
		labels = append(labels, map[string]any{"name": l})
	}
	for _, a := range i.assignees {
		assignees = append(assignees, map[string]any{"login": a})
	}
	for _, b := range i.blockedBy {
		blockers = append(blockers, map[string]any{"url": issURL(b), "title": fmt.Sprintf("Issue %d", b), "state": "OPEN"})
	}
	for _, k := range i.kids {
		kids = append(kids, k.node())
	}
	page := map[string]any{"hasNextPage": false, "endCursor": nil}
	return map[string]any{
		"__typename": "Issue", "number": i.num, "title": fmt.Sprintf("Issue %d", i.num),
		"url": fmt.Sprintf("https://github.com/%s/issues/%d", i.repoName(), i.num), "state": state, "body": "",
		"repository": map[string]any{"nameWithOwner": i.repoName()},
		"labels":     map[string]any{"nodes": labels, "pageInfo": page},
		"assignees":  map[string]any{"nodes": assignees},
		"blockedBy":  map[string]any{"nodes": blockers, "pageInfo": page},
		"subIssues":  map[string]any{"totalCount": len(i.kids), "nodes": kids, "pageInfo": page},
	}
}

// fixtureTree is the campaign the service tests follow: 21 ready, 22 blocked by 21, 23 done, 24 a research
// ticket, 25 assigned with no ship, 26 a nested parent holding 27, 28 a child in another repo.
func fixtureTree() iss {
	return iss{num: 20, labels: []string{"wayfinder:map"}, kids: []iss{
		{num: 21, labels: []string{"ready-for-agent"}},
		{num: 22, blockedBy: []int{21}},
		{num: 23, closed: true},
		{num: 24, labels: []string{"wayfinder:research"}},
		{num: 25, assignees: []string{"sam"}},
		{num: 26, kids: []iss{{num: 27, labels: []string{"wayfinder:task"}}}},
		{num: 28, repo: "acme/other"},
	}}
}

type fakeGH struct {
	dir string
	gh  string
	env []string
}

func newFakeGH(t *testing.T, root iss) *fakeGH {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "resp"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := &fakeGH{dir: dir, gh: filepath.Join(dir, "gh"), env: append(os.Environ(), "FAKEGH_DIR="+dir)}
	if err := os.WriteFile(g.gh, []byte(ghScript), 0o755); err != nil {
		t.Fatal(err)
	}
	g.set(root)
	g.respond("__", "fake-token-work-1")
	g.respond("search", `{"data":{"search":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`)
	return g
}

func (g *fakeGH) respond(key, body string) {
	if err := os.WriteFile(filepath.Join(g.dir, "resp", key+".json"), []byte(body), 0o644); err != nil {
		panic(err)
	}
}

// set answers a read of i and of every issue under it.
func (g *fakeGH) set(i iss) {
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"repository": map[string]any{"issueOrPullRequest": i.node()}}})
	g.respond(strings.ReplaceAll(i.repoName(), "/", "_")+"_"+fmt.Sprint(i.num), string(b))
	for _, k := range i.kids {
		g.set(k)
	}
}

func (g *fakeGH) delay(d time.Duration) {
	if err := os.WriteFile(filepath.Join(g.dir, "delay"), []byte(fmt.Sprintf("%.1f", d.Seconds())), 0o644); err != nil {
		panic(err)
	}
}

func (g *fakeGH) client(host string) *github.Client {
	c := github.NewClient(host, "", nil, g.env)
	c.Gh = g.gh
	return c
}

// rootReads counts the reads of the parent issue (number 20) so far.
func (g *fakeGH) rootReads() int {
	files, _ := filepath.Glob(filepath.Join(g.dir, "calls", "*.argv"))
	n := 0
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		if bytes.Contains(raw, []byte("number=20\x00")) && bytes.Contains(raw, []byte("owner=acme\x00")) {
			n++
		}
	}
	return n
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// rig is a Service over a store, a fake gh, a fake launch and a movable clock.
type rig struct {
	t        *testing.T
	svc      *Service
	gh       *fakeGH
	clock    *clock
	mux      *http.ServeMux
	mu       sync.Mutex
	shipList []ships.Ship
	claims   map[string]string
	launched []launcher.Request
	routed   []string // "host nwo" of each Route call
	hostSeen []string // hosts asked of Clients
	hasHost  func(host string) bool
}

func newRig(t *testing.T, root iss) *rig {
	t.Helper()
	r := &rig{t: t, gh: newFakeGH(t, root), clock: &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}, claims: map[string]string{},
		hasHost: func(h string) bool { return strings.EqualFold(h, "github.com") }}
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.svc = &Service{
		Store: store,
		Route: func(host, nwo string) string {
			r.routed = append(r.routed, host+" "+nwo)
			return "work"
		},
		Clients: func(fleet, host string) (*github.Client, bool) {
			r.hostSeen = append(r.hostSeen, host)
			if fleet != "work" || !r.hasHost(host) {
				return nil, false
			}
			return r.gh.client(host), true
		},
		Ships: func() []ships.Ship {
			r.mu.Lock()
			defer r.mu.Unlock()
			return append([]ships.Ship(nil), r.shipList...)
		},
		Claims: func() map[string]string {
			r.mu.Lock()
			defer r.mu.Unlock()
			out := map[string]string{}
			for k, v := range r.claims {
				out[k] = v
			}
			return out
		},
		Launch: func(_ context.Context, req launcher.Request) (ships.Ship, *launcher.Error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.launched = append(r.launched, req)
			return ships.Ship{Name: "calm-otter", Status: ships.StatusUnderway}, nil
		},
		Now: r.clock.Now,
	}
	r.mux = http.NewServeMux()
	r.svc.Routes(r.mux)
	return r
}

func (r *rig) setShips(list ...ships.Ship) {
	r.mu.Lock()
	r.shipList = list
	r.mu.Unlock()
}

func (r *rig) launches() []launcher.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]launcher.Request(nil), r.launched...)
}

func (r *rig) do(method, path string, body any) (int, []byte) {
	r.t.Helper()
	var rd *bytes.Reader
	if body == nil {
		rd = bytes.NewReader(nil)
	} else {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

// node is a child as the campaign JSON shows it.
type node struct {
	Number   int    `json:"number"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Repo     string `json:"repo"`
	Closed   bool   `json:"closed"`
	Parent   bool   `json:"parent"`
	Status   string `json:"status"`
	Ship     string `json:"ship"`
	Mode     string `json:"mode"`
	Blockers []struct {
		URL string `json:"url"`
	} `json:"blockers"`
	Skill struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Args string `json:"args"`
	} `json:"skill"`
	Children []*node `json:"children"`
}

type campaignJSON struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Fleet    string `json:"fleet"`
	WorkRepo string `json:"work_repo"`
	Ended    bool   `json:"ended"`
	LastRead string `json:"last_read"`
	ReadErr  string `json:"read_error"`
	Progress struct {
		Closed int `json:"closed"`
		Total  int `json:"total"`
	} `json:"progress"`
	Counts map[string]int `json:"counts"`
	Root   *node          `json:"root"`
}

func (n *node) find(num int) *node {
	if n == nil {
		return nil
	}
	if n.Number == num {
		return n
	}
	for _, c := range n.Children {
		if f := c.find(num); f != nil {
			return f
		}
	}
	return nil
}

func (r *rig) follow(ref string) campaignJSON {
	r.t.Helper()
	code, body := r.do("POST", "/api/campaigns", map[string]string{"reference": ref})
	if code != http.StatusCreated {
		r.t.Fatalf("POST /api/campaigns %q = %d %s, want 201", ref, code, body)
	}
	var c campaignJSON
	if err := json.Unmarshal(body, &c); err != nil {
		r.t.Fatalf("campaign JSON: %v\n%s", err, body)
	}
	return c
}

func (r *rig) get(id string) campaignJSON {
	r.t.Helper()
	code, body := r.do("GET", "/api/campaigns/"+id, nil)
	if code != http.StatusOK {
		r.t.Fatalf("GET /api/campaigns/%s = %d %s", id, code, body)
	}
	var c campaignJSON
	if err := json.Unmarshal(body, &c); err != nil {
		r.t.Fatalf("campaign JSON: %v\n%s", err, body)
	}
	return c
}

func (r *rig) launch(id string, body map[string]any) (int, string) {
	code, b := r.do("POST", "/api/campaigns/"+id+"/launch", body)
	return code, string(b)
}

// eventually polls for a condition the service reaches on its own goroutine.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// holds fails if ok turns true within a short window.
func holds(t *testing.T, what string, bad func() bool) {
	t.Helper()
	for i := 0; i < 10; i++ {
		if bad() {
			t.Fatalf("%s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o644) }
