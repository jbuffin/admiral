package crewapi_test

import "testing"

// accessCase is one route of the doc's No access table: setup cans the calls the route makes up to its 404, and gone says whether
// the repo itself answers 404 (no access) or is readable (the route keeps today's answer).
type accessCase struct {
	name string
	repo string
	// readable is the answer the route gives when the repo can be read; 0 means the route has no such case.
	readable int
	run      func(t *testing.T, gone bool) (*pubFixture, int, map[string]any)
}

func canRepo(g *ghFake, full string, gone bool) {
	switch {
	case gone:
		g.answer("GET", "repos/"+full, 404, nil, ghNotFound)
	case full == shareRepo:
		g.ok("GET", "repos/"+full, repoAnswer("private", true, false))
	default:
		g.ok("GET", "repos/"+full, privateRepo(full, false))
	}
}

func accessCases() []accessCase {
	const up = "hubot/crew"
	return []accessCase{
		{"adopt", shareRepo, 0, func(t *testing.T, gone bool) (*pubFixture, int, map[string]any) {
			p := newPub(t, pubOpts{noRepo: true})
			p.gh.ok("GET", "user", map[string]any{"login": "octocat"})
			canRepo(p.gh, shareRepo, gone)
			status, out := p.post("/sharing/repo", map[string]any{"repo": shareRepo})
			return p, status, out
		}},
		{"share preview", shareRepo, 0, func(t *testing.T, gone bool) (*pubFixture, int, map[string]any) {
			p := newPub(t, pubOpts{})
			p.member("bosun", "Do the work.")
			p.gh.ok("GET", "user", map[string]any{"login": "octocat"})
			canRepo(p.gh, shareRepo, gone)
			status, out := p.preview("bosun")
			return p, status, out
		}},
		{"share", shareRepo, 0, func(t *testing.T, gone bool) (*pubFixture, int, map[string]any) {
			p := newPub(t, pubOpts{})
			p.member("bosun", "Do the work.")
			p.gh.ok("GET", "user", map[string]any{"login": "octocat"})
			canRepo(p.gh, shareRepo, gone)
			status, out := p.share("bosun", map[string]any{"sha256": hexSum("x"), "repo": shareRepo, "replaces_blob": nil, "private": true}, nil)
			return p, status, out
		}},
		{"get published", shareRepo, 404, func(t *testing.T, gone bool) (*pubFixture, int, map[string]any) {
			p := newPub(t, pubOpts{})
			p.gh.answer("GET", memberPath("bosun"), 404, nil, ghNotFound)
			canRepo(p.gh, shareRepo, gone)
			status, out := p.getJSON("/published/bosun")
			return p, status, out
		}},
		{"delete published", shareRepo, 404, func(t *testing.T, gone bool) (*pubFixture, int, map[string]any) {
			p := newPub(t, pubOpts{})
			p.gh.answer("DELETE", memberPath("bosun"), 404, nil, ghNotFound)
			canRepo(p.gh, shareRepo, gone)
			status, text := p.do("DELETE", p.base()+"/published/bosun", map[string]any{"blob": blobOf("bosun"), "repo": shareRepo})
			return p, status, parse(t, text)
		}},
		{"import preview", up, 0, func(t *testing.T, gone bool) (*pubFixture, int, map[string]any) {
			p := newImp(t, impOpts{})
			canRepo(p.gh, up, gone)
			status, out := p.importPreview("hubot/crew/scout")
			return p, status, out
		}},
		{"sources", up, 0, func(t *testing.T, gone bool) (*pubFixture, int, map[string]any) {
			p := newImp(t, impOpts{})
			canRepo(p.gh, up, gone)
			status, out := p.put("/sources", map[string]any{"sources": []string{up}})
			return p, status, out
		}},
		{"browse", up, 404, func(t *testing.T, gone bool) (*pubFixture, int, map[string]any) {
			p := newImp(t, impOpts{})
			p.gh.answer("GET", "repos/"+up+"/contents/crew", 404, nil, ghNotFound)
			canRepo(p.gh, up, gone)
			status, out := p.getJSON("/sources/hubot/crew")
			return p, status, out
		}},
		{"update preview", up, 0, func(t *testing.T, gone bool) (*pubFixture, int, map[string]any) {
			p := newImp(t, impOpts{})
			p.clockIt(t)
			p.attach(att{name: "scout", text: oldScout, tag: "v1"})
			canRepo(p.gh, up, gone)
			status, out := p.post("/members/scout/update/preview", nil)
			return p, status, out
		}},
		{"update apply", up, 404, func(t *testing.T, gone bool) (*pubFixture, int, map[string]any) {
			p := newImp(t, impOpts{})
			p.clockIt(t)
			p.attach(att{name: "scout", text: oldScout, tag: "v1"})
			p.gh.answer("GET", "repos/"+up+"/contents/crew/scout.md?ref="+newSHA, 404, nil, ghNotFound)
			canRepo(p.gh, up, gone)
			status, out := p.post("/members/scout/update", map[string]any{"commit": newSHA, "sha256": hexSum(newScout), "current_sha256": hexSum(oldScout)})
			return p, status, out
		}},
		{"import", up, 404, func(t *testing.T, gone bool) (*pubFixture, int, map[string]any) {
			p := newImp(t, impOpts{})
			p.gh.answer("GET", "repos/"+up+"/contents/crew/scout.md?ref="+commitSHA, 404, nil, ghNotFound)
			canRepo(p.gh, up, gone)
			status, out := p.post("/imports", map[string]any{"repo": up, "path": "crew/scout.md", "commit": commitSHA, "sha256": hexSum(scout.text)})
			return p, status, out
		}},
	}
}

