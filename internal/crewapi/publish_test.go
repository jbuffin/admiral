package crewapi_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/crewapi"
)

const (
	offText     = "Crew sharing is off for this fleet. Turn it on in Preferences."
	connectText = "Connect GitHub (github.com) for this fleet first."
	importText  = "Imported members can't be shared. Make it yours first."
	changedText = "The member changed since the preview"
	fileChanged = "The published file changed since the preview"
	ghNotFound  = "gh: Not Found (HTTP 404)"
)

func wantStatus(t *testing.T, what string, got, want int, body map[string]any) {
	t.Helper()
	if got != want {
		t.Errorf("%s: status %d, want %d (%v)", what, got, want, body)
	}
}

func wantText(t *testing.T, what string, body map[string]any, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(errText(body), part) {
			t.Errorf("%s: error %q lacks %q", what, errText(body), part)
		}
	}
}

func wantNoWrites(t *testing.T, p *pubFixture) {
	t.Helper()
	if w := p.gh.writes(); len(w) != 0 {
		t.Errorf("gh wrote: %+v", w)
	}
}

// --- sharing off ---

func TestSharingOffRefusesRepoPreviewAndShareAndCallsNoGh(t *testing.T) {
	p := newPub(t, pubOpts{off: true})
	p.member("bosun", "Do the work.")
	for _, path := range []string{"/sharing/repo", "/members/bosun/share/preview", "/members/bosun/share"} {
		status, out := p.post(path, map[string]any{"repo": shareRepo, "name": "admiral-crew", "sha256": "x"})
		wantStatus(t, path, status, 403, out)
		wantText(t, path, out, offText)
	}
	if n := len(p.gh.calls()); n != 0 {
		t.Errorf("gh was called %d times with sharing off", n)
	}
}

func TestUnpublishWorksWithSharingOffAndLeavesTheLocalMemberAlone(t *testing.T) {
	p := newPub(t, pubOpts{off: true})
	p.member("bosun", "Do the work.")
	local := readFile(t, filepath.Join(p.crewDir(), "bosun.md"))
	p.gh.file("bosun", "published bytes\n")
	p.gh.commitOK("DELETE", "bosun")
	status, out := p.getJSON("/published/bosun")
	wantStatus(t, "GET published", status, 200, out)
	if str(out, "bytes") != "published bytes\n" || str(out, "blob") != blobOf("bosun") {
		t.Errorf("GET published = %v", out)
	}
	status, text := p.do("DELETE", p.base()+"/published/bosun", map[string]any{"blob": blobOf("bosun")})
	if status != 200 {
		t.Fatalf("DELETE published: %d %s", status, text)
	}
	if got := readFile(t, filepath.Join(p.crewDir(), "bosun.md")); got != local {
		t.Errorf("the local member changed:\n%s", got)
	}
}

// --- no GitHub source, or not github.com ---

func TestWithoutAGitHubSourceOrOnAnotherHostEveryRouteIs409AndCallsNoGh(t *testing.T) {
	for name, o := range map[string]pubOpts{"no source": {noGitHub: true}, "other host": {host: "ghe.example.com"}} {
		t.Run(name, func(t *testing.T) {
			p := newPub(t, o)
			p.member("bosun", "Do the work.")
			cases := []struct{ method, path string }{
				{"POST", "/sharing/repo"}, {"POST", "/members/bosun/share/preview"}, {"POST", "/members/bosun/share"},
				{"GET", "/published/bosun"}, {"DELETE", "/published/bosun"},
			}
			for _, c := range cases {
				status, text := p.do(c.method, p.base()+c.path, map[string]any{"repo": shareRepo, "sha256": "x", "blob": "x"})
				out := parse(t, text)
				wantStatus(t, c.method+" "+c.path, status, 409, out)
				wantText(t, c.method+" "+c.path, out, connectText)
			}
			if n := len(p.gh.calls()); n != 0 {
				t.Errorf("gh was called %d times", n)
			}
		})
	}
}

// --- create or adopt the share repo ---

