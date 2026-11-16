package sources

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// refreshingOverHTTP is the refreshing field of source key in GET /api/sources; nil when the field is absent.
func refreshingOverHTTP(t *testing.T, f *serviceFixture, key string) *bool {
	t.Helper()
	code, body := f.do(http.MethodGet, "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/sources = %d %s", code, body)
	}
	var list []struct {
		Key        string `json:"key"`
		Refreshing *bool  `json:"refreshing"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("GET /api/sources: %v: %s", err, body)
	}
	for _, s := range list {
		if s.Key == key {
			return s.Refreshing
		}
	}
	t.Fatalf("GET /api/sources lists no source %q: %s", key, body)
	return nil
}

func TestGitHubSourceReportsRefreshingWhileACycleRuns(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	writeSearch(t, g, "bugs", "label:bug", searchNode("issues", 13, obj{"subIssuesSummary": obj{"total": 0}}))
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()

	f.waitView("gha", "its first pull", func(s SourceView) bool { return len(s.orders("bugs")) == 1 && !s.Refreshing })
	if r := refreshingOverHTTP(t, f, "gha"); r == nil || *r {
		t.Fatalf("at rest GET /api/sources refreshing = %v, want false", r)
	}

	hold := filepath.Join(g.dir, "search.hold")
	g.write("search.hold", "")
	before := g.searches()
	if code, body := f.do(http.MethodPost, "/gha/refresh", map[string]any{}); code != http.StatusOK {
		t.Fatalf("POST refresh = %d %s", code, body)
	}
	if r := refreshingOverHTTP(t, f, "gha"); r == nil || !*r {
		t.Fatalf("right after the refresh POST refreshing = %v, want true", r)
	}
	deadline := time.Now().Add(15 * time.Second)
	for g.searches() == before {
		if time.Now().After(deadline) {
			t.Fatal("the refresh never started a search")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if r := refreshingOverHTTP(t, f, "gha"); r == nil || !*r {
		t.Fatalf("while a search is held refreshing = %v, want true", r)
	}

	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	f.waitView("gha", "refreshing to end once the search is released", func(s SourceView) bool { return !s.Refreshing })
	if r := refreshingOverHTTP(t, f, "gha"); r == nil || *r {
		t.Fatalf("after the cycle GET /api/sources refreshing = %v, want false", r)
	}
}
