package crewapi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

var scout = upstream{owner: "hubot", repo: "crew", name: "scout", text: memberFile("scout", "")}

func readSidecarMap(t *testing.T, path string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(slurp(path)), &out); err != nil {
		t.Fatalf("%s is not JSON: %v", path, err)
	}
	return out
}

// --- preview ---

func TestPreviewReadsTheFileAtThePinnedCommitAndNamesWhatItFound(t *testing.T) {
	p := newImp(t, impOpts{})
	u := scout
	u.branch = "trunk" // read from GitHub, never assumed
	u.tags = []string{"v1", "v1-rc"}
	p.gh.seed(u)

	status, pv := p.importPreview("hubot/crew/scout")
	wantStatus(t, "preview", status, 200, pv)
	for key, want := range map[string]string{"bytes": u.text, "owner": "hubot", "repo": "crew", "path": "crew/scout.md", "commit": commitSHA,
		"tag": "v1", "blob": u.blob(), "sha256": hexSum(u.text)} {
		if got := str(pv, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if card := cardOf(pv); card == nil || card["name"] != "scout" || card["role"] != "triager" || card["pick-me-when"] != "Pick scout." {
		t.Errorf("card = %v", pv["card"])
	}
	if pv["clash"] != nil {
		t.Errorf("clash = %v, want null", pv["clash"])
	}
	if skills, ok := pv["missing_skills"].([]any); !ok || len(skills) != 0 {
		t.Errorf("missing_skills = %v, want an empty list", pv["missing_skills"])
	}
	if scan, _ := pv["scan"].(map[string]any); scan["state"] != "clean" {
		t.Errorf("scan = %v, want clean", pv["scan"])
	}
	var got []string
	for _, c := range p.gh.reads() {
		got = append(got, c.Endpoint)
	}
	want := []string{"repos/hubot/crew", "repos/hubot/crew/commits/trunk", "repos/hubot/crew/contents/crew/scout.md?ref=" + commitSHA, "repos/hubot/crew/tags?per_page=100"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gh calls %v\nwant %v", got, want)
	}
	wantNoWrites(t, p)
	p.wantNothingImported("preview")
}

func TestPreviewWithARefResolvesThatRef(t *testing.T) {
	for _, c := range []struct{ ref, endpoint string }{
		{"v1", "repos/hubot/crew/commits/v1"},
		{"feature/x", "repos/hubot/crew/commits/feature%2Fx"},
		{commitSHA, "repos/hubot/crew/commits/" + commitSHA},
	} {
		t.Run(c.ref, func(t *testing.T) {
			p := newImp(t, impOpts{})
			u := scout
			u.ref = c.ref
			p.gh.seed(u)
			status, pv := p.importPreview("hubot/crew/scout@" + c.ref)
			wantStatus(t, "preview", status, 200, pv)
			if len(p.gh.find("GET", c.endpoint)) != 1 {
				t.Errorf("no single call to %s in %+v", c.endpoint, p.gh.reads())
			}
		})
	}
}

func TestPreviewOfABlobURLReadsTheMemberAtThatRef(t *testing.T) {
	p := newImp(t, impOpts{})
	u := scout
	u.ref = "feature/x"
	p.gh.seed(u)
	status, pv := p.importPreview("https://github.com/hubot/crew/blob/feature/x/crew/scout.md")
	wantStatus(t, "preview", status, 200, pv)
	if str(pv, "path") != "crew/scout.md" {
		t.Errorf("path = %q", str(pv, "path"))
	}
}

func TestPreviewOfARepoURLAnswersASourceAndCallsNoGh(t *testing.T) {
	p := newImp(t, impOpts{})
	for _, in := range []string{"https://github.com/hubot/crew", "https://github.com/hubot/crew/tree/main/crew"} {
		status, pv := p.importPreview(in)
		wantStatus(t, in, status, 200, pv)
		if str(pv, "source") != "hubot/crew" {
			t.Errorf("%s: source = %v", in, pv)
		}
	}
	if n := len(p.gh.calls()); n != 0 {
		t.Errorf("%d gh calls for a source form", n)
	}
}

func TestPreviewOfABadRefIs400NamingTheAcceptedForms(t *testing.T) {
	p := newImp(t, impOpts{})
	for _, in := range []string{"a/b", "a/b/c@..", "https://gitlab.com/a/b", ""} {
		status, out := p.importPreview(in)
		wantStatus(t, in, status, 400, out)
		wantText(t, in, out, "octocat/admiral-crew/reviewer@v1")
	}
	if n := len(p.gh.calls()); n != 0 {
		t.Errorf("%d gh calls for a bad ref", n)
	}
}

func TestPreviewNotFoundNamesWhichThingIsMissing(t *testing.T) {
	cases := map[string]struct {
		ref   string
		can   func(g *ghFake)
		names []string
	}{
		"commit 422": {"hubot/crew/scout@nope", func(g *ghFake) {
			g.ok("GET", "repos/hubot/crew", repoAs("hubot/crew", "main"))
			g.answer("GET", "repos/hubot/crew/commits/nope", 422, nil, "gh: No commit found for SHA: nope (HTTP 422)")
		}, []string{"nope"}},
		"commit 404": {"hubot/crew/scout@nope", func(g *ghFake) {
			g.ok("GET", "repos/hubot/crew", repoAs("hubot/crew", "main"))
			g.answer("GET", "repos/hubot/crew/commits/nope", 404, nil, "")
		}, []string{"nope"}},
		"file": {"hubot/crew/scout", func(g *ghFake) {
			g.ok("GET", "repos/hubot/crew", repoAs("hubot/crew", "main"))
			g.ok("GET", "repos/hubot/crew/commits/main", map[string]any{"sha": commitSHA})
			g.answer("GET", "repos/hubot/crew/contents/crew/scout.md?ref="+commitSHA, 404, nil, "")
		}, []string{"crew/scout.md", "hubot/crew"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := newImp(t, impOpts{})
			c.can(p.gh)
			status, out := p.importPreview(c.ref)
			wantStatus(t, name, status, 404, out)
			wantText(t, name, out, c.names...)
			wantNoWrites(t, p)
		})
	}
}

func TestPreviewOfARepoThatAnswers404IsNoAccessNamingTheRepo(t *testing.T) {
	p := newImp(t, impOpts{})
	p.gh.answer("GET", "repos/hubot/crew", 404, nil, "")
	status, out := p.importPreview("hubot/crew/scout")
	wantNoAccess(t, "repo 404", status, out, "hubot/crew")
	wantNoWrites(t, p)
}

func TestPreviewOfAFileOver64KiBIs422NamingTheRule(t *testing.T) {
	p := newImp(t, impOpts{})
	u := scout
	u.text = memberFile("scout", "") + strings.Repeat("x", 64<<10)
	p.gh.seed(u)
	status, out := p.importPreview("hubot/crew/scout")
	wantStatus(t, "too large", status, 422, out)
	wantText(t, "too large", out, "64 KiB")
}

func TestPreviewRateLimitIs429(t *testing.T) {
	p := newImp(t, impOpts{})
	p.gh.answer("GET", "repos/hubot/crew", 403, nil, "gh: API rate limit exceeded for user ID 1. Try again in 42 minutes. (HTTP 403)")
	status, out := p.importPreview("hubot/crew/scout")
	wantStatus(t, "rate limit", status, 429, out)
	wantText(t, "rate limit", out, "hubot/crew", "Try again in 42 minutes")
}

func TestARefusedFileIs422NamingTheRuleAndLineAndWritesNothing(t *testing.T) {
	cases := map[string]struct {
		text  string
		parts []string
	}{
		"invalid UTF-8":       {memberFile("scout", "") + "bad \xff byte\n", []string{"UTF-8", "line 8"}},
		"right-to-left mark":  {memberFile("scout", "") + "a‮b\n", []string{"U+202E", "line 8"}},
		"variation selectors": {memberFile("scout", "") + "a\U000E0100\U000E0101\U000E0102b\n", []string{"U+E0100", "line 8"}},
		"hangul filler":       {memberFile("scout", "") + "aㅤb\n", []string{"U+3164", "line 8"}},
		"CheckFile":           {"---\nname: scout\npick-me-when: Pick.\n---\n\nbody\n", []string{"role"}},
		"ad-hoc true":         {memberFile("scout", "ad-hoc: true\n"), []string{"ad-hoc", "line 5"}},
		"ad-hoc false":        {memberFile("scout", "ad-hoc: false\n"), []string{"ad-hoc", "line 5"}},
		"ad-hoc-run":          {memberFile("scout", "ad-hoc-run: r1\n"), []string{"ad-hoc-run", "line 5"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := newImp(t, impOpts{})
			u := scout
			u.text = c.text
			p.gh.seed(u)
			status, out := p.importPreview("hubot/crew/scout")
			wantStatus(t, "preview", status, 422, out)
			wantText(t, "preview", out, c.parts...)
			status, out = p.post("/imports", map[string]any{"repo": "hubot/crew", "path": "crew/scout.md", "commit": commitSHA, "sha256": hexSum(c.text)})
			wantStatus(t, "import", status, 422, out)
			wantText(t, "import", out, c.parts...)
			wantNoWrites(t, p)
			p.wantNothingImported(name)
		})
	}
}

func TestPreviewListsEverySkillTheDockCannotSeeAndNoMore(t *testing.T) {
	p := newImp(t, impOpts{installed: []string{"tdd"}})
	u := scout
	u.text = memberFile("scout", "skills:\n  - tdd\n  - nope\n")
	p.gh.seed(u)
	status, pv := p.importPreview("hubot/crew/scout")
	wantStatus(t, "preview", status, 200, pv)
	if got := pv["missing_skills"]; !reflect.DeepEqual(got, []any{"nope"}) {
		t.Errorf("missing_skills = %v, want [nope]", got)
	}
	status, out := p.importFrom(pv, nil)
	wantStatus(t, "import", status, 200, out)
	if got := out["missing_skills"]; !reflect.DeepEqual(got, []any{"nope"}) {
		t.Errorf("import missing_skills = %v, want [nope]", got)
	}
}

func TestAHitInAnImportedFileIsAWarningThatNeverEchoesTheSecretAndImportStillWorks(t *testing.T) {
	p := newImp(t, impOpts{})
	u := scout
	u.text = memberFile("scout", "") + "Token " + decoy + " here.\n"
	p.gh.seed(u)
	status, pv := p.importPreview("hubot/crew/scout")
	wantStatus(t, "preview", status, 200, pv)
	scan, _ := pv["scan"].(map[string]any)
	findings, _ := scan["findings"].([]any)
	if scan["state"] != "hit" || len(findings) != 1 {
		t.Fatalf("scan = %v, want one finding", scan)
	}
	if b, _ := json.Marshal(scan); strings.Contains(string(b), decoy) {
		t.Errorf("the scan warning echoes the secret: %s", b)
	}
	status, out := p.importFrom(pv, nil)
	wantStatus(t, "import", status, 200, out)
	if got := readFile(t, filepath.Join(p.crewDir(), "scout.md")); got != u.text {
		t.Errorf("imported %q", got)
	}
}

func TestAFailedScanOfAnImportedFileIsAWarningWithItsErrorAndImportStillWorks(t *testing.T) {
	p := newImp(t, impOpts{})
	p.leakFail("error")
	p.gh.seed(scout)
	status, pv := p.importPreview("hubot/crew/scout")
	wantStatus(t, "preview", status, 200, pv)
	scan, _ := pv["scan"].(map[string]any)
	if scan["state"] != "failed" || !strings.Contains(str(scan, "error"), "fake gitleaks: boom") {
		t.Fatalf("scan = %v, want a failure naming its error", scan)
	}
	status, out := p.importFrom(pv, nil)
	wantStatus(t, "import", status, 200, out)
	if !exists(filepath.Join(p.crewDir(), "scout.md")) {
		t.Error("nothing imported")
	}
}

func TestWithNoGitleaksPreviewAndImportStillWork(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noGitleak: true}})
	p.gh.seed(scout)
	p.importOK("hubot/crew/scout", nil)
	if !exists(filepath.Join(p.crewDir(), "scout.md")) {
		t.Error("nothing imported")
	}
}