func (p *pubFixture) createAnswers() {
	p.gh.ok("POST", "user/repos", map[string]any{"full_name": shareRepo, "name": "admiral-crew", "owner": map[string]any{"login": "octocat"}})
	p.gh.ok("PUT", "repos/"+shareRepo+"/contents/README.md", map[string]any{"commit": map[string]any{"html_url": "https://github.com/" + shareRepo + "/commit/r"}})
}

func TestCreateRepoMakesAPublicRepoThenTheREADMEThenSavesShareRepo(t *testing.T) {
	p := newPub(t, pubOpts{noRepo: true})
	p.createAnswers()
	status, out := p.post("/sharing/repo", map[string]any{"name": "admiral-crew", "description": "My crew"})
	wantStatus(t, "create", status, 200, out)
	w := p.gh.writes()
	if len(w) != 2 || w[0].Method != "POST" || w[0].Endpoint != "user/repos" || w[1].Method != "PUT" || w[1].Endpoint != "repos/"+shareRepo+"/contents/README.md" {
		t.Fatalf("writes = %+v, want POST user/repos then PUT README.md", w)
	}
	repo := putBody(t, w[0])
	if repo["private"] != false || repo["auto_init"] != false || repo["name"] != "admiral-crew" || repo["description"] != "My crew" {
		t.Errorf("create body = %v", repo)
	}
	readme := putBody(t, w[1])
	if str(readme, "message") != "Add README" {
		t.Errorf("README message = %q", str(readme, "message"))
	}
	if text := decodeContent(t, readme); text == "" || strings.Contains(text, "octocat") {
		t.Errorf("README = %q, want text that names no user", text)
	}
	if _, has := readme["sha"]; has {
		t.Error("the README PUT carries a sha")
	}
	if !strings.Contains(slurp(filepath.Join(p.home, "config.json")), `"share_repo": "`+shareRepo+`"`) &&
		!strings.Contains(slurp(filepath.Join(p.home, "config.json")), `"share_repo":"`+shareRepo+`"`) {
		t.Errorf("config.json lacks share_repo:\n%s", slurp(filepath.Join(p.home, "config.json")))
	}
}

func TestCreateRepoWithATakenNameIs409AndOffersAPublicPushableRepo(t *testing.T) {
	p := newPub(t, pubOpts{noRepo: true})
	p.gh.answer("POST", "user/repos", 422, nil, "gh: Repository creation failed. (HTTP 422)")
	p.gh.ok("GET", "user", map[string]any{"login": "octocat"})
	p.gh.ok("GET", "repos/"+shareRepo, repoAnswer("public", true, false))
	status, out := p.post("/sharing/repo", map[string]any{"name": "admiral-crew"})
	wantStatus(t, "taken", status, 409, out)
	if out["use_existing"] != shareRepo {
		t.Errorf("use_existing = %v, want %s", out["use_existing"], shareRepo)
	}
	if len(p.gh.writes()) != 1 {
		t.Errorf("writes = %+v, want only the failed create", p.gh.writes())
	}
}

func TestCreateRepoWithATakenNameOffersNothingWhenTheRepoCannotBeUsed(t *testing.T) {
	for name, repo := range map[string]map[string]any{
		"private":  repoAnswer("private", true, false),
		"archived": repoAnswer("public", true, true),
		"no push":  repoAnswer("public", false, false),
	} {
		t.Run(name, func(t *testing.T) {
			p := newPub(t, pubOpts{noRepo: true})
			p.gh.answer("POST", "user/repos", 422, nil, "")
			p.gh.ok("GET", "user", map[string]any{"login": "octocat"})
			p.gh.ok("GET", "repos/"+shareRepo, repo)
			status, out := p.post("/sharing/repo", map[string]any{"name": "admiral-crew"})
			wantStatus(t, name, status, 409, out)
			if _, has := out["use_existing"]; has {
				t.Errorf("use_existing offered for a %s repo: %v", name, out)
			}
			wantText(t, name, out, shareRepo)
		})
	}
}

