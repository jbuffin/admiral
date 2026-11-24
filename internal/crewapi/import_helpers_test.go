package crewapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crewapi"
	"github.com/jbuffin/admiral/internal/github"
)

// impOpts extends pubOpts with what the import routes read from the fleet.
type impOpts struct {
	pubOpts
	sources   []string
	installed []string // skills the fleet's claude dock can see
}

// newImp is newPub with crew_sharing.sources and a dock that sees only the installed skills.
func newImp(t *testing.T, o impOpts) *pubFixture {
	t.Helper()
	builtIns := commodoreAt(t)
	identity := filepath.Dir(filepath.Dir(filepath.Dir(builtIns)))
	userHome := t.TempDir()
	for _, s := range o.installed {
		writeFile(t, filepath.Join(identity, "skills", s, "SKILL.md"), "---\nname: "+s+"\ndescription: x\n---\n")
	}
	g := newGHFake(t)
	leaks := t.TempDir()
	path := filepath.Join(repoRoot(t), "e2e", "testdata", "fakegitleaks") + ":/usr/bin:/bin"
	if o.noGitleak {
		path = "/usr/bin:/bin"
	}
	sharing := map[string]any{"enabled": !o.off, "sources": o.sources}
	if sharing["sources"] == nil {
		sharing["sources"] = []string{}
	}
	if !o.noRepo {
		sharing["share_repo"] = shareRepo
	}
	fleet := map[string]any{
		"name":         "Work",
		"claude":       map[string]any{"env": map[string]string{"HOME": userHome, "CLAUDE_CONFIG_DIR": identity}},
		"codex":        map[string]any{},
		"env":          map[string]string{"PATH": path, "FAKEGITLEAKS_DIR": leaks},
		"crew_sharing": sharing,
	}
	cfg, _ := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{"work": fleet}})
	root, _ := filepath.EvalSymlinks(t.TempDir())
	f := &fixture{t: t, home: filepath.Join(root, "home")}
	if err := os.MkdirAll(f.home, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.home, config.FileName), string(cfg))
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
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fleet := r.Header.Get(botHeader); fleet != "" {
			r = r.WithContext(actor.WithFleet(r.Context(), fleet))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return &pubFixture{fixture: f, gh: g, leaks: leaks, builtIns: builtIns}
}

// upstream is one member in a source repo, as the fake GitHub answers for it.
type upstream struct {
	owner, repo, name string
	text              string
	ref               string // "" means the default branch
	branch            string // the repo's default branch; "main" when empty
	commit            string // "" means commitSHA
	tags              []string
}

func (u upstream) sha() string {
	if u.commit != "" {
		return u.commit
	}
	return commitSHA
}

func (u upstream) full() string { return u.owner + "/" + u.repo }

func (u upstream) blob() string { return blobOf(u.name + "-upstream") }

// repoAs is the repo answer for any owner/repo (repoAnswer always names the share repo).
func repoAs(full, branch string) map[string]any {
	return map[string]any{"full_name": full, "visibility": "public", "private": false, "archived": false,
		"permissions": map[string]any{"push": false}, "default_branch": branch}
}

// seed cans the repo, the commit, the file at the commit and the tags for u.
func (g *ghFake) seed(u upstream) {
	g.t.Helper()
	branch := u.branch
	if branch == "" {
		branch = "main"
	}
	ref := u.ref
	if ref == "" {
		ref = branch
	}
	g.ok("GET", "repos/"+u.full(), repoAs(u.full(), branch))
	g.ok("GET", "repos/"+u.full()+"/commits/"+strings.ReplaceAll(url.PathEscape(ref), "/", "%2F"), map[string]any{"sha": u.sha()})
	g.ok("GET", "repos/"+u.full()+"/contents/crew/"+u.name+".md?ref="+u.sha(), map[string]any{"name": u.name + ".md", "sha": u.blob(), "size": len(u.text), "encoding": "base64", "content": b64(u.text)})
	var tags []map[string]any
	for _, t := range u.tags {
		tags = append(tags, map[string]any{"name": t, "commit": map[string]any{"sha": u.sha()}})
	}
	g.ok("GET", "repos/"+u.full()+"/tags?per_page=100", tags)
}

// unseed removes every canned answer, so a test can seed a changed upstream.
func (g *ghFake) unseed() {
	g.t.Helper()
	if err := os.RemoveAll(filepath.Join(g.dir, "rest")); err != nil {
		g.t.Fatal(err)
	}
}

// reads returns the recorded GET calls.
func (g *ghFake) reads() []ghCall {
	g.t.Helper()
	var out []ghCall
	for _, c := range g.calls() {
		if c.Method == "GET" {
			out = append(out, c)
		}
	}
	return out
}

// memberFile is a valid upstream member file with the given extra frontmatter lines.
func memberFile(name, extra string) string {
	return "---\nname: " + name + "\nrole: triager\npick-me-when: Pick " + name + ".\n" + extra + "---\n\nWork as " + name + ".\n"
}

func (p *pubFixture) importPreview(ref string) (int, map[string]any) {
	p.t.Helper()
	return p.post("/imports/preview", map[string]any{"ref": ref})
}

// importFrom posts the Import body a page would send from a preview, with overrides.
func (p *pubFixture) importFrom(pv map[string]any, extra map[string]any) (int, map[string]any) {
	p.t.Helper()
	body := map[string]any{"repo": str(pv, "owner") + "/" + str(pv, "repo"), "path": pv["path"], "commit": pv["commit"], "sha256": pv["sha256"]}
	for k, v := range extra {
		body[k] = v
	}
	return p.post("/imports", body)
}

// importOK previews ref and imports it, failing the test on any other status.
func (p *pubFixture) importOK(ref string, extra map[string]any) map[string]any {
	p.t.Helper()
	status, pv := p.importPreview(ref)
	if status != 200 {
		p.t.Fatalf("preview %s: %d %v", ref, status, pv)
	}
	status, out := p.importFrom(pv, extra)
	if status != 200 {
		p.t.Fatalf("import %s: %d %v", ref, status, out)
	}
	return out
}

// wantNothingImported fails when the crew directory holds anything but the files in keep.
func (p *pubFixture) wantNothingImported(what string, keep ...string) {
	p.t.Helper()
	entries, _ := os.ReadDir(p.crewDir())
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		kept := false
		for _, k := range keep {
			kept = kept || k == e.Name()
		}
		if !kept {
			p.t.Errorf("%s: %s is in the fleet crew directory", what, e.Name())
		}
	}
}

func (p *pubFixture) put(path string, body any) (int, map[string]any) {
	p.t.Helper()
	status, text := p.do("PUT", p.base()+path, body)
	return status, parse(p.t, text)
}

// savedSources reads crew_sharing.sources from the config file on disk.
func (p *pubFixture) savedSources() []string {
	p.t.Helper()
	var cfg struct {
		Fleets map[string]struct {
			CrewSharing struct {
				Sources []string `json:"sources"`
			} `json:"crew_sharing"`
		} `json:"fleets"`
	}
	if err := json.Unmarshal([]byte(slurp(filepath.Join(p.home, config.FileName))), &cfg); err != nil {
		p.t.Fatal(err)
	}
	return cfg.Fleets["work"].CrewSharing.Sources
}

// cardOf is the preview's parsed card.
func cardOf(m map[string]any) map[string]any {
	c, _ := m["card"].(map[string]any)
	return c
}

func itoa(n int) string { return strconv.Itoa(n) }