func TestImportWorksWithSharingOff(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{off: true}})
	p.gh.seed(scout)
	p.importOK("hubot/crew/scout", nil)
}

// --- clash ---

func TestANameAlreadyTakenIs409WithTheOwnerSuggestionAndImportingUnderItRenames(t *testing.T) {
	taken := map[string]func(p *pubFixture){
		"fleet member": func(p *pubFixture) { p.member("scout", "Mine.") },
		"built-in":     func(p *pubFixture) { builtInMember(p.t, p.builtIns, "scout", "triager", "Built in.") },
		"orphan sidecar": func(p *pubFixture) {
			writeFile(p.t, filepath.Join(p.crewDir(), "scout.shared.json"), sidecarFor("whatever"))
		},
	}
	for name, setup := range taken {
		t.Run(name, func(t *testing.T) {
			p := newImp(t, impOpts{})
			setup(p)
			before := slurp(filepath.Join(p.crewDir(), "scout.md"))
			p.gh.seed(scout)

			status, pv := p.importPreview("hubot/crew/scout")
			wantStatus(t, "preview", status, 200, pv)
			if clash, _ := pv["clash"].(map[string]any); clash["name"] != "scout" || clash["suggested"] != "scout-hubot" {
				t.Errorf("clash = %v, want scout / scout-hubot", pv["clash"])
			}
			status, out := p.importFrom(pv, nil)
			wantStatus(t, "import", status, 409, out)
			if out["suggested"] != "scout-hubot" {
				t.Errorf("409 suggested = %v", out["suggested"])
			}
			if slurp(filepath.Join(p.crewDir(), "scout.md")) != before {
				t.Error("the existing member was changed")
			}

			status, out = p.importFrom(pv, map[string]any{"name": "scout-hubot"})
			wantStatus(t, "import renamed", status, 200, out)
			written := readFile(t, filepath.Join(p.crewDir(), "scout-hubot.md"))
			if want := strings.Replace(scout.text, "name: scout\n", "name: scout-hubot\n", 1); written != want {
				t.Errorf("renamed file = %q, want %q", written, want)
			}
			side := readSidecarMap(t, filepath.Join(p.crewDir(), "scout-hubot.shared.json"))
			if side["upstream_name"] != "scout" || side["content_sha256"] != hexSum(written) {
				t.Errorf("sidecar = %v; want upstream_name scout and the sha256 of the renamed bytes", side)
			}
		})
	}
}