func TestAdoptSavesAnExistingPublicPushableRepo(t *testing.T) {
	p := newPub(t, pubOpts{noRepo: true})
	p.gh.healthyRepo()
	status, out := p.post("/sharing/repo", map[string]any{"repo": shareRepo})
	wantStatus(t, "adopt", status, 200, out)
	wantNoWrites(t, p)
	_, view := p.getJSON("/sharing")
	cs, _ := view["crew_sharing"].(map[string]any)
	if cs["share_repo"] != shareRepo {
		t.Errorf("crew_sharing = %v, want share_repo %s", cs, shareRepo)
	}
}

func TestAdoptRefusesAPrivateArchivedOrUnpushableRepoAndSavesNothing(t *testing.T) {
	cases := map[string]struct {
		repo   map[string]any
		reason string
	}{
		"private":  {repoAnswer("private", true, false), "private"},
		"archived": {repoAnswer("public", true, true), "archived"},
		"no push":  {repoAnswer("public", false, false), "push"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPub(t, pubOpts{noRepo: true})
			p.gh.ok("GET", "user", map[string]any{"login": "octocat"})
			p.gh.ok("GET", "repos/"+shareRepo, c.repo)
			status, out := p.post("/sharing/repo", map[string]any{"repo": shareRepo})
			wantStatus(t, name, status, 409, out)
			wantText(t, name, out, shareRepo, c.reason)
			_, view := p.getJSON("/sharing")
			if cs, _ := view["crew_sharing"].(map[string]any); cs["share_repo"] != nil && cs["share_repo"] != "" {
				t.Errorf("share_repo saved after a refusal: %v", cs)
			}
		})
	}
}

// --- preview and Share ---

func (p *pubFixture) ready(name, body string) {
	p.member(name, body)
	p.gh.healthyRepo()
	p.gh.answer("GET", listingPath, 404, nil, ghNotFound)
}

func TestPreviewOfACleanMemberAndShareCommitExactlyThosBytes(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.ready("bosun", "Do the work.")
	p.gh.commitOK("PUT", "bosun")
	status, pv := p.preview("bosun")
	wantStatus(t, "preview", status, 200, pv)
	bytes := str(pv, "bytes")
	scan, _ := pv["scan"].(map[string]any)
	if bytes == "" || str(pv, "sha256") != hexSum(bytes) || str(pv, "path") != "crew/bosun.md" || str(pv, "repo") != shareRepo ||
		pv["replaces_blob"] != nil || scan["state"] != "clean" {
		t.Fatalf("preview = %v", pv)
	}
	if len(p.gh.writes()) != 0 {
		t.Errorf("preview wrote: %+v", p.gh.writes())
	}
	status, out := p.share("bosun", pv, nil)
	wantStatus(t, "share", status, 200, out)
	if !strings.Contains(str(out, "commit_url"), "/commit/abc123") {
		t.Errorf("share = %v, want the commit URL", out)
	}
	puts := p.gh.find("PUT", memberPath("bosun"))
	if len(puts) != 1 {
		t.Fatalf("PUT calls = %d, want 1 (%+v)", len(puts), p.gh.writes())
	}
	body := putBody(t, puts[0])
	if got := decodeContent(t, body); got != bytes {
		t.Errorf("committed bytes differ from the preview:\n%q\n%q", got, bytes)
	}
	if _, has := body["sha"]; has {
		t.Errorf("a new file's PUT carries sha %v", body["sha"])
	}
	if str(body, "message") != "Share crew member bosun" {
		t.Errorf("message = %q", str(body, "message"))
	}
	argv := strings.Join(puts[0].Argv, " ")
	if strings.Contains(argv, " -F") || strings.Contains(argv, "--field") || !strings.Contains(argv, "--input -") || puts[0].Host != "github.com" {
		t.Errorf("argv = %q host %q, want the body on stdin and --hostname github.com", argv, puts[0].Host)
	}
}

