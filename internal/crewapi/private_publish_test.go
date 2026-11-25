package crewapi_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/crewapi"
)

// homeText is every file under the fleet's home folder, joined: the config, the crew and its sidecars.
func (p *pubFixture) homeText() string {
	p.t.Helper()
	var all strings.Builder
	_ = filepath.WalkDir(p.home, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(path)
			all.Write(b)
		}
		return nil
	})
	return all.String()
}

// committed is the bytes and message of the one PUT a share made.
func committed(t *testing.T, p *pubFixture, name string) (string, string) {
	t.Helper()
	puts := p.gh.find("PUT", memberPath(name))
	if len(puts) != 1 {
		t.Fatalf("PUT calls = %+v, want 1", p.gh.writes())
	}
	body := putBody(t, puts[0])
	return decodeContent(t, body), str(body, "message")
}

// --- adopt, preview, share and the published routes against a private repo ---

func TestAdoptSavesAnExistingPrivatePushableRepo(t *testing.T) {
	p := newPub(t, pubOpts{noRepo: true})
	p.gh.privateShareRepo()
	status, out := p.post("/sharing/repo", map[string]any{"repo": shareRepo})
	wantStatus(t, "adopt", status, 200, out)
	wantNoWrites(t, p)
	_, view := p.getJSON("/sharing")
	if cs, _ := view["crew_sharing"].(map[string]any); cs["share_repo"] != shareRepo {
		t.Errorf("crew_sharing = %v, want share_repo %s", cs, shareRepo)
	}
	if strings.Contains(slurp(filepath.Join(p.home, "config.json")), `"private"`) {
		t.Errorf("config.json records the visibility:\n%s", slurp(filepath.Join(p.home, "config.json")))
	}
}

func TestCreateRepoWithATakenNameOffersAPrivatePushableRepo(t *testing.T) {
	p := newPub(t, pubOpts{noRepo: true})
	p.gh.answer("POST", "user/repos", 422, nil, "gh: Repository creation failed. (HTTP 422)")
	p.gh.privateShareRepo()
	status, out := p.post("/sharing/repo", map[string]any{"name": "admiral-crew"})
	wantStatus(t, "taken", status, 409, out)
	if out["use_existing"] != shareRepo {
		t.Errorf("use_existing = %v, want %s", out["use_existing"], shareRepo)
	}
}

func TestCreateRepoWithATakenNameThatTheAccountCannotSeeKeepsTheNameTakenAnswer(t *testing.T) {
	p := newPub(t, pubOpts{noRepo: true})
	p.gh.answer("POST", "user/repos", 422, nil, "gh: Repository creation failed. (HTTP 422)")
	p.gh.ok("GET", "user", map[string]any{"login": "octocat"})
	p.gh.answer("GET", "repos/"+shareRepo, 404, nil, ghNotFound)
	status, out := p.post("/sharing/repo", map[string]any{"name": "admiral-crew"})
	wantStatus(t, "taken", status, 409, out)
	wantText(t, "taken", out, "GitHub refused to create")
	wantNoCode(t, "taken", out)
	if _, has := out["use_existing"]; has {
		t.Errorf("use_existing offered for a repo the account can't see: %v", out)
	}
}

func TestAPrivateShareRepoGetsTheSameBytesMessageAndPlainGitHubLinksAsAPublicOne(t *testing.T) {
	var bytes, message [2]string
	for i, private := range []bool{false, true} {
		p := newPub(t, pubOpts{})
		if private {
			p.readyPrivate("bosun", "Do the work.")
		} else {
			p.ready("bosun", "Do the work.")
		}
		p.gh.commitOK("PUT", "bosun")
		status, pv := p.preview("bosun")
		wantStatus(t, "preview", status, 200, pv)
		status, out := p.share("bosun", pv, nil)
		wantStatus(t, "share", status, 200, out)
		if want := "https://github.com/" + shareRepo + "/commit/" + commitSHA; str(out, "commit_url") != want {
			t.Errorf("commit_url = %q, want %s", str(out, "commit_url"), want)
		}
		bytes[i], message[i] = committed(t, p, "bosun")
		if strings.Contains(p.homeText(), `"private"`) {
			t.Errorf("private=%v: a sidecar or config.json records the visibility", private)
		}
	}
	if bytes[0] != bytes[1] || message[0] != message[1] || bytes[0] == "" {
		t.Errorf("a private repo got %q %q, a public one %q %q", bytes[1], message[1], bytes[0], message[0])
	}
}

