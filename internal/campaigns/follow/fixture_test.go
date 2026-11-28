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
	"sync/atomic"
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
if [ "$1" = api ] && [ "$2" = --method ]; then
	method=$3 host= endpoint= input= prev=
	for a in "$@"; do
		[ "$prev" = --hostname ] && host=$a
		[ "$prev" = --input ] && input=$a
		prev=$a endpoint=$a
	done
	stdin=
	[ -n "$input" ] && stdin=$(cat)
	printf '%s\t%s\t%s\t%s\t%s\n' "$method" "$host" "$endpoint" "$stdin" "$*" >> "$d/rest.log"
	key=rest_${method}_$(printf %s "$endpoint" | tr / _)
	digits=$(printf %s "$stdin" | tr -cd 0-9)
	for k in "${key}_${digits}" "$key"; do
		if [ -e "$d/resp/$k.fail" ]; then cat "$d/resp/$k.fail" >&2; exit 1; fi
		if [ -e "$d/resp/$k.json" ]; then cat "$d/resp/$k.json"; exit 0; fi
	done
	echo "no canned answer for $key" >&2
	exit 1
fi
owner= repo= number= after= q=
for a in "$@"; do
	case "$a" in
	owner=*) owner=${a#owner=} ;;
	repo=*) repo=${a#repo=} ;;
	number=*) number=${a#number=} ;;
	after=*) after=${a#after=} ;;
	q=*) q=${a#q=} ;;
	esac
done
if [ -n "$q" ]; then
	key=search
	case "$q" in *has:sub-issue*) key=search_parents ;; esac
else key=${owner}_${repo}_${number}; fi
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
	g.respond("search_parents", `{"data":{"search":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`)
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

// restKey names the answer file of a REST call; withID picks the call whose body holds those digits (a sub-issue's REST id).
func restKey(method, endpoint, withID string) string {
	k := "rest_" + method + "_" + strings.ReplaceAll(endpoint, "/", "_")
	if withID != "" {
		k += "_" + withID
	}
	return k
}

func (g *fakeGH) respondREST(method, endpoint, withID, body string) {
	g.respond(restKey(method, endpoint, withID), body)
}

func (g *fakeGH) failREST(method, endpoint, withID, stderr string) {
	if err := os.WriteFile(filepath.Join(g.dir, "resp", restKey(method, endpoint, withID)+".fail"), []byte(stderr), 0o644); err != nil {
		panic(err)
	}
}

// restCall is one gh api --method call, as the fake gh saw it.
type restCall struct{ Method, Host, Endpoint, Stdin, Args string }

func (g *fakeGH) restCalls() []restCall {
	raw, _ := os.ReadFile(filepath.Join(g.dir, "rest.log"))
	var out []restCall
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 5)
		out = append(out, restCall{f[0], f[1], strings.TrimPrefix(f[2], "/"), f[3], f[4]})
	}
	return out
}

// hit is one issue a search answers with.
type hit struct {
	repo              string
	num               int
	closed            bool
	total, closedKids int
}

func (h hit) node() map[string]any {
	state := "OPEN"
	if h.closed {
		state = "CLOSED"
	}
	return map[string]any{
		"number": h.num, "title": fmt.Sprintf("Issue %d", h.num), "state": state,
		"url":              fmt.Sprintf("https://github.com/%s/issues/%d", h.repo, h.num),
		"repository":       map[string]any{"nameWithOwner": h.repo},
		"subIssuesSummary": map[string]any{"total": h.total, "completed": h.closedKids},
	}
}

// respondSearch answers the plain search with plain and the has:sub-issue search with parents.
func (g *fakeGH) respondSearch(plain, parents []hit) {
	for key, hits := range map[string][]hit{"search": plain, "search_parents": parents} {
		nodes := []any{}
		for _, h := range hits {
			nodes = append(nodes, h.node())
		}
		b, _ := json.Marshal(map[string]any{"data": map[string]any{"search": map[string]any{"nodes": nodes}}})
		g.respond(key, string(b))
	}
}

// respondIssue answers the single-issue read of h, the one a reference with no search hit resolves through.
func (g *fakeGH) respondIssue(h hit) {
	n := h.node()
	n["__typename"] = "Issue"
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"repository": map[string]any{"issueOrPullRequest": n}}})
	g.respond(strings.ReplaceAll(h.repo, "/", "_")+"_"+fmt.Sprint(h.num), string(b))
}

// respondPullRequest answers the single read of owner/repo#num with a pull request.
func (g *fakeGH) respondPullRequest(repo string, num int) {
	n := map[string]any{"__typename": "PullRequest", "number": num, "title": "A pull request", "state": "OPEN",
		"url": fmt.Sprintf("https://github.com/%s/pull/%d", repo, num), "repository": map[string]any{"nameWithOwner": repo}}
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"repository": map[string]any{"issueOrPullRequest": n}}})
	g.respond(strings.ReplaceAll(repo, "/", "_")+"_"+fmt.Sprint(num), string(b))
}