func TestPreviewOfAPublishedMemberReplacesItAndSharePutsTheBlobAsSha(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.member("bosun", "Do the work, new.")
	p.gh.healthyRepo()
	p.gh.published("bosun")
	p.gh.file("bosun", "old bytes\n")
	p.gh.commitOK("PUT", "bosun")
	status, pv := p.preview("bosun")
	wantStatus(t, "preview", status, 200, pv)
	if pv["replaces_blob"] != blobOf("bosun") || str(pv, "published_bytes") != "old bytes\n" {
		t.Fatalf("preview = %v, want replaces_blob %s and the old bytes", pv, blobOf("bosun"))
	}
	status, out := p.share("bosun", pv, nil)
	wantStatus(t, "share", status, 200, out)
	puts := p.gh.find("PUT", memberPath("bosun"))
	if len(puts) != 1 || putBody(t, puts[0])["sha"] != blobOf("bosun") {
		t.Errorf("PUT = %+v, want sha %s", puts, blobOf("bosun"))
	}
}

func TestShareOfAMemberEditedAfterThePreviewIs409AndPutsNothing(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.ready("bosun", "Do the work.")
	_, pv := p.preview("bosun")
	p.member("bosun", "Do the work, edited.")
	status, out := p.share("bosun", pv, nil)
	wantStatus(t, "share", status, 409, out)
	wantText(t, "share", out, changedText)
	wantNoWrites(t, p)
}

func TestShareWithAnotherRepoThanTheShareRepoIs409AndPutsNothing(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.ready("bosun", "Do the work.")
	_, pv := p.preview("bosun")
	status, out := p.share("bosun", pv, map[string]any{"repo": "mallory/elsewhere"})
	wantStatus(t, "share", status, 409, out)
	wantNoWrites(t, p)
}

func TestShareMapsGitHubs409And422OnTheShaTo409(t *testing.T) {
	for _, code := range []int{409, 422} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			p := newPub(t, pubOpts{})
			p.ready("bosun", "Do the work.")
			p.gh.answer("PUT", memberPath("bosun"), code, nil, "gh: Conflict (HTTP "+strconv.Itoa(code)+")")
			_, pv := p.preview("bosun")
			status, out := p.share("bosun", pv, nil)
			wantStatus(t, "share", status, 409, out)
			wantText(t, "share", out, fileChanged)
		})
	}
}

func TestSharingAMemberWithASidecarOrABuiltInIs409AndPutsNothing(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.gh.healthyRepo()
	p.gh.answer("GET", listingPath, 404, nil, ghNotFound)
	p.shared("attached")
	p.shared("detached")
	writeFile(t, filepath.Join(p.crewDir(), "detached.md"), memberText("detached", "triager", "Edited after import.", ""))
	p.fleetMember("unreadable", "triager", "Odd sidecar.", "")
	writeFile(t, p.sidecarPath("unreadable"), "not json")
	builtInMember(t, p.builtIns, "mate", "triager", "A built-in.")
	for _, name := range []string{"attached", "detached", "unreadable"} {
		status, out := p.preview(name)
		wantStatus(t, name+" preview", status, 409, out)
		wantText(t, name, out, importText)
		pv := map[string]any{"sha256": hexSum("x"), "repo": shareRepo, "replaces_blob": nil}
		status, out = p.share(name, pv, nil)
		wantStatus(t, name+" share", status, 409, out)
		wantText(t, name+" share", out, importText)
	}
	status, out := p.preview("mate")
	wantStatus(t, "built-in preview", status, 409, out)
	status, out = p.share("mate", map[string]any{"sha256": hexSum("x"), "repo": shareRepo}, nil)
	wantStatus(t, "built-in share", status, 409, out)
	wantNoWrites(t, p)
}

func TestPreviewRefusesAHiddenRuneAPathAndGitleaksAllowWith422AndPutsNothing(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"hidden", "Do‮ the work.", []string{"U+202E on line"}},
		{"path", "Read /Users/someone/secrets.txt first.", []string{"line"}},
		{"allow", "Do it. gitleaks:allow", []string{"gitleaks:allow", "line"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPub(t, pubOpts{})
			body := c.body
			if c.name == "path" {
				body = "Read " + p.home + "/secrets.txt first."
			}
			p.ready("bosun", body)
			status, out := p.preview("bosun")
			wantStatus(t, c.name, status, 422, out)
			wantText(t, c.name, out, c.want...)
			wantNoWrites(t, p)
			if exists(filepath.Join(p.leaks, "last")) {
				t.Error("gitleaks ran after a privacy check failed")
			}
		})
	}
}