func TestARepo404IsNoAccessOnEveryRouteOfTheNoAccessTable(t *testing.T) {
	for _, c := range accessCases() {
		t.Run(c.name, func(t *testing.T) {
			p, status, out := c.run(t, true)
			wantNoAccess(t, c.name, status, out, c.repo)
			if c.name != "delete published" {
				wantNoWrites(t, p)
			}
			p.wantNothingImported(c.name, "bosun.md", "scout.md", "scout.shared.json")
		})
	}
}

func TestAFileOrListing404InAReadableRepoKeepsTodaysAnswerWithNoCode(t *testing.T) {
	for _, c := range accessCases() {
		if c.readable == 0 {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			_, status, out := c.run(t, false)
			wantStatus(t, c.name, status, c.readable, out)
			wantNoCode(t, c.name, out)
		})
	}
}

func TestARepoThatGhReads403ForIsTodays502NotNoAccess(t *testing.T) {
	saml := "gh: Resource protected by organization SAML enforcement. (HTTP 403)"
	p := newImp(t, impOpts{})
	p.gh.answer("GET", "repos/hubot/crew", 403, nil, saml)
	status, out := p.importPreview("hubot/crew/scout")
	wantStatus(t, "import preview", status, 502, out)
	wantNoCode(t, "import preview", out)

	q := newPub(t, pubOpts{})
	q.member("bosun", "Do the work.")
	q.gh.ok("GET", "user", map[string]any{"login": "octocat"})
	q.gh.answer("GET", "repos/"+shareRepo, 403, nil, saml)
	status, out = q.preview("bosun")
	wantStatus(t, "share preview", status, 502, out)
	wantNoCode(t, "share preview", out)
}

func TestSharingOffAndABotKeepTheirOwn403WithNoCode(t *testing.T) {
	p := newPub(t, pubOpts{off: true})
	p.member("bosun", "Do the work.")
	status, out := p.preview("bosun")
	wantStatus(t, "sharing off", status, 403, out)
	wantNoCode(t, "sharing off", out)

	q := newImp(t, impOpts{})
	status, text := q.asBot("GET", "/sources/hubot/crew")
	out = parse(t, text)
	wantStatus(t, "bot", status, 403, out)
	wantNoCode(t, "bot", out)
}