func TestTheSuggestedNameFollowsTheOwnerRulesAndIsNullWhenTakenOrTooLong(t *testing.T) {
	long := strings.Repeat("a", 60)
	cases := []struct {
		name, owner string
		also        string // another member already taking the suggestion
		want        any
	}{
		{"scout", "Hub_Bot", "", "scout-hub-bot"},
		{"scout", "hubot", "scout-hubot", nil},
		{long, "hubot", "", long + "-hub"},
		{"scout", "a.b", "", "scout-a-b"},
	}
	for _, c := range cases {
		t.Run(c.name+"/"+c.owner, func(t *testing.T) {
			p := newImp(t, impOpts{})
			p.member(c.name, "Mine.")
			if c.also != "" {
				p.member(c.also, "Mine too.")
			}
			u := upstream{owner: c.owner, repo: "crew", name: c.name, text: memberFile(c.name, "")}
			p.gh.seed(u)
			status, pv := p.importPreview(c.owner + "/crew/" + c.name)
			wantStatus(t, "preview", status, 200, pv)
			clash, _ := pv["clash"].(map[string]any)
			if clash == nil || clash["suggested"] != c.want {
				t.Errorf("clash = %v, want suggested %v", pv["clash"], c.want)
			}
		})
	}
}

// --- import ---

