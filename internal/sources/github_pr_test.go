package sources

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

const prURL = "https://github.com/acme/widgets/pull/20"

// writePRs answers the Bugs view with one ready pull request and one issue.
func (g *fakeGH) writePRs() {
	pr := fmt.Sprintf(`{"number":20,"title":"My PR","url":%q,"repository":{"nameWithOwner":"acme/widgets","url":"https://github.com/acme/widgets"},"labels":{"nodes":[]},`+
		`"state":"OPEN","reviewDecision":"APPROVED","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","statusCheckRollup":{"state":"SUCCESS"}}`, prURL)
	issue := `{"number":13,"title":"An issue","url":"https://github.com/acme/widgets/issues/13","repository":{"nameWithOwner":"acme/widgets","url":"https://github.com/acme/widgets"},"labels":{"nodes":[]},"subIssuesSummary":{"total":0}}`
	g.write("search/bugs.match", "label:bug")
	g.write("search/bugs.json", `{"data":{"search":{"nodes":[`+pr+`,`+issue+`]}}}`)
}

// served is an order as the API sends it.
func served(t *testing.T, o Order) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestGitHubSourceServesAPullRequestOrderWithItsStateAndAnIssueWithout(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.writePRs()
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()
	src := f.waitView("gha", "the bugs", func(s SourceView) bool { return len(s.orders("bugs")) == 2 })

	for _, o := range src.view("bugs").Orders {
		pr, has := served(t, o)["pr"]
		switch o.ID {
		case "acme/widgets#20":
			var got map[string]any
			if !has || json.Unmarshal(pr, &got) != nil {
				t.Fatalf("the pull request order has no pr object: %+v", served(t, o))
			}
			want := map[string]any{"state": "OPEN", "review": "APPROVED", "checks": "SUCCESS", "mergeable": "MERGEABLE", "conflicts": false, "ready": true, "draft": false}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("pr = %v, want %v (a ship's prs[] fields, without the url)", got, want)
			}
		case "acme/widgets#13":
			if has {
				t.Errorf("the issue order carries pr %s, want none", pr)
			}
		}
	}
}

func TestGitHubSourceServesWhetherAPullRequestOrderIsADraft(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	node := func(n int, draft string) string {
		return fmt.Sprintf(`{"number":%d,"title":"PR %d","url":"https://github.com/acme/widgets/pull/%d","repository":{"nameWithOwner":"acme/widgets","url":"https://github.com/acme/widgets"},"labels":{"nodes":[]},`+
			`"state":"OPEN","isDraft":%s,"reviewDecision":null,"mergeable":"MERGEABLE","mergeStateStatus":"BLOCKED","statusCheckRollup":null}`, n, n, n, draft)
	}
	g.write("search/bugs.match", "label:bug")
	g.write("search/bugs.json", `{"data":{"search":{"nodes":[`+node(20, "true")+`,`+node(21, "false")+`]}}}`)
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()
	src := f.waitView("gha", "the bugs", func(s SourceView) bool { return len(s.orders("bugs")) == 2 })

	want := map[string]bool{"acme/widgets#20": true, "acme/widgets#21": false}
	for _, o := range src.view("bugs").Orders {
		var pr map[string]any
		if raw, has := served(t, o)["pr"]; !has || json.Unmarshal(raw, &pr) != nil {
			t.Fatalf("order %s has no pr object", o.ID)
		}
		if got, has := pr["draft"]; !has || got != want[o.ID] {
			t.Errorf("order %s pr.draft = %v (present %v), want %v", o.ID, got, has, want[o.ID])
		}
	}
}

func TestParseOrdersKeepsAGoodPRAndDropsAMalformedOneWithoutFailingTheList(t *testing.T) {
	raw := `[{"id":"good","title":"G","text":"g","pr":{"state":"OPEN","review":"APPROVED","checks":"SUCCESS","mergeable":"MERGEABLE","conflicts":false,"ready":true}},` +
		`{"id":"string","title":"S","text":"s","pr":"ready"},` +
		`{"id":"array","title":"A","text":"a","pr":[1]},` +
		`{"id":"typed","title":"T","text":"t","pr":{"state":5,"ready":"yes"}},` +
		`{"id":"after","title":"X","text":"x"}]`
	got, err := parseOrders([]byte(raw))
	if err != nil || len(got) != 5 {
		t.Fatalf("parseOrders = %d orders, %v; a bad pr must not fail the list", len(got), err)
	}
	var kept map[string]any
	if pr, ok := served(t, got[0])["pr"]; !ok || json.Unmarshal(pr, &kept) != nil || kept["state"] != "OPEN" || kept["ready"] != true || kept["review"] != "APPROVED" {
		t.Errorf("the good pr was not kept: %+v", served(t, got[0]))
	}
	if _, says := kept["draft"]; says {
		t.Errorf("a pr that does not say draft serves draft = %v, want the key left out", kept["draft"])
	}
	for _, o := range got[1:] {
		if pr, ok := served(t, o)["pr"]; ok {
			t.Errorf("order %s kept the malformed pr %s", o.ID, pr)
		}
	}
}

func TestParseOrdersKeepsADraftFlagTheSourceSends(t *testing.T) {
	got, err := parseOrders([]byte(`[{"id":"d","title":"D","text":"d","pr":{"state":"OPEN","draft":true}},{"id":"r","title":"R","text":"r","pr":{"state":"OPEN","draft":false}}]`))
	if err != nil || len(got) != 2 {
		t.Fatalf("parseOrders = %d orders, %v", len(got), err)
	}
	for i, want := range []bool{true, false} {
		var pr map[string]any
		if raw, ok := served(t, got[i])["pr"]; !ok || json.Unmarshal(raw, &pr) != nil || pr["draft"] != want {
			t.Errorf("order %s pr = %v, want draft %v", got[i].ID, pr, want)
		}
	}
}
