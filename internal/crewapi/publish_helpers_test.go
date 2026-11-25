package crewapi_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crewapi"
	"github.com/jbuffin/admiral/internal/github"
)

const (
	shareRepo   = "octocat/admiral-crew"
	listingPath = "repos/octocat/admiral-crew/contents/crew"
	botHeader   = "X-Test-Bot-Fleet"
)

func memberPath(name string) string { return listingPath + "/" + name + ".md" }

// decoy is a secret-shaped value the fake gitleaks reports and the repo's secret scan allows.
const decoy = "decoy-secret-0123456789abcdef"

// ghFake drives e2e/testdata/fakegh/gh through $FAKEGH_DIR.
type ghFake struct {
	t   *testing.T
	dir string
	n   int
}

func newGHFake(t *testing.T) *ghFake {
	t.Helper()
	g := &ghFake{t: t, dir: t.TempDir()}
	writeFile(t, filepath.Join(g.dir, "token"), "fake-token-work-2\n")
	return g
}

func (g *ghFake) signedOut() { _ = os.Remove(filepath.Join(g.dir, "token")) }

// answer cans the REST call METHOD endpoint (no leading slash, no query). stderr "" means the fake's default.
func (g *ghFake) answer(method, endpoint string, status int, body any, stderr string) {
	g.t.Helper()
	g.n++
	key := filepath.Join(g.dir, "rest", "a"+strconv.Itoa(1000+g.n))
	writeFile(g.t, key+".endpoint", endpoint)
	writeFile(g.t, key+".method", method)
	writeFile(g.t, key+".status", strconv.Itoa(status))
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			g.t.Fatal(err)
		}
		writeFile(g.t, key+".json", string(b))
	}
	if stderr != "" {
		writeFile(g.t, key+".stderr", stderr)
	}
}

// answerOnce is answer for one call only; the next matching canned answer takes over after it.
func (g *ghFake) answerOnce(method, endpoint string, body any) {
	g.t.Helper()
	g.answer(method, endpoint, 200, body, "")
	writeFile(g.t, filepath.Join(g.dir, "rest", "a"+strconv.Itoa(1000+g.n)+".once"), "")
}

// forget drops every canned answer of METHOD endpoint, so a later answer can replace it.
func (g *ghFake) forget(method, endpoint string) {
	g.t.Helper()
	keys, _ := filepath.Glob(filepath.Join(g.dir, "rest", "*.endpoint"))
	for _, e := range keys {
		key := strings.TrimSuffix(e, ".endpoint")
		if strings.TrimSpace(slurp(e)) == endpoint && strings.TrimSpace(slurp(key+".method")) == method {
			files, _ := filepath.Glob(key + ".*")
			for _, f := range files {
				_ = os.Remove(f)
			}
		}
	}
}

func (g *ghFake) ok(method, endpoint string, body any) { g.answer(method, endpoint, 200, body, "") }

type ghCall struct {
	Method, Endpoint, Host, Stdin string
	Argv                          []string
}