func TestImportWritesTheMemberByteForByteAndTheSidecarEveryField(t *testing.T) {
	p := newImp(t, impOpts{})
	u := scout
	u.tags = []string{"v1"}
	p.gh.seed(u)
	before := time.Now().UTC().Add(-2 * time.Second)
	out := p.importOK("hubot/crew/scout@main", nil)
	if out["name"] != "scout" {
		t.Errorf("import answered %v", out)
	}
	if got := readFile(t, filepath.Join(p.crewDir(), "scout.md")); got != u.text {
		t.Errorf("member = %q, want the upstream bytes", got)
	}
	sidecarFile := filepath.Join(p.crewDir(), "scout.shared.json")
	if info, err := os.Stat(sidecarFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("sidecar mode = %v, %v; want 0600", info, err)
	}
	side := readSidecarMap(t, sidecarFile)
	want := map[string]any{"version": float64(1), "host": "github.com", "repo": "hubot/crew", "path": "crew/scout.md", "upstream_name": "scout",
		"commit": commitSHA, "tag": "v1", "upstream_blob": u.blob(), "content_sha256": hexSum(u.text)}
	for k, v := range want {
		if side[k] != v {
			t.Errorf("sidecar %s = %v, want %v", k, side[k], v)
		}
	}
	stamp := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	imported, _ := side["imported_at"].(string)
	updated, _ := side["updated_at"].(string)
	if !stamp.MatchString(imported) || imported != updated {
		t.Errorf("imported_at %q, updated_at %q; want equal RFC 3339 UTC seconds", imported, updated)
	}
	if at, err := time.Parse(time.RFC3339, imported); err != nil || at.Before(before) || at.After(time.Now().Add(time.Second)) {
		t.Errorf("imported_at %q is not now", imported)
	}
	wantNoWrites(t, p)
}

