package sources

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
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

func TestGitHubSourceStopsRefreshingWhenACycleFails(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.write("search/bugs.match", "label:bug")
	g.write("search/bugs.json", "{}")
	g.fail("bugs")
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()

	f.waitView("gha", "a failed pull that has ended", func(s SourceView) bool { return s.view("bugs").Error != "" && !s.Refreshing })
	if r := refreshingOverHTTP(t, f, "gha"); r == nil || *r {
		t.Fatalf("after a failed cycle GET /api/sources refreshing = %v, want false", r)
	}
}

func TestARunnerIsToldWhenTheGitHubSourceStartsAndStopsRefreshing(t *testing.T) {
	var told atomic.Int64
	r := &runner{key: "gha", sid: 1, notify: func() { told.Add(1) }, cfg: func() *config.Config { return &config.Config{} }}
	c, err := r.open(1, config.Source{Key: "gha", Plugin: builtinGitHub, Fleet: "alpha"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := c.(*githubConn)
	g.refreshNow()
	if n := told.Load(); n != 1 {
		t.Fatalf("the runner was told %d times when refreshing turned true, want 1", n)
	}
	g.refreshNow()
	if n := told.Load(); n != 1 {
		t.Fatalf("a second refresh while refreshing told the runner again (%d), want 1", n)
	}
	<-g.wake // the cycle took the wake
	g.endRefreshing()
	if n := told.Load(); n != 2 {
		t.Fatalf("the runner was told %d times by the end of the refresh, want 2", n)
	}
}

func TestARefreshDuringAHeldCycleKeepsRefreshingUntilTheQueuedCycleEnds(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	writeSearch(t, g, "bugs", "label:bug", searchNode("issues", 13, obj{"subIssuesSummary": obj{"total": 0}}))
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()
	f.waitView("gha", "its first pull", func(s SourceView) bool { return len(s.orders("bugs")) == 1 && !s.Refreshing })

	g.write("search.hold", "")
	before := g.searches()
	for i := 0; i < 2; i++ {
		if code, body := f.do(http.MethodPost, "/gha/refresh", map[string]any{}); code != http.StatusOK {
			t.Fatalf("POST refresh = %d %s", code, body)
		}
		if i == 0 {
			deadline := time.Now().Add(15 * time.Second)
			for g.searches() == before {
				if time.Now().After(deadline) {
					t.Fatal("the first refresh never started a search")
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
	if err := os.Remove(filepath.Join(g.dir, "search.hold")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if r := refreshingOverHTTP(t, f, "gha"); r != nil && !*r {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refreshing never ended")
		}
		time.Sleep(time.Millisecond)
	}
	if n := g.searches() - before; n < 2 {
		t.Fatalf("refreshing read false after %d searches, want the queued cycle's too", n)
	}
}