func (g *ghFake) calls() []ghCall {
	g.t.Helper()
	entries, _ := os.ReadDir(filepath.Join(g.dir, "calls"))
	type stamped struct {
		at time.Time
		c  ghCall
	}
	var all []stamped
	for _, e := range entries {
		d := filepath.Join(g.dir, "calls", e.Name())
		method := strings.TrimSpace(slurp(filepath.Join(d, "method")))
		if method == "" {
			continue // gh auth token and the like
		}
		info, _ := e.Info()
		argv := strings.Split(strings.TrimSuffix(slurp(filepath.Join(d, "argv")), "\x00"), "\x00")
		all = append(all, stamped{info.ModTime(), ghCall{method, strings.TrimSpace(slurp(filepath.Join(d, "endpoint"))),
			strings.TrimSpace(slurp(filepath.Join(d, "host"))), slurp(filepath.Join(d, "stdin")), argv}})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	out := make([]ghCall, len(all))
	for i, a := range all {
		out[i] = a.c
	}
	return out
}

// find returns the recorded calls of one method and endpoint.
func (g *ghFake) find(method, endpoint string) []ghCall {
	g.t.Helper()
	var out []ghCall
	for _, c := range g.calls() {
		if c.Method == method && c.Endpoint == endpoint {
			out = append(out, c)
		}
	}
	return out
}

// writes returns every call that is not a GET.
func (g *ghFake) writes() []ghCall {
	g.t.Helper()
	var out []ghCall
	for _, c := range g.calls() {
		if c.Method != "GET" {
			out = append(out, c)
		}
	}
	return out
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// repoAnswer is GET /repos/{owner}/{repo} as GitHub sends it.
func repoAnswer(visibility string, push, archived bool) map[string]any {
	return map[string]any{"full_name": shareRepo, "visibility": visibility, "private": visibility != "public", "archived": archived,
		"permissions": map[string]any{"push": push}, "default_branch": "main"}
}

func (g *ghFake) healthyRepo() {
	g.ok("GET", "user", map[string]any{"login": "octocat"})
	g.ok("GET", "repos/"+shareRepo, repoAnswer("public", true, false))
}

// published lists the names in the share repo's crew/ directory, each with a blob SHA derived from its name.
func (g *ghFake) published(names ...string) {
	var list []map[string]any
	for _, n := range names {
		list = append(list, map[string]any{"name": n + ".md", "path": "crew/" + n + ".md", "type": "file", "sha": blobOf(n)})
	}
	g.ok("GET", listingPath, list)
}

func blobOf(name string) string { return (strings.Repeat(hexSum(name)[:8], 5)) }

// file canns GET and PUT/DELETE answers for a published file.
func (g *ghFake) file(name, text string) {
	g.ok("GET", memberPath(name), map[string]any{"name": name + ".md", "sha": blobOf(name), "size": len(text), "encoding": "base64", "content": b64(text)})
}

const commitSHA = "0123456789abcdef0123456789abcdef01234567"

func (g *ghFake) commitOK(method, name string) {
	g.ok(method, memberPath(name), map[string]any{"commit": map[string]any{"sha": commitSHA, "html_url": "javascript:alert(1)"}, "content": map[string]any{"sha": blobOf(name + "-new")}})
}

// pubFixture is a crewapi service wired to the fake gh and the fake gitleaks.
type pubFixture struct {
	*fixture
	gh    *ghFake
	leaks string // $FAKEGITLEAKS_DIR
	// builtIns is the Commodore crew directory.
	builtIns string
	// svc is the service behind the server, for a test that runs its background check.
	svc *crewapi.Service
}

type pubOpts struct {
	off       bool   // crew sharing off
	noRepo    bool   // no share_repo yet
	noGitHub  bool   // the fleet has no GitHub source
	host      string // GitHub host; default github.com
	noGitleak bool   // the fleet PATH lacks the fake gitleaks
}

func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func newPub(t *testing.T, o pubOpts) *pubFixture {
	t.Helper()
	builtIns := commodoreAt(t)
	g := newGHFake(t)
	leaks := t.TempDir()
	path := filepath.Join(repoRoot(t), "e2e", "testdata", "fakegitleaks") + ":/usr/bin:/bin"
	if o.noGitleak {
		path = "/usr/bin:/bin"
	}
	repo := `,"share_repo":"` + shareRepo + `"`
	if o.noRepo {
		repo = ""
	}
	envJSON, _ := json.Marshal(map[string]string{"PATH": path, "FAKEGITLEAKS_DIR": leaks})
	cfg := `{"version":1,"fleets":{"work":{"name":"Work","claude":{},"codex":{},"env":` + string(envJSON) +
		`,"crew_sharing":{"enabled":` + strconv.FormatBool(!o.off) + repo + `}}}}`
	root, _ := filepath.EvalSymlinks(t.TempDir())
	f := &fixture{t: t, home: filepath.Join(root, "home")}
	if err := os.MkdirAll(f.home, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.home, config.FileName), cfg)
	m, err := config.Open(f.home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	host := o.host
	if host == "" {
		host = "github.com"
	}
	svc := &crewapi.Service{Home: f.home, Config: m, ScanTimeout: 2 * time.Second,
		GitHub: func(*config.Config, string) *github.Client {
			if o.noGitHub {
				return nil
			}
			c := github.NewClient(host, "", nil, []string{"FAKEGH_DIR=" + g.dir, "PATH=/usr/bin:/bin"})
			c.Gh = filepath.Join(repoRoot(t), "e2e", "testdata", "fakegh", "gh")
			return c
		}}
	mux := http.NewServeMux()
	svc.Routes(mux)
	// A request that carries botHeader runs as a fleet actor (AdmiralBot), the way the guard's middleware sets it.
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fleet := r.Header.Get(botHeader); fleet != "" {
			r = r.WithContext(actor.WithFleet(r.Context(), fleet))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return &pubFixture{fixture: f, gh: g, leaks: leaks, builtIns: builtIns}
}

func (p *pubFixture) base() string { return "/api/fleets/work/crew" }

func (p *pubFixture) post(path string, body any) (int, map[string]any) {
	p.t.Helper()
	status, text := p.do("POST", p.base()+path, body)
	return status, parse(p.t, text)
}

func (p *pubFixture) getJSON(path string) (int, map[string]any) {
	p.t.Helper()
	status, text := p.do("GET", p.base()+path, nil)
	return status, parse(p.t, text)
}

func parse(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	return out
}

// asBot sends the request as a non-engineer actor of the fleet.
func (p *pubFixture) asBot(method, path string) (int, string) {
	p.t.Helper()
	req, _ := http.NewRequest(method, p.srv.URL+p.base()+path, bytes.NewReader(nil))
	req.Header.Set(botHeader, "work")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

// member writes a fleet member whose instructions are body.
func (p *pubFixture) member(name, body string) {
	p.t.Helper()
	writeFile(p.t, filepath.Join(p.crewDir(), name+".md"), "---\nname: "+name+"\nrole: triager\npick-me-when: Shared "+name+".\n---\n\n"+body+"\n")
}

func (p *pubFixture) preview(name string) (int, map[string]any) {
	p.t.Helper()
	return p.post("/members/"+name+"/share/preview", map[string]any{})
}

func str(m map[string]any, key string) string { s, _ := m[key].(string); return s }

func errText(m map[string]any) string { return str(m, "error") }

// share posts the Share body a page would send from a preview, with overrides.
func (p *pubFixture) share(name string, pv map[string]any, extra map[string]any) (int, map[string]any) {
	p.t.Helper()
	body := map[string]any{"sha256": pv["sha256"], "repo": pv["repo"], "replaces_blob": pv["replaces_blob"]}
	if private, has := pv["private"]; has {
		body["private"] = private
	}
	for k, v := range extra {
		body[k] = v
	}
	return p.post("/members/"+name+"/share", body)
}

func (p *pubFixture) leakFail(mode string) { writeFile(p.t, filepath.Join(p.leaks, "fail"), mode) }

func (p *pubFixture) leakVersion(v string) { writeFile(p.t, filepath.Join(p.leaks, "version"), v) }

// putBody decodes the JSON a recorded PUT or DELETE sent on stdin.
func putBody(t *testing.T, c ghCall) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(c.Stdin), &body); err != nil {
		t.Fatalf("%s %s stdin %q is not JSON: %v", c.Method, c.Endpoint, c.Stdin, err)
	}
	return body
}

func decodeContent(t *testing.T, body map[string]any) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(str(body, "content"))
	if err != nil {
		t.Fatalf("content is not base64: %v", err)
	}
	return string(b)
}
