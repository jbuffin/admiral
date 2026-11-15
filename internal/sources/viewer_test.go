package sources

import (
	"encoding/json"
	"testing"
	"time"
)

func TestGitHubSourceServesTheViewersLoginOnTheSource(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.write("viewer", "fake-viewer\n")
	writeSearch(t, g, "bugs", "label:bug", searchNode("pull", 21, obj{}))
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()
	f.waitView("gha", "the bugs", func(s SourceView) bool { return len(s.orders("bugs")) == 1 })

	var got any
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		code, body := f.do("GET", "", nil)
		if code != 200 {
			t.Fatalf("GET /api/sources = %d: %s", code, body)
		}
		var list []obj
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatal(err)
		}
		for _, s := range list {
			if s["key"] == "gha" {
				got = s["viewer"]
			}
		}
		if got == "fake-viewer" {
			return
		}
	}
	t.Errorf("source gha viewer = %v, want fake-viewer", got)
}