func TestAnImportedMemberShowsSharedAndSharedURLAndItsSidecarIsNotAMember(t *testing.T) {
	p := newImp(t, impOpts{})
	u := scout
	u.tags = []string{"v1"}
	p.gh.seed(u)
	p.importOK("hubot/crew/scout", nil)

	m := p.rawMember("scout")
	shared, _ := m["shared"].(map[string]any)
	if shared == nil || shared["owner"] != "hubot" || shared["repo"] != "crew" || shared["commit"] != commitSHA || shared["tag"] != "v1" || shared["detached"] != false {
		t.Errorf("shared = %v", m["shared"])
	}
	if want := "https://github.com/hubot/crew/blob/" + commitSHA + "/crew/scout.md"; m["shared_url"] != want {
		t.Errorf("shared_url = %v, want %s", m["shared_url"], want)
	}
	for name := range p.listed() {
		if strings.Contains(name, "shared") {
			t.Errorf("%s is listed as a member", name)
		}
	}
}

func TestImportRefusesWhenTheFileChangedSinceThePreviewAndWritesNothing(t *testing.T) {
	p := newImp(t, impOpts{})
	p.gh.seed(scout)
	status, pv := p.importPreview("hubot/crew/scout")
	wantStatus(t, "preview", status, 200, pv)
	status, out := p.importFrom(pv, map[string]any{"sha256": hexSum("something else\n")})
	wantStatus(t, "import", status, 409, out)
	wantText(t, "import", out, "changed since the preview")
	if out["kind"] != "changed" {
		t.Errorf("kind = %v, want changed", out["kind"])
	}
	wantNoWrites(t, p)
	p.wantNothingImported("changed upstream")
}

func TestImportValidatesEveryFieldAndNamesTheBadOne(t *testing.T) {
	good := map[string]any{"repo": "hubot/crew", "path": "crew/scout.md", "commit": commitSHA, "sha256": hexSum(scout.text)}
	bad := map[string]any{"repo": "hubot", "path": "crew/../x.md", "commit": "abc", "sha256": "xyz", "name": "Bad Name"}
	for field, value := range bad {
		t.Run(field, func(t *testing.T) {
			p := newImp(t, impOpts{})
			body := map[string]any{}
			for k, v := range good {
				body[k] = v
			}
			body[field] = value
			status, out := p.post("/imports", body)
			wantStatus(t, field, status, 400, out)
			wantText(t, field, out, field)
			if n := len(p.gh.calls()); n != 0 {
				t.Errorf("%d gh calls for a bad %s", n, field)
			}
		})
	}
}