func TestGetAndDeletePublishedWorkOnAPrivateShareRepoWithoutAVisibilityCheck(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.gh.privateShareRepo()
	p.gh.file("bosun", "published bytes\n")
	p.gh.commitOK("DELETE", "bosun")
	status, out := p.getJSON("/published/bosun")
	wantStatus(t, "GET published", status, 200, out)
	if str(out, "bytes") != "published bytes\n" || str(out, "blob") != blobOf("bosun") {
		t.Errorf("GET published = %v", out)
	}
	status, text := p.do("DELETE", p.base()+"/published/bosun", map[string]any{"blob": str(out, "blob"), "repo": str(out, "repo")})
	if status != 200 {
		t.Fatalf("DELETE published: %d %s", status, text)
	}
	if n := len(p.gh.find("GET", "repos/"+shareRepo)); n != 0 {
		t.Errorf("%d repo reads, want none: Unpublish checks no visibility", n)
	}
}

// --- the privacy checks and the secret scan are the same ---

func TestAPrivatePublishRefusesWhatAPublicOneRefuses(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.readyPrivate("bosun", "Read "+p.home+"/secrets.txt first.")
	status, out := p.preview("bosun")
	wantStatus(t, "path", status, 422, out)
	wantText(t, "path", out, "line")
	wantNoWrites(t, p)

	p = newPub(t, pubOpts{})
	p.readyPrivate("bosun", "Token "+decoy+" here.")
	canonical := hexSum(string(crewapi.CanonicalMemberBytes(resolvedMember(t, readFile(t, filepath.Join(p.crewDir(), "bosun.md"))))))
	status, text := p.do("POST", p.base()+"/members/bosun/share", map[string]any{"sha256": canonical, "repo": shareRepo, "private": true})
	out = parse(t, text)
	wantStatus(t, "hit", status, 422, out)
	wantText(t, "hit", out, "decoy-secret", "line")
	if strings.Contains(text, decoy) {
		t.Error("the response holds the secret")
	}
	wantNoWrites(t, p)
}

func TestAPrivatePublishStillRefusesTheScanOptOutWhenGitleaksIsUsable(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.readyPrivate("bosun", "Do the work.")
	status, pv := p.preview("bosun")
	wantStatus(t, "preview", status, 200, pv)
	status, out := p.share("bosun", pv, map[string]any{"skip_secret_scan": true})
	wantStatus(t, "opt-out", status, 400, out)
	wantNoWrites(t, p)
}

func TestTheTokenNeverReachesAResponseAFileOrTheLogOnAPrivatePublishOrANoAccessAnswer(t *testing.T) {
	logs := captureLog(t)
	p := newPub(t, pubOpts{})
	p.readyPrivate("bosun", "Do the work.")
	p.gh.commitOK("PUT", "bosun")
	status, text := p.do("POST", p.base()+"/members/bosun/share/preview", map[string]any{})
	if status != 200 {
		t.Fatalf("preview: %d %s", status, text)
	}
	wantNoToken(t, "the preview", text)
	status, text = p.do("POST", p.base()+"/members/bosun/share", map[string]any{"sha256": str(parse(t, text), "sha256"), "repo": shareRepo, "private": true})
	if status != 200 {
		t.Fatalf("share: %d %s", status, text)
	}
	wantNoToken(t, "the share answer", text)
	puts := p.gh.find("PUT", memberPath("bosun"))
	if len(puts) != 1 {
		t.Fatalf("PUT calls = %+v", p.gh.writes())
	}
	wantNoToken(t, "the committed body", puts[0].Stdin)

	n := newPub(t, pubOpts{})
	n.member("bosun", "Do the work.")
	n.gh.ok("GET", "user", map[string]any{"login": "octocat"})
	n.gh.answer("GET", "repos/"+shareRepo, 404, nil, ghNotFound)
	status, text = n.do("POST", n.base()+"/members/bosun/share/preview", map[string]any{})
	if status != 403 {
		t.Fatalf("no-access preview: %d %s", status, text)
	}
	wantNoToken(t, "the no-access answer", text)
	wantNoToken(t, "the fleet's files", p.homeText(), n.homeText())
	wantNoToken(t, "the log", logs.String())
}

// --- the setup view ---

func TestTheSharingViewOfAPrivateRepoCarriesPrivateAndNoPublic(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.gh.privateShareRepo()
	p.gh.published("bosun")
	status, out := p.getJSON("/sharing")
	wantStatus(t, "sharing", status, 200, out)
	repo, _ := out["repo"].(map[string]any)
	if repo["exists"] != true || repo["private"] != true || repo["pushable"] != true || repo["archived"] != false {
		t.Errorf("repo = %v", repo)
	}
	if _, has := repo["public"]; has {
		t.Errorf("repo = %v, want no public key", repo)
	}
}

func TestTheSharingViewOfASavedRepoThatAnswers404IsExistsFalseAndOnlyThat(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.gh.ok("GET", "user", map[string]any{"login": "octocat"})
	p.gh.noAccessTo(shareRepo)
	status, out := p.getJSON("/sharing")
	wantStatus(t, "sharing", status, 200, out)
	if want := map[string]any{"exists": false}; !reflect.DeepEqual(out["repo"], want) {
		t.Errorf("repo = %v, want exactly %v", out["repo"], want)
	}
	if got := str(out, "published_error"); got != noAccessText(shareRepo) {
		t.Errorf("published_error = %q, want the no-access text", got)
	}
	if names, _ := out["published"].([]any); len(names) != 0 {
		t.Errorf("published = %v, want none", names)
	}
}