// --- the secret scan ---

func TestAHitIs422NamingTheLineAndRuleAndNeverTheSecret(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.ready("bosun", "Token "+decoy+" here.")
	canonical := hexSum(string(crewapi.CanonicalMemberBytes(resolvedMember(t, readFile(t, filepath.Join(p.crewDir(), "bosun.md"))))))
	bodies := map[string]map[string]any{"/members/bosun/share/preview": {}, "/members/bosun/share": {"sha256": canonical, "repo": shareRepo}}
	for path, body := range bodies {
		status, text := p.do("POST", p.base()+path, body)
		out := parse(t, text)
		wantStatus(t, path, status, 422, out)
		wantText(t, path, out, "decoy-secret", "line")
		scan, _ := out["scan"].(map[string]any)
		if findings, _ := scan["findings"].([]any); scan["state"] != "hit" || len(findings) != 1 {
			t.Errorf("%s scan = %v, want one finding", path, scan)
		}
		if strings.Contains(text, decoy) {
			t.Errorf("%s response holds the secret", path)
		}
	}
	wantNoWrites(t, p)
}

func TestMissingOrTooOldGitleaksNeedsTheOptOutAndTheOptOutCommits(t *testing.T) {
	cases := map[string]struct {
		opts    pubOpts
		version string
		state   string
		hint    string
	}{
		"missing": {pubOpts{noGitleak: true}, "", "missing", "brew install gitleaks"},
		"too old": {pubOpts{}, "8.18.0", "too-old", "brew upgrade gitleaks"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPub(t, c.opts)
			if c.version != "" {
				p.leakVersion(c.version)
			}
			p.ready("bosun", "Do the work.")
			p.gh.commitOK("PUT", "bosun")
			status, pv := p.preview("bosun")
			wantStatus(t, "preview", status, 200, pv)
			scan, _ := pv["scan"].(map[string]any)
			if scan["state"] != c.state || !strings.Contains(str(scan, "install"), c.hint) {
				t.Fatalf("scan = %v, want %s with %q", scan, c.state, c.hint)
			}
			status, out := p.share("bosun", pv, nil)
			wantStatus(t, "share without the opt-out", status, 422, out)
			wantNoWrites(t, p)
			status, out = p.share("bosun", pv, map[string]any{"skip_secret_scan": true})
			wantStatus(t, "share with the opt-out", status, 200, out)
			if len(p.gh.find("PUT", memberPath("bosun"))) != 1 {
				t.Errorf("writes = %+v, want the one PUT", p.gh.writes())
			}
			if cfg := slurp(filepath.Join(p.home, "config.json")); strings.Contains(cfg, "scan") || strings.Contains(cfg, "skip") {
				t.Errorf("config.json holds a scan choice:\n%s", cfg)
			}
		})
	}
}

func TestTheOptOutWithAUsableGitleaksIs400ForCleanHitAndFailed(t *testing.T) {
	cases := map[string]struct{ body, fail string }{
		"clean":  {"Do the work.", ""},
		"hit":    {"Token " + decoy, ""},
		"failed": {"Do the work.", "error"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPub(t, pubOpts{})
			p.ready("bosun", c.body)
			if c.fail != "" {
				p.leakFail(c.fail)
			}
			pv := map[string]any{"sha256": hexSum("x"), "repo": shareRepo, "replaces_blob": nil}
			status, out := p.share("bosun", pv, map[string]any{"skip_secret_scan": true})
			wantStatus(t, name, status, 400, out)
			wantNoWrites(t, p)
		})
	}
}

