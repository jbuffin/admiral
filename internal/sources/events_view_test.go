package sources

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSourcesListCarriesAGitHubSourcesEventCatalogOnceItHasOne(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	f := newServiceFixture(t, ghConfig(`"alpha":{"name":"Alpha","claude":{}}`,
		`"gh":{"plugin":"github","fleet":"alpha","settings":{"views":[{"title":"Bugs","query":"is:open label:bug"},{"title":"Mine","query":"author:@me"}]}},`+
			`"ghost":{"plugin":"no-such-plugin","fleet":"alpha"}`))

	list := func() map[string]map[string]json.RawMessage {
		t.Helper()
		code, body := f.do(http.MethodGet, "", nil)
		var all []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &all); code != http.StatusOK || err != nil {
			t.Fatalf("GET /api/sources = %d %s", code, body)
		}
		out := map[string]map[string]json.RawMessage{}
		for _, s := range all {
			var key string
			_ = json.Unmarshal(s["key"], &key)
			out[key] = s
		}
		return out
	}

	f.startSources()
	v := f.waitView("gh", "its event catalog", func(s SourceView) bool { return len(s.Events) > 0 })
	f.waitView("ghost", "listed", func(SourceView) bool { return true })

	if _, has := list()["ghost"]["events"]; has {
		t.Errorf("a source with no catalog carries events: %s", list()["ghost"]["events"])
	}
	var got []EventKind
	if err := json.Unmarshal(list()["gh"]["events"], &got); err != nil {
		t.Fatalf("events: %v", err)
	}
	want := []EventKind{{Kind: "review_requested", Title: "Review requested of you"}, {Kind: "assigned", Title: "Assigned to you"}, {Kind: "mentioned", Title: "You are mentioned"}}
	for _, p := range v.Views {
		want = append(want, EventKind{Kind: "new_in_view", Title: "New in " + p.Title, Panel: p.ID})
	}
	if len(v.Views) < 2 {
		t.Fatalf("the source's views = %+v, want the two the settings name", v.Views)
	}
	if len(got) != len(want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("events[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
