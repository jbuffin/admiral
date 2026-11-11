package launchorders_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launchorders"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/state"
)

type fixture struct {
	t     *testing.T
	home  string
	work  string
	state *state.Store
	cfg   *config.Manager
	ships []ships.Ship
	srv   *httptest.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	f := &fixture{t: t, home: filepath.Join(root, "home"), work: filepath.Join(root, "work")}
	for _, d := range []string{f.home, f.work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := `{"version":1,"fleets":{
	  "work":{"name":"Work","search_roots":[` + quote(f.work) + `],"claude":{},"codex":{}},
	  "home":{"name":"Home","search_roots":[` + quote(f.work) + `],"claude":{}},
	  "nosearch":{"name":"No Search","claude":{}}}}`
	if err := os.WriteFile(filepath.Join(f.home, config.FileName), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	if f.cfg, err = config.Open(f.home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.cfg.Close)
	if f.state, err = state.Open(f.home); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&launchorders.Service{Config: f.cfg, State: f.state, Ships: func() []ships.Ship { return f.ships }}).Routes(mux)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

func (f *fixture) repo(rel string, remotes ...string) string {
	f.t.Helper()
	d := filepath.Join(f.work, rel)
	if err := os.MkdirAll(d, 0o755); err != nil {
		f.t.Fatal(err)
	}
	args := [][]string{{"init", "-q"}}
	for i, r := range remotes {
		args = append(args, []string{"remote", "add", "r" + string(rune('0'+i)), r})
	}
	for _, a := range args {
		cmd := exec.Command("git", a...)
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			f.t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	return d
}

func (f *fixture) launched(fleet, harness, dir string, at time.Time) {
	f.state.SetLastDir(fleet, harness, dir)
	f.ships = append(f.ships, ships.Ship{Fleet: fleet, Harness: harness, Dir: dir, Status: ships.StatusUnderway, Launched: at.UTC().Format(time.RFC3339)})
}

func (f *fixture) do(method, path string, body any) (int, string) {
	f.t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

type dirsResp struct {
	Entries []struct {
		Path      string `json:"path"`
		Kind      string `json:"kind"`
		ShipsHere int    `json:"ships_here"`
	} `json:"entries"`
	Selected *string `json:"selected"`
	Reason   *string `json:"reason"`
}

func (r dirsResp) paths() []string {
	p := []string{}
	for _, e := range r.Entries {
		p = append(p, e.Path)
	}
	return p
}

func (r dirsResp) pick() (string, string) {
	if r.Selected == nil {
		return "", ""
	}
	return *r.Selected, *r.Reason
}

func (f *fixture) dirs(fleet, harness, repo, text string) dirsResp {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/launch-orders/dirs", map[string]string{"fleet": fleet, "harness": harness, "repo": repo, "text": text})
	if code != http.StatusOK {
		f.t.Fatalf("dirs = %d: %s", code, body)
	}
	var r dirsResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		f.t.Fatal(err)
	}
	return r
}

func TestPreselection(t *testing.T) {
	f := newFixture(t)
	x := f.repo("x", "https://github.com/acme/x")
	y := f.repo("y", "git@github.com:acme/y.git")
	z := f.repo("z")
	f.repo("four") // a folder name is not a remote

	check := func(name, fleet, harness, repo, text, wantDir, wantReason string) {
		t.Helper()
		if dir, why := f.dirs(fleet, harness, repo, text).pick(); dir != wantDir || why != wantReason {
			t.Errorf("%s: selected %q (%s), want %q (%s)", name, dir, why, wantDir, wantReason)
		}
	}
	check("repo hint", "work", "claude", "https://github.com/acme/x", "", x, "repo")
	check("first URL in the text that matches", "work", "claude", "", "https://example.com/nope https://github.com/acme/y and https://github.com/acme/x", y, "text")
	check("hint beats text", "work", "claude", "https://github.com/acme/x", "https://github.com/acme/y", x, "repo")
	check("hint that matches nothing falls to the text", "work", "claude", "https://github.com/acme/none", "https://github.com/acme/y", y, "text")
	check("never by folder name", "work", "claude", "https://github.com/acme/four", "", "", "")

	f.state.SetLastDir("work", "claude", z)
	check("this dock's last directory", "work", "claude", "", "", z, "last_used")
	check("a match beats the last used", "work", "claude", "https://github.com/acme/x", "", x, "repo")
	check("another dock", "work", "codex", "", "", "", "")
	check("another fleet's dock", "home", "claude", "", "", "", "")
	if err := os.RemoveAll(z); err != nil {
		t.Fatal(err)
	}
	check("a remembered directory that is gone", "work", "claude", "", "", "", "")
}

func TestSeveralMatchesFloatTheNewestShipFirst(t *testing.T) {
	f := newFixture(t)
	p1 := f.repo("p1", "git@github.com:acme/shared.git")
	p2 := f.repo("p2", "https://github.com/acme/shared")
	f.repo("p3", "https://github.com/acme/other")
	now := time.Now()
	f.launched("work", "claude", p1, now.Add(-time.Hour))
	f.launched("work", "claude", p2, now)

	r := f.dirs("work", "claude", "https://github.com/acme/shared", "")
	if dir, why := r.pick(); dir != p2 || why != "repo" {
		t.Errorf("selected %q (%s), want %q, which has the newer ship", dir, why, p2)
	}
	if got := r.paths(); len(got) != 4 || got[0] != p2 || got[1] != p1 || got[3] != f.work {
		t.Errorf("entries = %v, want p2, p1 first and the search root last", got)
	}
}

func TestCheckoutsMostRecentlyUsedFirst(t *testing.T) {
	f := newFixture(t)
	a := f.repo("a")
	b := f.repo("b", "https://github.com/acme/b")
	c := f.repo("c")
	f.state.SetLastDir("work", "claude", c)
	time.Sleep(time.Millisecond)
	f.state.SetLastDir("work", "codex", a) // on another dock of the fleet
	want := []string{a, c, b, f.work}

	if got := f.dirs("work", "claude", "", "").paths(); !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %v, want %v with no ships, from the remembered launches", got, want)
	}
	r := f.dirs("work", "claude", "https://github.com/acme/b", "")
	if got := r.paths(); !reflect.DeepEqual(got, want) {
		t.Errorf("with a single match, entries = %v, want the order unchanged %v", got, want)
	}
}

func TestShipsHereCountsLiveShipsFromAnyFleet(t *testing.T) {
	f := newFixture(t)
	a, b := f.repo("a"), f.repo("b")
	f.launched("work", "claude", a, time.Now())
	f.launched("home", "claude", a, time.Now())
	f.launched("work", "claude", b, time.Now())
	f.ships = append(f.ships, ships.Ship{Dir: b, Status: ships.StatusAnchored})
	got := map[string]int{}
	for _, e := range f.dirs("work", "claude", "", "").Entries {
		got[e.Path] = e.ShipsHere
	}
	if got[a] != 2 || got[b] != 1 || got[f.work] != 0 {
		t.Errorf("ships_here = %v, want a:2 b:1 and the search root 0", got)
	}
}

func TestDirsAndSkillsRefusals(t *testing.T) {
	f := newFixture(t)
	a := f.repo("a")
	if err := os.MkdirAll(filepath.Join(a, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, fleet, harness string
		status               int
		msg                  string
	}{
		{"no search roots", "nosearch", "claude", http.StatusConflict, "This fleet has no search roots"},
		{"unknown fleet", "nope", "claude", http.StatusNotFound, ""},
		{"harness not in the fleet", "home", "codex", http.StatusBadRequest, ""},
	} {
		code, body := f.do(http.MethodPost, "/api/launch-orders/dirs", map[string]string{"fleet": c.fleet, "harness": c.harness})
		if code != c.status || !strings.Contains(body, c.msg) {
			t.Errorf("dirs, %s = %d %s, want %d naming %q", c.name, code, body, c.status, c.msg)
		}
	}
	skills := func(dir string) int {
		code, _ := f.do(http.MethodGet, "/api/launch-orders/skills?"+url.Values{"fleet": {"work"}, "harness": {"claude"}, "dir": {dir}}.Encode(), nil)
		return code
	}
	if code := skills(filepath.Join(a, "sub")); code != http.StatusBadRequest {
		t.Errorf("skills for a folder inside a checkout = %d, want 400", code)
	}
	if code := skills(a); code != http.StatusOK {
		t.Errorf("skills for a listed checkout = %d, want 200", code)
	}
}

func TestFavorites(t *testing.T) {
	f := newFixture(t)
	put := func(skills ...string) (int, string) {
		return f.do(http.MethodPut, "/api/favorites", map[string]any{"skills": skills})
	}
	five := []string{"f1", "f2", "f3", "f4", "f5"}
	if code, body := put(five...); code != http.StatusOK || !strings.Contains(body, `"favorite_skills":["f1","f2","f3","f4","f5"]`) {
		t.Fatalf("PUT five = %d %s, want 200 and the new favorites", code, body)
	}
	if got := f.cfg.View().FavoriteSkills; !reflect.DeepEqual(got, five) {
		t.Errorf("the config view right after the PUT has %v, want %v", got, five)
	}
	if code, body := put(append(five, "f6")...); code != http.StatusBadRequest || !strings.Contains(body, "Unstar one first") {
		t.Errorf("PUT six = %d %s, want 400 \"Unstar one first\"", code, body)
	}
	if code, _ := put("f1", "f1"); code != http.StatusBadRequest {
		t.Errorf("a repeated favorite = %d, want 400, not a silent dedupe", code)
	}
	if got := f.cfg.View().FavoriteSkills; !reflect.DeepEqual(got, five) {
		t.Errorf("a refused PUT changed the favorites to %v", got)
	}
}

func TestFavoritesNeverReplaceADanglingSymlink(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.home, config.FileName)
	real := filepath.Join(t.TempDir(), "real.json")
	if err := os.Rename(path, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(real); err != nil {
		t.Fatal(err)
	}
	if code, body := f.do(http.MethodPut, "/api/favorites", map[string]any{"skills": []string{"f1"}}); code < 400 {
		t.Errorf("PUT with the symlink's target missing = %d, want an error: %s", code, body)
	}
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("config.json must still be the symlink (err %v)", err)
	}
}