// --- not connected ---

func TestWithNoGitHubSourceEveryImportRouteIs409WithTheConnectTextAndCallsNoGh(t *testing.T) {
	for name, o := range map[string]pubOpts{"no source": {noGitHub: true}, "other host": {host: "ghe.example.com"}} {
		t.Run(name, func(t *testing.T) {
			p := newImp(t, impOpts{pubOpts: o})
			steps := map[string]func() (int, map[string]any){
				"preview member": func() (int, map[string]any) { return p.importPreview("hubot/crew/scout") },
				"preview source": func() (int, map[string]any) { return p.importPreview("https://github.com/hubot/crew") },
				"import": func() (int, map[string]any) {
					return p.post("/imports", map[string]any{"repo": "hubot/crew", "path": "crew/scout.md", "commit": commitSHA, "sha256": hexSum(scout.text)})
				},
				"browse": func() (int, map[string]any) { return p.getJSON("/sources/hubot/crew") },
				"add source": func() (int, map[string]any) {
					return p.put("/sources", map[string]any{"sources": []string{"hubot/crew"}})
				},
			}
			for step, run := range steps {
				status, out := run()
				wantStatus(t, step, status, 409, out)
				wantText(t, step, out, connectText)
			}
			if n := len(p.gh.calls()); n != 0 {
				t.Errorf("%d gh calls without a github.com client", n)
			}
			p.wantNothingImported(name)
		})
	}
}

// --- sources ---

func (g *ghFake) crewListing(full string, names ...string) {
	g.t.Helper()
	g.ok("GET", "repos/"+full, repoAs(full, "main"))
	var list []map[string]any
	for _, n := range names {
		list = append(list, map[string]any{"name": n, "path": "crew/" + n, "type": "file", "sha": blobOf(n)})
	}
	g.ok("GET", "repos/"+full+"/contents/crew", list)
}

func TestAddingSourcesChecksEachNewEntryOnceAndSavesTheList(t *testing.T) {
	p := newImp(t, impOpts{})
	p.gh.crewListing("a/b", "x.md")
	p.gh.crewListing("c/d", "y.md")
	status, out := p.put("/sources", map[string]any{"sources": []string{"a/b", "https://github.com/c/d"}})
	wantStatus(t, "PUT sources", status, 200, out)
	if got := p.savedSources(); !reflect.DeepEqual(got, []string{"a/b", "c/d"}) {
		t.Errorf("saved %v", got)
	}
	var got []string
	for _, c := range p.gh.calls() {
		got = append(got, c.Method+" "+c.Endpoint)
	}
	want := []string{"GET repos/a/b", "GET repos/a/b/contents/crew", "GET repos/c/d", "GET repos/c/d/contents/crew"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gh calls %v\nwant %v", got, want)
	}
}

func TestRemovingOrReorderingSourcesMakesNoGhCall(t *testing.T) {
	for name, list := range map[string][]string{"reorder": {"c/d", "a/b"}, "remove": {"c/d"}, "clear": {}} {
		t.Run(name, func(t *testing.T) {
			p := newImp(t, impOpts{sources: []string{"a/b", "c/d"}})
			status, out := p.put("/sources", map[string]any{"sources": list})
			wantStatus(t, name, status, 200, out)
			if got := p.savedSources(); !reflect.DeepEqual(got, list) {
				t.Errorf("saved %v, want %v", got, list)
			}
			if n := len(p.gh.calls()); n != 0 {
				t.Errorf("%d gh calls for %s", n, name)
			}
		})
	}
}