// failFile makes the answer for key an error with stderr.
func (g *fakeGH) failFile(key, stderr string) {
	if err := os.WriteFile(filepath.Join(g.dir, "resp", key+".fail"), []byte(stderr), 0o644); err != nil {
		panic(err)
	}
}

// searchQueries reads back the q= of every search gh was asked for.
func (g *fakeGH) searchQueries() []string {
	files, _ := filepath.Glob(filepath.Join(g.dir, "calls", "*.argv"))
	var out []string
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		for _, a := range strings.Split(string(raw), "\x00") {
			if q, ok := strings.CutPrefix(a, "q="); ok {
				out = append(out, q)
			}
		}
	}
	return out
}

// ghCalls counts every gh run so far, tokens included.
func (g *fakeGH) ghCalls() int {
	files, _ := filepath.Glob(filepath.Join(g.dir, "calls", "*.argv"))
	return len(files)
}

func (g *fakeGH) delay(d time.Duration) {
	if err := os.WriteFile(filepath.Join(g.dir, "delay"), []byte(fmt.Sprintf("%.3f", d.Seconds())), 0o644); err != nil {
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
	polls    atomic.Int32 // lists of the ships the service has taken
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
			r.polls.Add(1)
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
	t.Cleanup(r.svc.Close) // a read on its own goroutine must end before the TempDirs go
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
		URL    string `json:"url"`
		Capped bool   `json:"capped"`
	} `json:"blockers"`
	Skill struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Args string `json:"args"`
	} `json:"skill"`
	Children []*node `json:"children"`

	Labels       []string `json:"labels"`
	Overridden   bool     `json:"overridden"`
	DefaultMode  string   `json:"default_mode"`
	DefaultSkill struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"default_skill"`
}

type campaignJSON struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Fleet     string `json:"fleet"`
	WorkRepo  string `json:"work_repo"`
	Ended     bool   `json:"ended"`
	EndReason string `json:"end_reason"`
	LastRead  string `json:"last_read"`
	ReadErr   string `json:"read_error"`
	Progress  struct {
		Closed int `json:"closed"`
		Total  int `json:"total"`
	} `json:"progress"`
	Counts map[string]int `json:"counts"`
	Root   *node          `json:"root"`

	Title           string `json:"title"`
	Number          int    `json:"number"`
	Repo            string `json:"repo"`
	FollowedAt      string `json:"followed_at"`
	EndedAt         string `json:"ended_at"`
	ReadyWithYou    int    `json:"ready_with_you"`
	ReadyWithoutYou int    `json:"ready_without_you"`
	Reading         bool   `json:"reading"`
	LastTried       string `json:"last_tried"`
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

// idle waits until no campaign has a read under way, on the service's own goroutine or a request's.
func (r *rig) idle() {
	r.t.Helper()
	eventually(r.t, "the reads to finish", func() bool {
		r.svc.mu.Lock()
		defer r.svc.mu.Unlock()
		for _, rd := range r.svc.reads {
			if rd.active.Load() != 0 {
				return false
			}
		}
		return true
	})
}

// shipsChanged tells the service the ships changed and returns once it has acted on that: the second poke is
// taken only after the first one's diff. The rereads it asked for have only started; holds waits for them.
func (r *rig) shipsChanged() {
	r.t.Helper()
	n := r.polls.Load()
	r.svc.ShipsChanged()
	eventually(r.t, "the service to list the ships", func() bool { return r.polls.Load() > n })
	r.svc.ShipsChanged()
	eventually(r.t, "the service to take the next poke", func() bool { return r.polls.Load() > n+1 })
}

// holds fails if bad is true once every read the service started has finished.
func (r *rig) holds(what string, bad func() bool) {
	r.t.Helper()
	r.idle()
	if bad() {
		r.t.Fatalf("%s", what)
	}
}

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o644) }

// sliceTree is the campaign the slice launch tests follow: 31, 32 and 33 are ready Without you on Commodore, 34 is
// blocked by 31, 35 done, 36 assigned with no ship, 37 With you, 38 a wayfinder task, 39 ready in another repo and
// 40 a nested parent holding 41.
func sliceTree() iss {
	ready := []string{"ready-for-agent"}
	return iss{num: 20, labels: []string{"wayfinder:map"}, kids: []iss{
		{num: 31, labels: ready},
		{num: 32, labels: ready},
		{num: 33, labels: ready},
		{num: 34, labels: ready, blockedBy: []int{31}},
		{num: 35, labels: ready, closed: true},
		{num: 36, labels: ready, assignees: []string{"sam"}},
		{num: 37, labels: []string{"ready-for-human"}},
		{num: 38, labels: []string{"wayfinder:task"}},
		{num: 39, labels: ready, repo: "acme/other"},
		{num: 40, kids: []iss{{num: 41, labels: ready}}},
	}}
}