func TestAScanThatCrashesErrorsOrHangsIs422WithTheErrorAndNoOptOut(t *testing.T) {
	for mode, want := range map[string]string{"crash": "", "error": "fake gitleaks: boom", "hang": "timed out"} {
		t.Run(mode, func(t *testing.T) {
			p := newPub(t, pubOpts{})
			p.ready("bosun", "Do the work.")
			p.leakFail(mode)
			status, out := p.preview("bosun")
			wantStatus(t, mode, status, 422, out)
			scan, _ := out["scan"].(map[string]any)
			if scan["state"] != "failed" || str(scan, "error") == "" || !strings.Contains(str(scan, "error")+errText(out), want) {
				t.Errorf("%s: out = %v, want a failed scan with %q", mode, out, want)
			}
			wantNoWrites(t, p)
		})
	}
}

// --- errors from GitHub ---

func TestASignedOutGhIs502NamingTheStepAndTheReconnectAdvice(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.member("bosun", "Do the work.")
	p.gh.signedOut()
	status, out := p.preview("bosun")
	wantStatus(t, "preview", status, 502, out)
	wantText(t, "preview", out, "no oauth token found")
	if !strings.Contains(strings.ToLower(errText(out)), "reconnect") {
		t.Errorf("error %q lacks the reconnect advice", errText(out))
	}
}

func TestARateLimitIs429(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.member("bosun", "Do the work.")
	p.gh.answer("GET", "repos/"+shareRepo, 403, nil, "gh: API rate limit exceeded for user ID 1. (HTTP 403)")
	status, out := p.preview("bosun")
	wantStatus(t, "rate limit", status, 429, out)
	wantText(t, "rate limit", out, shareRepo)
}

func TestAShareRepoThatCannotBeWrittenIs409AtPreviewNamingTheRepoAndTheReason(t *testing.T) {
	cases := map[string]struct {
		repo   map[string]any
		status int
		reason string
	}{
		"private":  {repoAnswer("private", true, false), 200, "private"},
		"archived": {repoAnswer("public", true, true), 200, "archived"},
		"no push":  {repoAnswer("public", false, false), 200, "push"},
		"gone":     {nil, 404, "not found"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPub(t, pubOpts{})
			p.member("bosun", "Do the work.")
			p.gh.ok("GET", "user", map[string]any{"login": "octocat"})
			p.gh.answer("GET", "repos/"+shareRepo, c.status, c.repo, ghNotFound)
			status, out := p.preview("bosun")
			wantStatus(t, name, status, 409, out)
			wantText(t, name, out, shareRepo, "Choose another repo")
			if !strings.Contains(strings.ToLower(errText(out)), c.reason) {
				t.Errorf("error %q lacks %q", errText(out), c.reason)
			}
		})
	}
}

// --- the sharing view, the published listing and its cache ---

func TestTheSharingViewShowsTheSettingsLoginRepoStateAndPublishedNames(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.gh.healthyRepo()
	p.gh.published("bosun", "mate-two")
	status, out := p.getJSON("/sharing")
	wantStatus(t, "sharing", status, 200, out)
	cs, _ := out["crew_sharing"].(map[string]any)
	repo, _ := out["repo"].(map[string]any)
	names, _ := out["published"].([]any)
	if cs["enabled"] != true || cs["share_repo"] != shareRepo || out["login"] != "octocat" ||
		repo["exists"] != true || repo["public"] != true || repo["pushable"] != true || len(names) != 2 || names[0] != "bosun" {
		t.Errorf("sharing = %v", out)
	}
}

func TestPublishedOnTheCrewViewComesFromOneCachedListingAndABotNeverTriggersACall(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.member("bosun", "Do the work.")
	p.member("mate", "Other.")
	p.gh.published("bosun")
	// A bot with a cold cache calls nothing and sees nothing published.
	status, text := p.asBot("GET", "")
	if status != 200 || len(p.gh.calls()) != 0 {
		t.Fatalf("cold bot read: %d, gh calls %d", status, len(p.gh.calls()))
	}
	if m := parse(t, text); published(m, "bosun") {
		t.Errorf("a cold bot read shows bosun published: %s", text)
	}
	// An engineer's read lists once and shows it; a second read reuses it.
	for i := 0; i < 2; i++ {
		if p.rawMember("bosun")["published"] != true || p.rawMember("mate")["published"] != false {
			t.Fatalf("read %d: published flags wrong", i)
		}
	}
	if n := len(p.gh.find("GET", listingPath)); n != 1 {
		t.Errorf("listing calls = %d, want 1", n)
	}
	// The bot now reads the cache, with no further call.
	status, text = p.asBot("GET", "")
	if m := parse(t, text); status != 200 || !published(m, "bosun") || len(p.gh.find("GET", listingPath)) != 1 {
		t.Errorf("warm bot read: %d %s, listing calls %d", status, text, len(p.gh.find("GET", listingPath)))
	}
}