func TestSourcesThatDifferOnlyInCaseAreDroppedKeepingTheFirstSpelling(t *testing.T) {
	p := newImp(t, impOpts{})
	p.gh.crewListing("Octocat/Admiral-Crew", "x.md")
	status, out := p.put("/sources", map[string]any{"sources": []string{"Octocat/Admiral-Crew", "octocat/admiral-crew"}})
	wantStatus(t, "PUT sources", status, 200, out)
	if got := p.savedSources(); !reflect.DeepEqual(got, []string{"Octocat/Admiral-Crew"}) {
		t.Errorf("saved %v", got)
	}
	if n := len(p.gh.find("GET", "repos/Octocat/Admiral-Crew")); n != 1 {
		t.Errorf("%d repo checks, want one for the one entry kept", n)
	}
}

func TestASourceWithNoRepoOrNoCrewFolderIs404NamingWhichAndSavesNothing(t *testing.T) {
	cases := map[string]struct {
		can   func(g *ghFake)
		parts []string
	}{
		"no crew folder": {func(g *ghFake) {
			g.ok("GET", "repos/a/b", repoAs("a/b", "main"))
			g.answer("GET", "repos/a/b/contents/crew", 404, nil, "")
		}, []string{"crew/"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := newImp(t, impOpts{sources: []string{"keep/me"}})
			c.can(p.gh)
			status, out := p.put("/sources", map[string]any{"sources": []string{"keep/me", "a/b"}})
			wantStatus(t, name, status, 404, out)
			wantText(t, name, out, c.parts...)
			if got := p.savedSources(); !reflect.DeepEqual(got, []string{"keep/me"}) {
				t.Errorf("saved %v, want the old list", got)
			}
		})
	}
}

func TestASourceWhoseRepoAnswers404IsNoAccessAndSavesNothing(t *testing.T) {
	p := newImp(t, impOpts{sources: []string{"keep/me"}})
	p.gh.answer("GET", "repos/a/b", 404, nil, "")
	status, out := p.put("/sources", map[string]any{"sources": []string{"keep/me", "a/b"}})
	wantNoAccess(t, "no repo", status, out, "a/b")
	if got := p.savedSources(); !reflect.DeepEqual(got, []string{"keep/me"}) {
		t.Errorf("saved %v, want the old list", got)
	}
}

func TestARemovalWorksOnAFleetWithNoGitHubSourceAndSharingOffAndAnEntryThatIsNotARepoIs400(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noGitHub: true, off: true}, sources: []string{"a/b", "c/d"}})
	status, out := p.put("/sources", map[string]any{"sources": []string{"a/b"}})
	wantStatus(t, "removal", status, 200, out)
	if got := p.savedSources(); !reflect.DeepEqual(got, []string{"a/b"}) {
		t.Errorf("saved %v", got)
	}
	status, out = p.put("/sources", map[string]any{"sources": []string{"a/b", "not a repo"}})
	wantStatus(t, "bad entry", status, 400, out)
	wantText(t, "bad entry", out, "not a repo", "owner/name")
	if got := p.savedSources(); !reflect.DeepEqual(got, []string{"a/b"}) {
		t.Errorf("saved %v after a refused list, want the old one", got)
	}
}

func TestAddingASourceWorksWithSharingOff(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{off: true}})
	p.gh.crewListing("a/b", "x.md")
	status, out := p.put("/sources", map[string]any{"sources": []string{"a/b"}})
	wantStatus(t, "PUT sources", status, 200, out)
	if got := p.savedSources(); !reflect.DeepEqual(got, []string{"a/b"}) {
		t.Errorf("saved %v, want [a/b]", got)
	}
}

// --- browse ---

