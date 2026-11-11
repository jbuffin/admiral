package sources

import (
	"fmt"
	"testing"

	"github.com/jbuffin/admiral/internal/ships"
)

const campaignFleet = `"alpha":{"name":"Alpha","claude":{}}`
const campaignSource = `"gha":{"plugin":"github","fleet":"alpha","settings":{"views":[{"title":"Bugs","query":"is:open label:bug"}]}}`

// writeIssues answers the Bugs view with the given issues, each {number, sub-issue total}.
func (g *fakeGH) writeIssues(items ...[2]int) {
	nodes := ""
	for i, it := range items {
		if i > 0 {
			nodes += ","
		}
		nodes += fmt.Sprintf(`{"number":%d,"title":"Issue %d","url":"https://github.com/acme/widgets/issues/%d","repository":{"nameWithOwner":"acme/widgets","url":"https://github.com/acme/widgets"},"labels":{"nodes":[]},"subIssuesSummary":{"total":%d}}`,
			it[0], it[0], it[0], it[1])
	}
	g.write("search/bugs.match", "label:bug")
	g.write("search/bugs.json", `{"data":{"search":{"nodes":[`+nodes+`]}}}`)
}

func TestGitHubOrdersCarryTheirSubIssueCount(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.writeIssues([2]int{12, 3}, [2]int{13, 0})
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()
	src := f.waitView("gha", "the bugs", func(s SourceView) bool { return len(s.orders("bugs")) == 2 })
	count := map[string]int{}
	for _, o := range src.view("bugs").Orders {
		count[o.ID] = o.SubIssues
	}
	if count["acme/widgets#12"] != 3 || count["acme/widgets#13"] != 0 {
		t.Errorf("sub-issue counts = %v, want 3 for #12 and 0 for #13", count)
	}
}

func TestViewHidesAnOrderWhoseIssueAShipHolds(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.writeIssues([2]int{12, 0}, [2]int{13, 0})
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	// A campaign ship carries the issue under its own source key, in another spelling of the URL.
	f.Svc.Ships = func() []ships.Ship {
		return []ships.Ship{{Name: "calm-otter", Status: ships.StatusUnderway, Source: "campaign", OrderID: "https://github.com/acme/widgets/issues/12", URL: "https://GitHub.com/acme/widgets/issues/12/"}}
	}
	f.startSources()
	src := f.waitView("gha", "the bugs", func(s SourceView) bool { return len(s.orders("bugs")) > 0 })
	if got := src.orders("bugs"); len(got) != 1 || got[0] != "acme/widgets#13" {
		t.Errorf("bugs view = %v, want only acme/widgets#13: a ship holds the issue of #12", got)
	}
}

func TestParseOrdersDropsASubIssueCountThatIsNotAPositiveInteger(t *testing.T) {
	raw := `[{"id":"a","title":"A","text":"a","sub_issues":"3"},{"id":"b","title":"B","text":"b","sub_issues":2.5},` +
		`{"id":"c","title":"C","text":"c","sub_issues":-1},{"id":"d","title":"D","text":"d","sub_issues":4}]`
	got, err := parseOrders([]byte(raw))
	if err != nil {
		t.Fatalf("parseOrders: %v; a bad sub_issues must not fail the list", err)
	}
	want := []int{0, 0, 0, 4}
	for i, o := range got {
		if o.SubIssues != want[i] {
			t.Errorf("order %s sub-issues = %d, want %d", o.ID, o.SubIssues, want[i])
		}
	}
}
