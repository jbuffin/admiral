package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Launch-orders data scenarios (u6): the repo finder, the skill index, the
// Commodore lookup, favorites and the launch re-check, through the real binary
// and its HTTP API (POST /api/launch-orders/dirs, GET /api/launch-orders/skills,
// PUT /api/favorites, POST /api/ships).

func sorted(s []string) []string {
	c := append([]string{}, s...)
	sort.Strings(c)
	return c
}

func sameSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(sorted(got), sorted(want)) {
		t.Errorf("%s = %v, want exactly %v", what, sorted(got), sorted(want))
	}
}

// S1: checkouts up to two levels below a search root, never inside a checkout,
// then the search roots; a worktree is a checkout and its remotes are read
// through its .git file; the list is read fresh.
func TestRepoFinderListing(t *testing.T) {
	d := newDataWorld(t)
	a := d.repo("a", "git@github.com:acme/a.git")
	b := d.repo("grp/b", "https://github.com/acme/b")
	d.repo("a/inner", "https://github.com/acme/inner") // inside a checkout: not listed
	if err := os.MkdirAll(filepath.Join(d.work, "plain", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	d.repo("d1/d2/d3", "https://github.com/acme/deep") // three levels down: not listed
	wt := d.worktree(a, "wt")
	d.start()

	r := d.dirs("work", "claude", "", "")
	if got, want := len(r.Entries), 4; got != want {
		t.Fatalf("entries = %v, want the three checkouts then the search root", r.paths())
	}
	sameSet(t, "the checkouts", r.paths()[:3], []string{a, b, wt})
	for _, e := range r.Entries[:3] {
		if e.Kind != "checkout" || e.Name != filepath.Base(e.Path) {
			t.Errorf("entry %+v: want kind checkout and the base name", e)
		}
	}
	if last := r.Entries[3]; last.Path != d.work || last.Kind != "search_root" || last.Name != filepath.Base(d.work) {
		t.Errorf("last entry = %+v, want the search root %s (kind search_root)", last, d.work)
	}
	remotes := map[string][]string{}
	for _, e := range r.Entries {
		remotes[e.Path] = e.Remotes
		if e.ShipsHere != 0 {
			t.Errorf("%s ships_here = %d, want 0", e.Path, e.ShipsHere)
		}
	}
	if len(remotes[a]) == 0 || !reflect.DeepEqual(remotes[wt], remotes[a]) {
		t.Errorf("worktree remotes = %v, want its main checkout's %v (read through the .git file)", remotes[wt], remotes[a])
	}

	d.repo("late", "https://github.com/acme/late")
	if got := d.dirs("work", "claude", "", "").paths(); len(got) != 5 || !contains(got, filepath.Join(d.work, "late")) {
		t.Errorf("a checkout made after the first request is missing from the next: %v", got)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// A search root that is itself a checkout stays a search root, and its remotes
// still pre-select it.
func TestSearchRootThatIsACheckout(t *testing.T) {
	d := newDataWorld(t)
	d.git(d.work, "init", "-q")
	d.git(d.work, "remote", "add", "origin", "https://github.com/acme/mono.git")
	d.start()
	r := d.dirs("work", "claude", "git@github.com:acme/mono.git", "")
	last := r.Entries[len(r.Entries)-1]
	if last.Path != d.work || last.Kind != "search_root" || len(last.Remotes) == 0 {
		t.Fatalf("last entry = %+v, want the search root with its remotes read", last)
	}
	if r.selected() != d.work || r.reason() != "repo" {
		t.Errorf("selected %q (%s), want the search root by repo", r.selected(), r.reason())
	}
}

// S2: remotes match by host and owner/repo, in any spelling, never by folder name.
func TestRepoURLMatching(t *testing.T) {
	d := newDataWorld(t)
	one := d.repo("one", "git@GitHub.com:acme/one.git")
	two := d.repo("two", "https://github.com/acme/two")
	d.repo("three", "https://gitlab.com/acme/three.git")
	d.repo("four") // no remote
	five := d.repo("five", "https://oauth2:tok-secret-55@github.com/acme/five.git")
	yy := d.repo("yy", "https://github.com/acme/y")
	qq := d.repo("qq", "https://user:pa?ss-tok-77@github.com/acme/qq.git")
	d.repo("tt", "https://user:tok/a/b@github.com/acme/tt.git")
	d.repo("uu", "https://user:pa?a/b/c-tok-88@github.com/acme/uu.git")
	six := d.repo("six", "https://user:tok/secret-66@github.com/acme/six.git")
	d.start()

	cases := []struct{ hint, want string }{
		{"https://github.com/acme/one", one},
		{"https://github.com/acme/one.git", one},
		{"ssh://git@github.com/acme/one.git", one},
		{"https://GITHUB.COM/acme/one/", one},
		{"git@github.com:acme/two.git", two},
		{"https://github.com/acme/two/", two},
		{"https://github.com/acme/five", five},
		{"https://github.com/acme/six", six},
		{"https://github.com/acme/qq", qq},
		{"https://github.com/acme/y/blob/main/img@2x.png", yy}, // an "@" in the path is not userinfo
		{"https://github.com/acme/y/pull/5#a@b", yy},
		{"https://github.com/acme/y/tree/x@evil.com/o/r", yy},
		{"https://github.com/acme/three", ""}, // same owner/repo, another host
		{"https://github.com/acme/four", ""},  // a folder name is not a remote
	}
	for _, c := range cases {
		t.Run(c.hint, func(t *testing.T) {
			code, body := d.postDirs("work", "claude", c.hint, "")
			if code != http.StatusOK {
				t.Fatalf("status %d: %s", code, body)
			}
			if strings.Contains(body, "tok-secret-55") || strings.Contains(body, "secret-66") || strings.Contains(body, "tok-77") || strings.Contains(body, "tok-88") || strings.Contains(body, "b@github") {
				t.Errorf("the response carries a credential from a remote URL: %s", body)
			}
			var r dirsResp
			if err := json.Unmarshal([]byte(body), &r); err != nil {
				t.Fatal(err)
			}
			if r.selected() != c.want {
				t.Errorf("selected %q, want %q", r.selected(), c.want)
			}
			if c.want != "" && r.reason() != "repo" {
				t.Errorf("reason %q, want repo", r.reason())
			}
			if c.want == "" && r.reason() != "" {
				t.Errorf("reason %q with nothing selected, want null", r.reason())
			}
		})
	}
}

// S2: repo hint, then a URL in the text (first that matches), then the dock's
// last directory, skipping one that is gone.
func TestPreselectionOrder(t *testing.T) {
	d := newDataWorld(t)
	x := d.repo("x", "https://github.com/acme/x")
	y := d.repo("y", "git@github.com:acme/y.git")
	z := d.repo("z")
	d.start()

	check := func(name, fleet, harness, repo, text, wantDir, wantReason string) {
		t.Helper()
		r := d.dirs(fleet, harness, repo, text)
		if r.selected() != wantDir || r.reason() != wantReason {
			t.Errorf("%s: selected %q (%s), want %q (%s)", name, r.selected(), r.reason(), wantDir, wantReason)
		}
	}
	check("repo hint", "work", "claude", "https://github.com/acme/x", "", x, "repo")
	check("text URL", "work", "claude", "", "see https://github.com/acme/y/issues/3 please", y, "text")
	check("first URL that matches wins", "work", "claude", "", "https://example.com/nope https://github.com/acme/y and https://github.com/acme/x", y, "text")
	check("hint beats text", "work", "claude", "https://github.com/acme/x", "https://github.com/acme/y", x, "repo")
	check("hint that matches nothing falls to the text", "work", "claude", "https://github.com/acme/none", "https://github.com/acme/y", y, "text")
	check("nothing matches and nothing was used", "work", "claude", "", "just words", "", "")

	d.launch(d.launchReq("work", "claude", y+"/", noSkill, "go")) // a non-clean path: the cleaned one is remembered
	check("last used is the cleaned path", "work", "claude", "", "", y, "last_used")
	d.launch(d.launchReq("work", "claude", z, noSkill, "go"))
	check("last used on this dock", "work", "claude", "", "", z, "last_used")
	check("a match beats the last used", "work", "claude", "https://github.com/acme/x", "", x, "repo")
	check("another dock never launched", "work", "codex", "", "", "", "")
	check("another fleet's dock", "home", "claude", "", "", "", "")

	if err := os.RemoveAll(z); err != nil {
		t.Fatal(err)
	}
	check("a remembered directory that is gone", "work", "claude", "", "", "", "")
	if got := d.dirs("work", "claude", "", "").paths(); contains(got, z) {
		t.Errorf("the deleted checkout is still listed: %v", got)
	}
}

// S2: several checkouts share a remote: the one with the newest ship is
// selected and the matches float to the top, newest first.
func TestSeveralMatchesFloatMostRecentlyUsed(t *testing.T) {
	d := newDataWorld(t)
	p1 := d.repo("p1", "git@github.com:acme/shared.git")
	p2 := d.repo("p2", "https://github.com/acme/shared")
	d.repo("p3", "https://github.com/acme/other")
	d.start()

	d.launch(d.launchReq("work", "claude", p1, noSkill, "go"))
	time.Sleep(1100 * time.Millisecond) // @admiral-launched has one-second resolution
	d.launch(d.launchReq("work", "claude", p2, noSkill, "go"))

	r := d.dirs("work", "claude", "https://github.com/acme/shared", "")
	if r.selected() != p2 || r.reason() != "repo" {
		t.Errorf("selected %q (%s), want %q: the newer ship is in p2", r.selected(), r.reason(), p2)
	}
	if got := r.paths(); len(got) < 3 || got[0] != p2 || got[1] != p1 {
		t.Errorf("entries = %v, want the matches p2, p1 first", got)
	}
	if last := r.Entries[len(r.Entries)-1]; last.Kind != "search_root" {
		t.Errorf("the search roots stay last, got %+v", last)
	}
}

// The checkouts are listed most recently used first, on any of the fleet's
// docks, and the order survives the ships and a restart. The pre-selection is
// unchanged by it.
func TestDirsMostRecentlyUsedFirst(t *testing.T) {
	d := newDataWorld(t)
	a := d.repo("a")
	b := d.repo("b", "https://github.com/acme/b")
	c := d.repo("c")
	d.start()

	d.launch(d.launchReq("work", "claude", c, noSkill, "go"))
	d.launch(d.launchReq("work", "codex", a, noSkill, "go"))
	want := []string{a, c, b, d.work}

	r := d.dirs("work", "claude", "", "")
	if got := r.paths(); !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if r.selected() != c || r.reason() != "last_used" {
		t.Errorf("selected %q (%s), want this dock's last directory %q", r.selected(), r.reason(), c)
	}
	r = d.dirs("work", "claude", "https://github.com/acme/b", "")
	if got := r.paths(); !reflect.DeepEqual(got, want) {
		t.Errorf("with a match, entries = %v, want %v", got, want)
	}
	if r.selected() != b || r.reason() != "repo" {
		t.Errorf("selected %q (%s), want the match %q", r.selected(), r.reason(), b)
	}

	d.in.Stop()
	if _, err := d.tmux("kill-server"); err != nil {
		t.Fatal(err)
	}
	d.start()
	if got := d.dirs("work", "claude", "", "").paths(); !reflect.DeepEqual(got, want) {
		t.Errorf("with no ships after a restart, entries = %v, want %v", got, want)
	}
}

// S3: ships_here counts the ships that are underway or need you in a directory,
// whichever fleet launched them.
func TestShipsHere(t *testing.T) {
	d := newDataWorld(t)
	a := d.repo("a")
	b := d.repo("b")
	d.start()
	d.launch(d.launchReq("work", "claude", a, noSkill, "go"))
	d.launch(d.launchReq("home", "codex", a, noSkill, "go"))
	d.launch(d.launchReq("work", "claude", b, noSkill, "go"))
	got := map[string]int{}
	for _, e := range d.dirs("work", "claude", "", "").Entries {
		got[e.Path] = e.ShipsHere
	}
	if got[a] != 2 || got[b] != 1 || got[d.work] != 0 {
		t.Errorf("ships_here = %v, want a:2 b:1 and the search root 0", got)
	}
}

// S8: a fleet with no search roots refuses (story 29), and so do an unknown
// fleet and a harness the fleet doesn't have.
func TestDirsRefusals(t *testing.T) {
	d := newDataWorld(t)
	d.start()
	code, body := d.postDirs("nosearch", "claude", "", "")
	if code != http.StatusConflict {
		t.Errorf("a fleet with no search roots = %d, want 409: %s", code, body)
	}
	if msg := requireRefusal(t, code, body); !strings.Contains(msg, "This fleet has no search roots") {
		t.Errorf("error %q, want it to say \"This fleet has no search roots\"", msg)
	}
	for name, c := range map[string][2]string{"unknown fleet": {"nope", "claude"}, "harness not in the fleet": {"home", "copilot"}} {
		code, body := d.postDirs(c[0], c[1], "", "")
		t.Run(name, func(t *testing.T) { requireRefusal(t, code, body) })
	}
}

// S4: the skill index reads each harness's own locations and nothing else.
func TestSkillIndexLocationsPerHarness(t *testing.T) {
	d := newDataWorld(t)
	p1 := d.repo("p1")
	p2 := d.repo("p2")
	writeSkill(t, filepath.Join(d.claude.dir, "skills"), "u-claude")
	writeSkill(t, filepath.Join(d.claude.dir, "skills"), "dup")
	if err := os.MkdirAll(filepath.Join(d.claude.dir, "skills", "no-skill-file"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(p1, ".claude", "skills"), "p-claude")
	writeSkill(t, filepath.Join(p1, ".claude", "skills"), "dup")
	writeSkill(t, filepath.Join(p1, ".agents", "skills"), "p-agents")
	writeSkill(t, filepath.Join(p1, ".github", "skills"), "p-github")
	writeSkill(t, filepath.Join(d.fh, ".agents", "skills"), "h-agents")
	writeSkill(t, filepath.Join(d.codex, "skills"), "u-codex")
	writeSkill(t, filepath.Join(d.cop, "skills"), "u-copilot")
	// Defaults that the fleet's own variables replace.
	writeSkill(t, filepath.Join(d.fh, ".claude", "skills"), "h-claude")
	writeSkill(t, filepath.Join(d.fh, ".copilot", "skills"), "h-copilot")
	d.start()

	cases := []struct {
		harness, dir string
		want         []string
	}{
		{"claude", p1, []string{"u-claude", "p-claude", "dup"}},
		{"claude", p2, []string{"u-claude", "dup"}},
		{"codex", p1, []string{"h-agents", "u-codex", "p-agents"}},
		{"codex", p2, []string{"h-agents", "u-codex"}},
		{"copilot", p1, []string{"u-copilot", "h-agents", "p-github", "p-agents", "p-claude", "dup"}},
		{"copilot", p2, []string{"u-copilot", "h-agents"}},
	}
	for _, c := range cases {
		t.Run(c.harness+"/"+filepath.Base(c.dir), func(t *testing.T) {
			sameSet(t, "skills", d.skills(t, "work", c.harness, c.dir).names(), c.want)
		})
	}
	// Read fresh on every request.
	writeSkill(t, filepath.Join(d.claude.dir, "skills"), "late")
	if got := d.skills(t, "work", "claude", p2).names(); !contains(got, "late") {
		t.Errorf("a skill installed after the first request is missing: %v", got)
	}
}

// S4: with no identity variable, the default directories come from the fleet's
// own HOME, not Admiral's.
func TestSkillIndexExpandsHomeFromTheFleetEnvironment(t *testing.T) {
	w := newWorld(t)
	fh := filepath.Join(w.root, "fleet-home")
	proj := w.repo("proj")
	writeSkill(t, filepath.Join(fh, ".claude", "skills"), "fleet-claude")
	writeSkill(t, filepath.Join(fh, ".agents", "skills"), "fleet-agents")
	writeSkill(t, filepath.Join(w.userHom, ".claude", "skills"), "admiral-claude")
	env := w.harnessEnv()
	env["HOME"] = fh
	dock := func(h string) map[string]any { d := w.dock(h); d["env"] = env; return d }
	b, _ := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{
		"work": map[string]any{"name": "Work", "search_roots": []string{w.work}, "claude": dock("claude"), "codex": dock("codex")}}})
	writeConfig(t, w.home, string(b))
	w.start()
	sameSet(t, "claude skills", w.skills(t, "work", "claude", proj).names(), []string{"fleet-claude"})
	sameSet(t, "codex skills", w.skills(t, "work", "codex", proj).names(), []string{"fleet-agents"})
}

// S4: Claude Code plugins: installed_plugins.json, enabledPlugins (user and
// project settings), defaultEnabled, nested skill folders, manifest-declared
// paths, installs outside the cache, skills-directory plugins, and the decoy
// secrets in settings.json that must never come back.
func TestSkillIndexClaudePlugins(t *testing.T) {
	d := newDataWorld(t)
	p1 := d.repo("p1")
	p2 := d.repo("p2")
	cc := d.claude
	cache := filepath.Join(cc.dir, "plugins", "cache", "mk")

	cc.plugin("jasonbuffin@mk", "jasonbuffin", filepath.Join(cache, "jasonbuffin", "1.0.0"),
		map[string]any{"skills": []string{"./extra"}}, "engineering/commodore", "alpha")
	writeSkill(t, filepath.Join(cache, "jasonbuffin", "1.0.0", "extra"), "xs")
	cc.enable("jasonbuffin@mk", true)
	cc.plugin("off@mk", "off", filepath.Join(cache, "off", "1.0.0"), nil, "offskill")
	cc.enable("off@mk", false)
	cc.plugin("dflt@mk", "dflt", filepath.Join(cache, "dflt", "1.0.0"), nil, "ds") // not in enabledPlugins
	cc.plugin("loc@mk", "loc", filepath.Join(d.root, "local-plugin"), nil, "ls")   // installed outside the cache
	cc.enable("loc@mk", true)
	cc.plugin("pd@mk", "pd", filepath.Join(cache, "pd", "1.0.0"), nil, "pds")
	cc.enable("pd@mk", true)
	writeJSON(t, filepath.Join(p1, ".claude", "settings.json"), map[string]any{
		"enabledPlugins": map[string]bool{"pd@mk": false}, "env": map[string]string{"P": decoySecret}})
	// A skills-directory plugin.
	dp := filepath.Join(cc.dir, "skills", "dirplug")
	writeJSON(t, filepath.Join(dp, ".claude-plugin", "plugin.json"), map[string]any{"name": "dp"})
	writeSkill(t, filepath.Join(dp, "skills"), "dps")
	d.start()

	base := []string{"jasonbuffin:commodore", "jasonbuffin:alpha", "jasonbuffin:xs", "dflt:ds", "loc:ls", "dp:dps"}
	sameSet(t, "skills for p1 (pd disabled by the project)", d.skills(t, "work", "claude", p1).names(), base)
	sameSet(t, "skills for p2", d.skills(t, "work", "claude", p2).names(), append(base, "pd:pds"))
	if got := d.skills(t, "work", "codex", p1).names(); len(got) != 0 {
		t.Errorf("Codex must not see Claude plugins: %v", got)
	}

	// Nothing but the plugin key may be read out of settings.json.
	for _, h := range []string{"claude", "codex", "copilot"} {
		code, body := d.getSkills("work", h, p1)
		if code != http.StatusOK {
			t.Fatalf("%s skills = %d: %s", h, code, body)
		}
		for _, secret := range []string{decoySecret, decoyHelper, "API_SECRET", "apiKeyHelper"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s skills response carries %q from settings.json: %s", h, secret, body)
			}
		}
	}
	_, body := d.postDirs("work", "claude", "", "")
	if strings.Contains(body, decoySecret) || strings.Contains(body, decoyHelper) {
		t.Errorf("dirs response carries a settings secret: %s", body)
	}
	if out := d.in.stderrBuf.String(); strings.Contains(out, decoySecret) || strings.Contains(out, decoyHelper) {
		t.Errorf("Admiral's stderr carries a settings secret: %s", out)
	}
}

// S5: Commodore is found as a skill named commodore or <plugin>:commodore; a
// plain one wins, then plugins by name; Codex finds only a plain one.
func TestCommodoreLookup(t *testing.T) {
	cases := []struct {
		name, harness string
		setup         func(t *testing.T, d *dataWorld)
		found         bool
		skill         string // the invocation name
		prompt        string
	}{
		{"absent", "claude", func(t *testing.T, d *dataWorld) {}, false, "", ""},
		{"claude plugin", "claude", func(t *testing.T, d *dataWorld) {
			d.claude.plugin("jasonbuffin@mk", "jasonbuffin", filepath.Join(d.claude.dir, "plugins", "cache", "mk", "jasonbuffin", "1"), nil, "engineering/commodore")
			d.claude.enable("jasonbuffin@mk", true)
		}, true, "jasonbuffin:commodore", "/jasonbuffin:commodore do it"},
		{"codex plain", "codex", func(t *testing.T, d *dataWorld) { writeSkill(t, filepath.Join(d.codex, "skills"), "commodore") },
			true, "commodore", "$commodore do it"},
		{"codex with only the claude plugin", "codex", func(t *testing.T, d *dataWorld) {
			d.claude.plugin("jasonbuffin@mk", "jasonbuffin", filepath.Join(d.claude.dir, "plugins", "cache", "mk", "jasonbuffin", "1"), nil, "commodore")
			d.claude.enable("jasonbuffin@mk", true)
		}, false, "", ""},
		{"plain beats plugin", "claude", func(t *testing.T, d *dataWorld) {
			writeSkill(t, filepath.Join(d.claude.dir, "skills"), "commodore")
			d.claude.plugin("jasonbuffin@mk", "jasonbuffin", filepath.Join(d.claude.dir, "plugins", "cache", "mk", "jasonbuffin", "1"), nil, "commodore")
			d.claude.enable("jasonbuffin@mk", true)
		}, true, "commodore", "/commodore do it"},
		{"a user plugin beats the project's plain one", "claude", func(t *testing.T, d *dataWorld) {
			writeSkill(t, filepath.Join(d.work, "proj", ".claude", "skills"), "commodore")
			d.claude.plugin("jasonbuffin@mk", "jasonbuffin", filepath.Join(d.claude.dir, "plugins", "cache", "mk", "jasonbuffin", "1"), nil, "commodore")
			d.claude.enable("jasonbuffin@mk", true)
		}, true, "jasonbuffin:commodore", "/jasonbuffin:commodore do it"},
		{"the project's plain one when the user has none", "claude", func(t *testing.T, d *dataWorld) {
			writeSkill(t, filepath.Join(d.work, "proj", ".claude", "skills"), "commodore")
		}, true, "commodore", "/commodore do it"},
		{"a plugin name with a newline is never used", "claude", func(t *testing.T, d *dataWorld) {
			bad := filepath.Join(d.work, "proj", ".claude", "skills", "x")
			writeJSON(t, filepath.Join(bad, ".claude-plugin", "plugin.json"), map[string]any{"name": "evil\nname"})
			writeSkill(t, filepath.Join(bad, "skills"), "commodore")
		}, false, "", ""},
		{"a project that disables the user's Commodore plugin gets no Commodore", "claude", func(t *testing.T, d *dataWorld) {
			writeSkill(t, filepath.Join(d.work, "proj", ".claude", "skills"), "commodore")
			d.claude.plugin("jasonbuffin@mk", "jasonbuffin", filepath.Join(d.claude.dir, "plugins", "cache", "mk", "jasonbuffin", "1"), nil, "commodore")
			d.claude.enable("jasonbuffin@mk", true)
			writeJSON(t, filepath.Join(d.work, "proj", ".claude", "settings.json"),
				map[string]any{"enabledPlugins": map[string]bool{"jasonbuffin@mk": false}})
		}, false, "", ""},
		{"plugins by plugin name", "claude", func(t *testing.T, d *dataWorld) {
			for _, n := range []string{"zzz", "aaa"} {
				d.claude.plugin(n+"@mk", n, filepath.Join(d.claude.dir, "plugins", "cache", "mk", n, "1"), nil, "commodore")
				d.claude.enable(n+"@mk", true)
			}
		}, true, "aaa:commodore", "/aaa:commodore do it"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newDataWorld(t)
			proj := d.repo("proj")
			c.setup(t, d)
			d.start()
			for _, n := range d.skills(t, "work", c.harness, proj).names() {
				if strings.ContainsAny(n, "\n\r") || strings.Contains(n, "evil") {
					t.Errorf("skill %q is listed, but its plugin name is not safe", n)
				}
			}
			got := d.skills(t, "work", c.harness, proj).Commodore
			if got.Found != c.found || got.Name != c.skill {
				t.Errorf("commodore = %+v, want found %v name %q", got, c.found, c.skill)
			}
			before := d.sessions()
			if !c.found {
				code, body := d.post(d.launchReq("work", c.harness, proj, commodore, "do it"))
				if msg := requireRefusal(t, code, body); !strings.Contains(msg, "Commodore not found") {
					t.Errorf("error %q, want \"Commodore not found\"", msg)
				}
				if after := d.sessions(); !reflect.DeepEqual(before, after) {
					t.Errorf("a refused launch created a session: %v", after)
				}
				return
			}
			ship := d.launch(d.launchReq("work", c.harness, proj, commodore, "do it"))
			if got := promptOf(t, d.world, d.record(ship.Session)); got != c.prompt {
				t.Errorf("prompt = %q, want %q", got, c.prompt)
			}
		})
	}
}

// S6: up to five favorites, written through the config, carried by the
// response, GET /api/config and the next websocket `config` message.
func TestFavorites(t *testing.T) {
	d := newDataWorld(t)
	proj := d.repo("proj")
	writeSkill(t, filepath.Join(d.claude.dir, "skills"), "f1")
	writeSkill(t, filepath.Join(d.claude.dir, "skills"), "other")
	d.start()
	url := d.in.LocalURL + "/api/favorites"
	c := dialWS(t, d.in.instance)
	waitWS(t, c, 3*time.Second, "the first config", anyConfig)

	five := []string{"f1", "f2", "f3", "f4", "f5"}
	code, body := send(t, http.MethodPut, url, map[string]any{"skills": five})
	if code != http.StatusOK {
		t.Fatalf("PUT five favorites = %d, want 200: %s", code, body)
	}
	var resp struct {
		Favorites []string `json:"favorite_skills"`
		Skills    []string `json:"skills"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || !(reflect.DeepEqual(resp.Favorites, five) || reflect.DeepEqual(resp.Skills, five)) {
		t.Errorf("the response must carry the new favorites %v as favorite_skills: %s", five, body)
	}
	if api, _ := fetchConfig(t, d.in.instance); !reflect.DeepEqual(api.FavoriteSkills, five) {
		t.Errorf("GET /api/config favorite_skills = %v right after the PUT, want %v", api.FavoriteSkills, five)
	}
	waitWS(t, c, 3*time.Second, "the five favorites", func(b cfgBody) bool { return reflect.DeepEqual(b.FavoriteSkills, five) })
	for _, s := range d.skills(t, "work", "claude", proj).Skills {
		if want := s.Name == "f1"; s.Favorite != want {
			t.Errorf("skill %s favorite = %v, want %v", s.Name, s.Favorite, want)
		}
	}

	six := append(append([]string{}, five...), "f6")
	code, body = send(t, http.MethodPut, url, map[string]any{"skills": six})
	if msg := requireRefusal(t, code, body); !strings.Contains(msg, "Unstar one first") {
		t.Errorf("error %q, want \"Unstar one first\"", msg)
	}
	if api, _ := fetchConfig(t, d.in.instance); !reflect.DeepEqual(api.FavoriteSkills, five) {
		t.Errorf("a refused PUT changed favorite_skills to %v", api.FavoriteSkills)
	}
	code, body = send(t, http.MethodPut, url, map[string]any{"skills": []string{"f1", "f1"}})
	requireRefusal(t, code, body) // not silently deduplicated
	if code, body = send(t, http.MethodPut, url, map[string]any{"skills": five[:4]}); code != http.StatusOK {
		t.Errorf("unstarring = %d: %s", code, body)
	}
}

// A symlinked config.json whose target is gone is not replaced by a plain file.
func TestFavoritesKeepASymlinkedConfig(t *testing.T) {
	d := newDataWorld(t)
	d.start()
	d.in.Stop()
	real := filepath.Join(d.root, "real-config.json")
	if err := os.Rename(configPath(d.home), real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, configPath(d.home)); err != nil {
		t.Fatal(err)
	}
	d.restart()
	if code, body := send(t, http.MethodPut, d.in.LocalURL+"/api/favorites", map[string]any{"skills": []string{"f0"}}); code != http.StatusOK {
		t.Fatalf("control PUT through the symlink = %d, want 200: %s", code, body)
	}
	if err := os.Remove(real); err != nil {
		t.Fatal(err)
	}
	if code, body := send(t, http.MethodPut, d.in.LocalURL+"/api/favorites", map[string]any{"skills": []string{"f1"}}); code < 400 {
		t.Errorf("PUT with the symlink's target missing = %d, want an error: %s", code, body)
	}
	if fi, err := os.Lstat(configPath(d.home)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("config.json must still be the symlink (err %v)", err)
	}
}

// S7: a launch re-checks that the directory is in the fleet's current list and
// still exists, before any session; the skills endpoint applies the same check.
func TestLaunchRecheck(t *testing.T) {
	d := newDataWorld(t)
	a := d.repo("a")
	gone := d.repo("gone")
	plain := filepath.Join(d.work, "plainfolder")
	if err := os.MkdirAll(filepath.Join(a, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(d.root, "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	d.git(outside, "init", "-q")
	d.start()
	d.postDirs("work", "claude", "", "") // gone is listed here, then deleted below
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	refused := map[string]string{
		"a checkout outside the search roots": outside,
		"a plain folder in a search root":     plain,
		"a folder inside a checkout":          filepath.Join(a, "sub"),
		"a path that cleans to outside":       filepath.Join(a, "..", "..", "elsewhere"),
		"a listed checkout deleted since":     gone,
	}
	for name, dir := range refused {
		t.Run(name, func(t *testing.T) {
			before := d.sessions()
			code, body := d.post(d.launchReq("work", "claude", dir, noSkill, "go"))
			requireRefusal(t, code, body)
			if after := d.sessions(); !reflect.DeepEqual(before, after) {
				t.Errorf("a refused launch changed the sessions: before %v, after %v", before, after)
			}
			if n := len(d.in.ships(t)); n != 0 {
				t.Errorf("ships = %d after a refusal, want 0", n)
			}
			if code, body := d.getSkills("work", "claude", dir); code < 400 || code > 499 {
				t.Errorf("GET skills for %s = %d, want a 4xx: %s", dir, code, body)
			}
		})
	}
	// Both kinds of listed entry launch.
	d.launch(d.launchReq("work", "claude", a, noSkill, "go"))
	d.launch(d.launchReq("work", "claude", d.work, noSkill, "go"))
	if code, body := d.getSkills("work", "claude", a); code != http.StatusOK {
		t.Errorf("GET skills for a listed checkout = %d: %s", code, body)
	}
}