func published(crew map[string]any, name string) bool {
	members, _ := crew["members"].([]any)
	for _, m := range members {
		if mm, _ := m.(map[string]any); mm["name"] == name {
			return mm["published"] == true
		}
	}
	return false
}

func TestShareAndUnpublishClearTheListingCache(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.ready("bosun", "Do the work.")
	p.gh.commitOK("PUT", "bosun")
	p.gh.commitOK("DELETE", "bosun")
	p.gh.file("bosun", "old\n")
	p.rawMember("bosun")
	_, pv := p.preview("bosun")
	if _, out := p.share("bosun", pv, nil); out["error"] != nil {
		t.Fatalf("share: %v", out)
	}
	before := len(p.gh.find("GET", listingPath))
	p.rawMember("bosun")
	if got := len(p.gh.find("GET", listingPath)); got != before+1 {
		t.Errorf("after Share, listing calls %d -> %d, want one more", before, got)
	}
	p.do("DELETE", p.base()+"/published/bosun", map[string]any{"blob": blobOf("bosun")})
	before = len(p.gh.find("GET", listingPath))
	p.rawMember("bosun")
	if got := len(p.gh.find("GET", listingPath)); got != before+1 {
		t.Errorf("after Unpublish, listing calls %d -> %d, want one more", before, got)
	}
}

func TestAFailedListingGivesPublishedFalseAndAnErrorNever5xxOnTheCrewView(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.member("bosun", "Do the work.")
	p.gh.answer("GET", listingPath, 500, nil, "gh: Server Error (HTTP 500)")
	if m := p.rawMember("bosun"); m["published"] != false {
		t.Errorf("published = %v, want false", m["published"])
	}
	_, view := p.getJSON("/sharing")
	if str(view, "published_error") == "" {
		t.Errorf("sharing = %v, want published_error", view)
	}
}

func TestAMissingCrewDirectoryMeansNothingPublishedNotAnError(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.gh.healthyRepo()
	p.gh.answer("GET", listingPath, 404, nil, ghNotFound)
	_, view := p.getJSON("/sharing")
	names, _ := view["published"].([]any)
	if str(view, "published_error") != "" || len(names) != 0 || len(p.gh.find("GET", listingPath)) != 1 {
		t.Errorf("sharing = %v, want an empty list and no error after one listing", view)
	}
}

func TestAThousandEntryListingIsAnErrorNotNothingPublished(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.gh.healthyRepo()
	names := make([]string, 1000)
	for i := range names {
		names[i] = "m" + strconv.Itoa(i)
	}
	p.gh.published(names...)
	_, view := p.getJSON("/sharing")
	if str(view, "published_error") == "" {
		t.Errorf("a 1000-entry listing gave no error: %v", view["published_error"])
	}
	p2 := newPub(t, pubOpts{})
	p2.gh.healthyRepo()
	p2.gh.published(names[:999]...)
	_, view = p2.getJSON("/sharing")
	if got, _ := view["published"].([]any); str(view, "published_error") != "" || len(got) != 999 {
		t.Errorf("a 999-entry listing: %d names, error %q", len(got), str(view, "published_error"))
	}
}

func TestNoListingIsFetchedWithoutAShareRepo(t *testing.T) {
	p := newPub(t, pubOpts{noRepo: true})
	p.member("bosun", "Do the work.")
	if m := p.rawMember("bosun"); m["published"] != false {
		t.Errorf("published = %v, want false", m["published"])
	}
	if n := len(p.gh.calls()); n != 0 {
		t.Errorf("gh was called %d times", n)
	}
}

