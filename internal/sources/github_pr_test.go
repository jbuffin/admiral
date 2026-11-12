package sources

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/ships"
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
			want := map[string]any{"state": "OPEN", "review": "APPROVED", "checks": "SUCCESS", "mergeable": "MERGEABLE", "conflicts": false, "ready": true}
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
	for _, o := range got[1:] {
		if pr, ok := served(t, o)["pr"]; ok {
			t.Errorf("order %s kept the malformed pr %s", o.ID, pr)
		}
	}
}

func TestViewKeepsAnOrderVisibleWhileItsShipIsMooredOnThatPRAndHidesItWhileTheShipIsLive(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.writePRs()
	launched := ships.Ship{Name: "calm-otter", Source: "gha", OrderID: "acme/widgets#20"}
	moored := func(prs ...string) ships.Ship {
		s := launched
		s.Status = ships.StatusMoored
		for _, u := range prs {
			s.PRs = append(s.PRs, moor.PR{URL: u, State: "OPEN"})
		}
		return s
	}
	live := launched
	live.Status = ships.StatusUnderway

	for _, c := range []struct {
		name    string
		ship    ships.Ship
		visible bool
	}{
		{"moored on the PR", moored(prURL), true},
		{"moored on the PR in another spelling", moored("https://GitHub.com/acme/widgets/pull/20/"), true},
		{"moored on another PR only", moored("https://github.com/acme/widgets/pull/99"), false},
		{"underway", live, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
			f.Svc.Ships = func() []ships.Ship { return []ships.Ship{c.ship} }
			f.startSources()
			src := f.waitView("gha", "the issue", func(s SourceView) bool {
				for _, id := range s.orders("bugs") {
					if id == "acme/widgets#13" {
						return true
					}
				}
				return false
			})
			has := false
			for _, id := range src.orders("bugs") {
				has = has || id == "acme/widgets#20"
			}
			if has != c.visible {
				t.Errorf("bugs view = %v, order acme/widgets#20 visible = %v, want %v", src.orders("bugs"), has, c.visible)
			}
		})
	}
}
