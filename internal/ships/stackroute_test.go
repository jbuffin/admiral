package ships

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

func TestStackShapesAnswerTheBarAndOnlyChainedPRsCanBeStacked(t *testing.T) {
	const fourth = "https://github.com/acme/widgets/pull/24"
	seven, nine := layerURL(7), layerURL(9)
	mixed := chainedDetails(2)
	mixed[fourth] = moor.Detail{Number: 24, State: "OPEN", Head: "layer-24", Base: "main", DefaultBranch: "main"}
	twoStacks := stackedDetails(2)
	for u, d := range twoStacks {
		st := *d.Stack
		st.Entries = []moor.DetailStackEntry{{Position: 1, Number: 7, URL: seven, State: "OPEN"}}
		st.Size = 1
		if u == nine {
			st.Number = 5
			st.Entries = []moor.DetailStackEntry{{Position: 1, Number: 9, URL: nine, State: "OPEN"}}
		}
		d.Stack = &st
		twoStacks[u] = d
	}
	oneOnDevelop := independentDetails(1)
	d := oneOnDevelop[seven]
	d.Base = "develop"
	oneOnDevelop[seven] = d
	oneStacked := stackedDetails(1)

	for name, c := range map[string]struct {
		prs     []string
		details map[string]moor.Detail
		shape   string
		number  int
		reason  string
	}{
		"already a stack on GitHub":              {[]string{seven, nine}, stackedDetails(2), "stacked", 4, "already a stack on GitHub"},
		"independent PRs":                        {[]string{seven, nine}, independentDetails(2), "independent", 0, "independent of each other"},
		"chained and independent mixed":          {[]string{seven, nine, fourth}, mixed, "mixed", 0, "neither a chain nor independent"},
		"one PR on the default branch":           {[]string{seven}, independentDetails(1), "independent", 0, "at least two"},
		"one PR on another branch":               {[]string{seven}, oneOnDevelop, "mixed", 0, "at least two"},
		"one PR in a one-entry stack on GitHub":  {[]string{seven}, oneStacked, "stacked", 4, "at least two"},
		"two stacks that are not the ship's PRs": {[]string{seven, nine}, twoStacks, "mixed", 0, "neither a chain nor independent"},
	} {
		t.Run(name, func(t *testing.T) {
			g := &pollGitHub{detail: c.details}
			f := mooredService(t, map[string]*pollGitHub{"work": g})
			session := f.moor(t, "Nelson", "work", c.prs...)

			rec := f.getStack(session)
			var got struct {
				Shape       string   `json:"shape"`
				StackNumber int      `json:"stack_number"`
				PRs         []string `json:"prs"`
			}
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Shape != c.shape || got.StackNumber != c.number || !slices.Equal(got.PRs, c.prs) {
				t.Errorf("GET stack = %d %s, want shape %s number %d over %v", rec.Code, rec.Body, c.shape, c.number, c.prs)
			}
			if c.number == 0 && strings.Contains(rec.Body.String(), "stack_number") {
				t.Errorf("GET stack = %s, want no stack_number: only a stacked shape has one", rec.Body)
			}

			rec = f.post("/api/ships/" + session + "/stack")
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), c.reason) {
				t.Errorf("POST stack = %d %s, want 409 saying %q", rec.Code, rec.Body, c.reason)
			}
			g.mu.Lock()
			linked := g.linked
			g.mu.Unlock()
			if len(linked) != 0 || len(g.mergeLog()) != 0 {
				t.Errorf("a refused stack ran gh stack: link %v, merge %v", linked, g.mergeLog())
			}
			if sh, ok := f.ship(t, "Nelson"); !ok || sh.Status != StatusMoored {
				t.Errorf("ship = %+v, want still moored", sh)
			}
		})
	}
}

func TestStackLinkOfAShipWithAPROnAnotherHostIsRefusedAndRunsNoLink(t *testing.T) {
	g := &pollGitHub{}
	chainedSevenNine(g)
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven, "https://attacker.example/acme/widgets/pull/22")
	rec := f.post("/api/ships/" + session + "/stack")
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "GitHub host") || len(g.linked) != 0 || len(g.detailLog()) != 0 {
		t.Errorf("POST stack = %d %s with %d link and %d detail calls, want 422 naming the host and none", rec.Code, rec.Body, len(g.linked), len(g.detailLog()))
	}
}

func TestStackLinkWhileAnotherLinkIsRunningIsRefused(t *testing.T) {
	g := &pollGitHub{linkBlock: make(chan struct{})}
	chainedSevenNine(g)
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven, prNine)
	first := make(chan int, 1)
	go func() { first <- f.post("/api/ships/" + session + "/stack").Code }()
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		g.mu.Lock()
		n := len(g.linked)
		g.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(end) {
			t.Fatal("the first link never reached gh")
		}
	}
	if rec := f.post("/api/ships/" + session + "/stack"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already under way") {
		t.Errorf("POST stack while a link runs = %d %s, want 409 saying already under way", rec.Code, rec.Body)
	}
	close(g.linkBlock)
	if code := <-first; code != http.StatusOK {
		t.Errorf("the first POST stack = %d, want 200", code)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.linked) != 1 {
		t.Errorf("link calls = %d, want one: the refused POST ran nothing", len(g.linked))
	}
}