// --- Unpublish ---

func TestUnpublishDeletesWithTheBlobAndLeavesTheLocalMemberByteIdentical(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.member("bosun", "Do the work.")
	local := readFile(t, filepath.Join(p.crewDir(), "bosun.md"))
	p.gh.file("bosun", "published\n")
	p.gh.commitOK("DELETE", "bosun")
	status, out := p.getJSON("/published/bosun")
	wantStatus(t, "GET", status, 200, out)
	status, text := p.do("DELETE", p.base()+"/published/bosun", map[string]any{"blob": str(out, "blob")})
	if status != 200 {
		t.Fatalf("DELETE: %d %s", status, text)
	}
	dels := p.gh.find("DELETE", memberPath("bosun"))
	if len(dels) != 1 {
		t.Fatalf("DELETE calls = %+v", p.gh.writes())
	}
	body := putBody(t, dels[0])
	if body["sha"] != blobOf("bosun") || str(body, "message") != "Unpublish crew member bosun" {
		t.Errorf("DELETE body = %v", body)
	}
	if readFile(t, filepath.Join(p.crewDir(), "bosun.md")) != local {
		t.Error("the local member changed")
	}
}

func TestUnpublishWithAStaleBlobIs409(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.gh.answer("DELETE", memberPath("bosun"), 409, nil, "gh: Conflict (HTTP 409)")
	status, text := p.do("DELETE", p.base()+"/published/bosun", map[string]any{"blob": blobOf("stale")})
	out := parse(t, text)
	wantStatus(t, "stale", status, 409, out)
	if len(p.gh.find("DELETE", memberPath("bosun"))) != 1 {
		t.Errorf("no DELETE was sent: %+v", p.gh.calls())
	}
}

func TestUnpublishOfAFileThatIsNotThereIs404NamingIt(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.gh.answer("GET", memberPath("bosun"), 404, nil, ghNotFound)
	p.gh.answer("DELETE", memberPath("bosun"), 404, nil, ghNotFound)
	status, out := p.getJSON("/published/bosun")
	wantStatus(t, "GET", status, 404, out)
	wantText(t, "GET", out, "bosun")
	status, text := p.do("DELETE", p.base()+"/published/bosun", map[string]any{"blob": blobOf("bosun")})
	out = parse(t, text)
	wantStatus(t, "DELETE", status, 404, out)
	if len(p.gh.find("GET", memberPath("bosun"))) != 1 || len(p.gh.find("DELETE", memberPath("bosun"))) != 1 {
		t.Errorf("calls = %+v, want one GET and one DELETE", p.gh.calls())
	}
}

func TestUnpublishRefusesABadNameBeforeCallingGh(t *testing.T) {
	p := newPub(t, pubOpts{})
	status, _ := p.do("GET", p.base()+"/published/Bad_Name", nil)
	if status != 400 && status != 404 {
		t.Errorf("status = %d, want 400 or 404", status)
	}
	if n := len(p.gh.calls()); n != 0 {
		t.Errorf("gh was called %d times", n)
	}
}

func TestAPublishedFileOverSixtyFourKiBIsRefusedBeforeItsContentIsDecoded(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.gh.ok("GET", memberPath("bosun"), map[string]any{"name": "bosun.md", "sha": blobOf("bosun"), "size": 70000, "encoding": "base64", "content": b64("tiny")})
	p.gh.file("mate", "small\n")
	if status, out := p.getJSON("/published/mate"); status != 200 || str(out, "bytes") != "small\n" {
		t.Errorf("GET small = %d %v, want its bytes", status, out)
	}
	status, out := p.getJSON("/published/bosun")
	if status < 400 || status == 404 || status == 501 || out["bytes"] != nil || len(p.gh.find("GET", memberPath("bosun"))) != 1 {
		t.Errorf("GET published = %d %v, want a refusal after one contents call and no bytes", status, out)
	}
}

