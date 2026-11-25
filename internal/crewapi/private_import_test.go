package crewapi_test

import (
	"reflect"
	"strings"
	"testing"
)

var privateScout = upstream{owner: "hubot", repo: "crew", name: "scout", text: memberFile("scout", ""), private: true}

func TestAPrivateSourceBrowsesPreviewsByRefAndByURLAndImportsLikeAPublicOne(t *testing.T) {
	p := newImp(t, impOpts{})
	u := privateScout
	u.tags = []string{"v1"}
	p.gh.seed(u)
	p.gh.upstreamListing("hubot/crew", map[string]string{"scout": u.blob()})

	status, out := p.getJSON("/sources/hubot/crew")
	wantStatus(t, "browse", status, 200, out)
	if rows, _ := out["members"].([]any); len(rows) != 1 || str(rows[0].(map[string]any), "name") != "scout" {
		t.Errorf("browse rows = %v", out["members"])
	}
	for _, ref := range []string{"hubot/crew/scout", "https://github.com/hubot/crew/blob/main/crew/scout.md"} {
		status, pv := p.importPreview(ref)
		wantStatus(t, ref, status, 200, pv)
		if str(pv, "bytes") != u.text || str(pv, "commit") != commitSHA {
			t.Errorf("%s: preview = %v", ref, pv)
		}
	}
	p.importOK("hubot/crew/scout", nil)
	m := p.rawMember("scout")
	if want := "https://github.com/hubot/crew/blob/" + commitSHA + "/crew/scout.md"; m["shared_url"] != want {
		t.Errorf("shared_url = %v, want %s", m["shared_url"], want)
	}
	if strings.Contains(p.homeText(), `"private"`) {
		t.Errorf("a sidecar or config.json records the visibility:\n%s", slurp(p.sidecarPath("scout")))
	}
	wantNoToken(t, "the fleet's files", p.homeText())
}

func TestAPrivateSourceIsCheckedAndSavedByPutSources(t *testing.T) {
	p := newImp(t, impOpts{})
	p.gh.ok("GET", "repos/a/b", privateRepo("a/b", false))
	p.gh.ok("GET", "repos/a/b/contents/crew", []map[string]any{{"name": "x.md", "path": "crew/x.md", "type": "file", "sha": blobOf("x")}})
	status, out := p.put("/sources", map[string]any{"sources": []string{"a/b"}})
	wantStatus(t, "sources", status, 200, out)
	if got := p.savedSources(); !reflect.DeepEqual(got, []string{"a/b"}) {
		t.Errorf("saved %v", got)
	}
}
