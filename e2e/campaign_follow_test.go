package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func campIssueURL(n int) string { return fmt.Sprintf("https://github.com/acme/widgets/issues/%d", n) }

// campNode is an issue as the GraphQL tree read answers it; kids are its sub-issues.
func campNode(n int, kids ...map[string]any) map[string]any {
	page := map[string]any{"hasNextPage": false, "endCursor": nil}
	nodes := []any{}
	for _, k := range kids {
		nodes = append(nodes, k)
	}
	return map[string]any{"__typename": "Issue", "number": n, "title": fmt.Sprintf("Issue %d", n), "url": campIssueURL(n),
		"state": "OPEN", "body": "", "repository": map[string]any{"nameWithOwner": "acme/widgets"},
		"labels": map[string]any{"nodes": []any{}, "pageInfo": page}, "assignees": map[string]any{"nodes": []any{}},
		"blockedBy": map[string]any{"nodes": []any{}, "pageInfo": page},
		"subIssues": map[string]any{"totalCount": len(kids), "nodes": nodes, "pageInfo": page}}
}

// cannTree answers a read of issue n with its node; the needle number=NN is distinct per issue (two digits).
func (g *fakeGH) cannTree(n int, node map[string]any) {
	g.t.Helper()
	b, err := json.Marshal(map[string]any{"data": map[string]any{"repository": map[string]any{"issueOrPullRequest": node}}})
	if err != nil {
		g.t.Fatal(err)
	}
	key := fmt.Sprintf("tree%d", n)
	g.write("search/"+key+".match", fmt.Sprintf("number=%d", n))
	g.write("search/"+key+".json", string(b))
}

type campJSON struct {
	ID    string `json:"id"`
	Ended bool   `json:"ended"`
	Root  *struct {
		Children []struct {
			Number int    `json:"number"`
			Status string `json:"status"`
			Ship   string `json:"ship"`
		} `json:"children"`
	} `json:"root"`
}

func (c campJSON) status(n int) (string, string) {
	if c.Root != nil {
		for _, ch := range c.Root.Children {
			if ch.Number == n {
				return ch.Status, ch.Ship
			}
		}
	}
	return "", ""
}

func (w *world) campaign(id string) campJSON {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/v1/campaigns/"+id, nil)
	if code != http.StatusOK {
		w.t.Fatalf("GET campaign %s = %d: %s", id, code, body)
	}
	var c campJSON
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		w.t.Fatalf("campaign JSON: %v\n%s", err, body)
	}
	return c
}

func (w *world) campaignLaunch(id string, child int) (int, string) {
	w.t.Helper()
	return send(w.t, http.MethodPost, w.in.LocalURL+"/api/v1/campaigns/"+id+"/launch", map[string]any{
		"child_url": campIssueURL(child), "harness": "claude", "dir": w.checkout("widgets")})
}

func TestCampaignFollowJourney(t *testing.T) {
	if testing.Short() {
		t.Skip("needs tmux")
	}
	t.Parallel()
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", "fake-token-default-1")
	w.checkoutOf(w.work, "widgets", "acme/widgets")
	g.cannTree(20, campNode(20, campNode(21), campNode(22)))
	g.cannTree(21, campNode(21))
	g.cannTree(22, campNode(22))
	g.search("partof", "Part of #20")
	g.write("search/partof.json", `{"data":{"search":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`)
	g.search("assigned", "assignee:@me", ghNode("github.com", "acme/widgets", 21, false, "Issue 21"))
	w.writeCue("wait go", "exit 0")
	writeConfig(t, w.home, ghConfig([]string{"alpha", w.ghFleet("Alpha", []string{w.work}, nil, nil)},
		rawSources(srcDef{Key: "gha", Plugin: "github", Name: "GitHub", Fleet: "alpha", Settings: map[string]any{"views": []any{}}})))
	w.start()
	w.waitGH("gha", 20*time.Second, "its view", func(s ghSrcJSON) bool { return s.State == "ok" })

	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/v1/campaigns", map[string]string{"reference": campIssueURL(20)})
	if code != http.StatusCreated {
		t.Fatalf("follow = %d, want 201: %s", code, body)
	}
	var c campJSON
	if err := json.Unmarshal([]byte(body), &c); err != nil || c.ID == "" {
		t.Fatalf("followed campaign: %v\n%s", err, body)
	}
	if st, _ := c.status(21); st != "ready" {
		t.Errorf("child 21 = %q after follow, want ready: %s", st, body)
	}
	if n := len(w.in.ships(t)); n != 0 {
		t.Fatalf("following launched %d ships, want none", n)
	}

	code, body = w.campaignLaunch(c.ID, 21)
	if code != http.StatusCreated {
		t.Fatalf("launch of child 21 = %d, want 201: %s", code, body)
	}
	ship := decodeShip(t, body)
	if st, held := w.campaign(c.ID).status(21); st != "underway" && held != ship.Name {
		t.Logf("campaign view of 21 right after launch: %s held by %q", st, held)
	}

	code, body = w.campaignLaunch(c.ID, 21)
	w.wantConflict(code, body, "second launch of child 21")
	if !strings.Contains(body, ship.Name) {
		t.Errorf("the 409 body %q does not name the holding ship %q", body, ship.Name)
	}
	code, body = w.launchFrom("alpha", "gha", "acme/widgets#21")
	w.wantConflict(code, body, "launch of the source order for the same issue")
	if !strings.Contains(body, ship.Name) {
		t.Errorf("the source order's 409 body %q does not name the holding ship %q", body, ship.Name)
	}

	w.release(ship.Session, "go")
	w.in.waitShipsList(t, 15*time.Second, "the ship sailed off", func(l []shipJSON) bool { return statusOf(l, ship.Session) == "" })
	deadline := time.Now().Add(15 * time.Second)
	for st, _ := w.campaign(c.ID).status(21); st != "ready"; st, _ = w.campaign(c.ID).status(21) {
		if time.Now().After(deadline) {
			t.Fatalf("child 21 is %q after its ship sailed off, want ready again", st)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if code, body = w.campaignLaunch(c.ID, 21); code != http.StatusCreated {
		t.Errorf("launch after the sail-off = %d, want 201: %s", code, body)
	}

	if code, body = send(t, http.MethodPost, w.in.LocalURL+"/api/v1/campaigns/"+c.ID+"/unfollow", nil); code != http.StatusOK {
		t.Fatalf("unfollow = %d: %s", code, body)
	}
	_, active := send(t, http.MethodGet, w.in.LocalURL+"/api/v1/campaigns?ended=false", nil)
	_, ended := send(t, http.MethodGet, w.in.LocalURL+"/api/v1/campaigns?ended=true", nil)
	if strings.Contains(active, c.ID) || !strings.Contains(ended, c.ID) {
		t.Errorf("ended=false lists %s; ended=true lists %s; want the campaign only under ended", active, ended)
	}

	for _, call := range g.calls() {
		if slices.ContainsFunc(call.Argv, func(a string) bool { return strings.Contains(a, "mutation") }) {
			t.Errorf("gh was asked for a mutation: %v", call.Argv)
		}
	}
	for _, r := range g.restCalls() {
		if r.Method != http.MethodGet {
			t.Errorf("gh made a REST write: %s %s", r.Method, r.Endpoint)
		}
	}
}