// --- create ---

func TestCreateRepoSendsPrivateAsChosenAndTheREADMEIsTheSameEitherWay(t *testing.T) {
	var readmes [3]string
	for i, c := range []struct {
		name string
		body map[string]any
		want bool
	}{
		{"absent", map[string]any{"name": "admiral-crew"}, false},
		{"false", map[string]any{"name": "admiral-crew", "private": false}, false},
		{"true", map[string]any{"name": "admiral-crew", "private": true}, true},
	} {
		p := newPub(t, pubOpts{noRepo: true})
		p.createAnswers()
		status, out := p.post("/sharing/repo", c.body)
		wantStatus(t, c.name, status, 200, out)
		w := p.gh.writes()
		if len(w) != 2 {
			t.Fatalf("%s: writes = %+v, want the create and the README", c.name, w)
		}
		if got := putBody(t, w[0])["private"]; got != c.want {
			t.Errorf("%s: private sent as %v, want %v", c.name, got, c.want)
		}
		readmes[i] = decodeContent(t, putBody(t, w[1]))
	}
	if readmes[0] != readmes[1] || readmes[1] != readmes[2] {
		t.Errorf("README differs by visibility:\n%q\n%q\n%q", readmes[0], readmes[1], readmes[2])
	}
}

func TestCreateRepoWithAPrivateThatIsNotABoolIs400NamingItAndCreatesNothing(t *testing.T) {
	for _, bad := range []any{"yes", "true", 1, []string{"x"}} {
		p := newPub(t, pubOpts{noRepo: true})
		p.createAnswers()
		status, out := p.post("/sharing/repo", map[string]any{"name": "admiral-crew", "private": bad})
		wantStatus(t, "private", status, 400, out)
		wantText(t, "private", out, "private")
		wantNoWrites(t, p)
	}
}

func TestAPreviewOfTheREADMEIgnoresPrivateWhateverItsValue(t *testing.T) {
	var readmes []string
	for _, v := range []any{nil, true, false, "garbage", 7} {
		p := newPub(t, pubOpts{noRepo: true})
		body := map[string]any{"preview": true, "owner": "octocat", "name": "admiral-crew"}
		if v != nil {
			body["private"] = v
		}
		status, out := p.post("/sharing/repo", body)
		wantStatus(t, "readme preview", status, 200, out)
		readmes = append(readmes, str(out, "readme"))
		wantNoWrites(t, p)
	}
	for _, r := range readmes {
		if r == "" || r != readmes[0] {
			t.Errorf("readme = %q, want %q for every value", r, readmes[0])
		}
	}
}

// --- the visibility recheck ---

func TestSharePreviewReportsTheVisibilityTheShareEchoes(t *testing.T) {
	for _, private := range []bool{false, true} {
		p := newPub(t, pubOpts{})
		if private {
			p.readyPrivate("bosun", "Do the work.")
		} else {
			p.ready("bosun", "Do the work.")
		}
		p.gh.commitOK("PUT", "bosun")
		status, pv := p.preview("bosun")
		wantStatus(t, "preview", status, 200, pv)
		if pv["private"] != private {
			t.Errorf("preview private = %v, want %v", pv["private"], private)
		}
		status, out := p.share("bosun", pv, nil)
		wantStatus(t, "share", status, 200, out)
	}
}

func TestShareIs409AndCommitsNothingWhenTheRepoIsNotTheVisibilityThePreviewSaw(t *testing.T) {
	for _, private := range []bool{false, true} {
		p := newPub(t, pubOpts{})
		if private {
			p.readyPrivate("bosun", "Do the work.")
		} else {
			p.ready("bosun", "Do the work.")
		}
		p.gh.commitOK("PUT", "bosun")
		_, pv := p.preview("bosun")
		pv["private"] = !private
		status, out := p.share("bosun", pv, nil)
		wantStatus(t, "share", status, 409, out)
		wantText(t, "share", out, "Preview it again.")
		wantNoWrites(t, p)
	}
}

func TestShareWithAMissingOrNonBoolPrivateIs400AndCommitsNothing(t *testing.T) {
	p := newPub(t, pubOpts{})
	p.ready("bosun", "Do the work.")
	p.gh.commitOK("PUT", "bosun")
	_, pv := p.preview("bosun")
	for name, private := range map[string]any{"missing": nil, "string": "false", "number": 0} {
		body := map[string]any{"sha256": pv["sha256"], "repo": pv["repo"], "replaces_blob": pv["replaces_blob"]}
		if name != "missing" {
			body["private"] = private
		}
		status, out := p.post("/members/bosun/share", body)
		wantStatus(t, name, status, 400, out)
		wantText(t, name, out, "private")
	}
	wantNoWrites(t, p)
}