func TestBrowseListsOnlyValidMarkdownFilesInOneCallAndMarksImportedByLocalName(t *testing.T) {
	p := newImp(t, impOpts{})
	p.member("scout", "Mine.")
	p.gh.seed(scout)
	p.importOK("hubot/crew/scout", map[string]any{"name": "scout-hubot"})
	p.gh.unseed()
	list := []map[string]any{
		{"name": "scout.md", "path": "crew/scout.md", "type": "file", "sha": blobOf("scout")},
		{"name": "bosun.md", "path": "crew/bosun.md", "type": "file", "sha": blobOf("bosun")},
		{"name": "Bad Name.md", "path": "crew/Bad Name.md", "type": "file", "sha": blobOf("x")},
		{"name": "notes.txt", "path": "crew/notes.txt", "type": "file", "sha": blobOf("y")},
		{"name": "sub.md", "path": "crew/sub.md", "type": "dir", "sha": blobOf("z")},
	}
	p.gh.ok("GET", "repos/hubot/crew/contents/crew", list)
	calls := len(p.gh.calls())

	status, out := p.getJSON("/sources/hubot/crew")
	wantStatus(t, "browse", status, 200, out)
	rows, _ := out["members"].([]any)
	var names []string
	imported := map[string]any{}
	for _, r := range rows {
		row := r.(map[string]any)
		names = append(names, str(row, "name"))
		imported[str(row, "name")] = row["imported"]
		if row["published"] == true {
			t.Errorf("%v is marked published in a repo that is not the share repo", row)
		}
	}
	if !reflect.DeepEqual(names, []string{"scout", "bosun"}) {
		t.Errorf("rows %v, want scout and bosun only", names)
	}
	if imported["scout"] != "scout-hubot" || imported["bosun"] != nil {
		t.Errorf("imported = %v; want scout-hubot for scout and null for bosun", imported)
	}
	if rows0, _ := rows[1].(map[string]any); rows0["path"] != "crew/bosun.md" || rows0["blob"] != blobOf("bosun") {
		t.Errorf("row = %v", rows[1])
	}
	if got := len(p.gh.calls()) - calls; got != 1 {
		t.Errorf("browse made %d gh calls, want one contents listing", got)
	}
}

func TestBrowsingTheShareRepoMarksEveryRowPublished(t *testing.T) {
	p := newImp(t, impOpts{})
	p.gh.published("bosun", "mate")
	status, out := p.getJSON("/sources/octocat/admiral-crew")
	wantStatus(t, "browse", status, 200, out)
	rows, _ := out["members"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	for _, r := range rows {
		if row := r.(map[string]any); row["published"] != true {
			t.Errorf("row %v is not marked published", row)
		}
	}
}

func TestBrowseOfARepoWithNoCrewFolderIs404(t *testing.T) {
	p := newImp(t, impOpts{})
	p.gh.answer("GET", "repos/hubot/crew/contents/crew", 404, nil, "")
	status, out := p.getJSON("/sources/hubot/crew")
	wantStatus(t, "browse", status, 404, out)
	wantText(t, "browse", out, "crew/")
}

// --- shared_url ---

func TestAMemberWithAnAttachedSidecarHasAServerBuiltSharedURL(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.shared("bosun")
	f.fleetMember("plain", "triager", "Plain.", "")
	want := "https://github.com/octocat/admiral-crew/blob/" + commit40 + "/crew/bosun.md"
	if got := f.rawMember("bosun")["shared_url"]; got != want {
		t.Errorf("shared_url = %v, want %s", got, want)
	}
	if got, present := f.rawMember("plain")["shared_url"]; present && got != nil {
		t.Errorf("a member with no sidecar has shared_url %v", got)
	}
}

// --- actor ---

func TestAnActorGets403OnEveryImportRoute(t *testing.T) {
	p := newImp(t, impOpts{})
	for _, r := range []struct{ method, path string }{
		{"POST", "/imports/preview"}, {"POST", "/imports"}, {"PUT", "/sources"}, {"GET", "/sources/hubot/crew"},
	} {
		status, body := p.asBot(r.method, r.path)
		if status != 403 || !strings.Contains(body, "Only the engineer can import crew members.") {
			t.Errorf("%s %s as a bot: %d %s, want 403 saying only the engineer can import", r.method, r.path, status, body)
		}
	}
	if n := len(p.gh.calls()); n != 0 {
		t.Errorf("%d gh calls for a refused actor", n)
	}
}
