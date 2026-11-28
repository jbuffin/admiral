package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	g.search("partof", "Part of") // the tree read runs two Part of searches, with and without the repo
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
	var st, held string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if st, held = w.campaign(c.ID).status(21); st == "underway" && held == ship.Name {
			break
		}
	}
	if st != "underway" || held != ship.Name {
		t.Fatalf("campaign view of 21 after launch = %s held by %q, want underway held by %s", st, held, ship.Name)
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

	w.draftingShipsFollowTheirCampaign(t, c.ID)

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

// campLabeled puts labels on a campNode; campClosed closes one.
func campLabeled(n map[string]any, labels ...string) map[string]any {
	nodes := []any{}
	for _, l := range labels {
		nodes = append(nodes, map[string]any{"name": l})
	}
	n["labels"] = map[string]any{"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}
	return n
}

func campClosed(n map[string]any) map[string]any { n["state"] = "CLOSED"; return n }

// AdmiralBot works a followed campaign: the ready Without-you child launches in autopilot with no chat turn, the
// With-you child is left alone, and closing every issue ends the commitment with a card. Admiral writes nothing to GitHub.
func TestAdmiralBot_WorkOnACampaignLaunchesItsWithoutYouChildrenAndEndsWithIt(t *testing.T) {
	if testing.Short() {
		t.Skip("needs tmux")
	}
	t.Parallel()
	var g *fakeGH
	c := newBotWorld(t, botOpts{sources: func(w *world) string {
		g = w.newFakeGH()
		g.token("", "fake-token-chat")
		return rawSourcesIn(chatFleet, srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Settings: map[string]any{"views": []any{}}})
	}})
	w := c.world
	// only the chat's fleet holds the checkout, so the campaign routes to it
	cfgRaw, err := os.ReadFile(configPath(w.home))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		t.Fatal(err)
	}
	shipRoots := filepath.Join(w.root, "ship-roots")
	if err := os.MkdirAll(shipRoots, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg["fleets"].(map[string]any)["ship"].(map[string]any)["search_roots"] = []string{shipRoots}
	out, _ := json.Marshal(cfg)
	writeConfig(t, w.home, string(out))
	w.checkoutOf(w.work, "widgets", "acme/widgets")
	w.proveGuard(chatFleet)
	// the chat fleet's claude dock keeps its skills under CLAUDE_CONFIG_DIR, not under HOME
	skillDir := filepath.Join(c.cfgDir, "skills", "commodore")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: commodore\ndescription: a fake skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	build := func(closed bool) {
		a, b, root := campLabeled(campNode(21), "ready-for-agent"), campNode(22), campNode(20)
		if closed {
			a, b, root = campClosed(a), campClosed(b), campClosed(root)
		}
		root["subIssues"] = map[string]any{"totalCount": 2, "nodes": []any{a, b}, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}
		g.cannTree(20, root)
		g.cannTree(21, a)
		g.cannTree(22, b)
	}
	build(false)
	g.search("partof", "Part of")
	g.write("search/partof.json", `{"data":{"search":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`)
	w.writeCue("mcp work", "wait go", "exit 0")
	w.extra = append(w.extra, "ADMIRAL_CAMPAIGN_PAGE_FLOOR=200ms")
	w.start()
	c.start()

	typed := c.stdin()
	r := c.mcpCall("work", "work_on_campaign", map[string]any{"reference": "acme/widgets#20", "harness": "claude"})
	if r.IsError || !strings.Contains(r.text(), "acme/widgets#20") {
		t.Fatalf("work_on_campaign = %+v, want the commitment quoted back", r)
	}

	code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/chat/"+chatFleet+"/commitments", nil)
	var listed struct {
		Commitments []struct {
			Text    string
			Action  string
			Trigger struct{ Kind, Subject string }
		}
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &listed) != nil || len(listed.Commitments) != 1 ||
		listed.Commitments[0].Trigger.Kind != "campaign" || listed.Commitments[0].Action != "work" {
		t.Fatalf("commitments = %d %s, want the one campaign commitment with action work", code, body)
	}

	var child shipJSON
	waitCond(t, "a ship launched for the Without-you child", func() bool {
		for _, s := range w.in.ships(t) {
			if s.Fleet == chatFleet && s.Via == "admiralbot" {
				child = s
				return true
			}
		}
		return false
	})

	if env := w.record(child.Session).env; env["ADMIRAL_AUTOPILOT"] != "1" {
		t.Errorf("the child's ship ADMIRAL_AUTOPILOT = %q, want 1: a commitment's launch is unattended", env["ADMIRAL_AUTOPILOT"])
	}
	holdThat(t, "a second ship launched: the With-you child is left to the engineer", func() bool {
		n := 0
		for _, s := range w.in.ships(t) {
			if s.Fleet == chatFleet {
				n++
			}
		}
		return n == 1
	})
	if got := c.stdin(); got != typed {
		t.Errorf("the chat was sent %q after the launch, want %q: a commitment's launch starts no model turn", got, typed)
	}

	build(true)
	cardOf := func() *chatMsg {
		for _, m := range c.session().Messages {
			var card struct{ Outcome string }
			if m.Kind == "commitment" && json.Unmarshal(m.Card, &card) == nil && card.Outcome == "ended" {
				return &m
			}
		}
		return nil
	}
	// opening the campaign page rereads the tree, no more often than the page floor: the store's own tick is five minutes
	campID := listed.Commitments[0].Trigger.Subject
	waitCond(t, "the card for the ended campaign", func() bool { w.campaign(campID); return cardOf() != nil })
	_, body = send(t, http.MethodGet, w.in.LocalURL+"/api/chat/"+chatFleet+"/commitments", nil)
	if strings.Contains(body, `"campaign"`) {
		t.Errorf("commitments after the campaign ended = %s, want none open", body)
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

const draftedEnded = "ended without filing a campaign"

// swapCue replaces the cue file with a new inode, so a ship still playing the old one reads on undisturbed.
func (w *world) swapCue(lines ...string) {
	w.t.Helper()
	next := w.cue + ".next"
	if err := os.WriteFile(next, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		w.t.Fatal(err)
	}
	if err := os.Rename(next, w.cue); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) writeCampaignRef(session, url string) {
	w.t.Helper()
	dir := filepath.Join(w.home, "ships", session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "campaign-ref"), []byte(url+"\n"), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// endedItemIDs lists the ids of the Admiral inbox items that say a drafting ship ended without filing.
func (w *world) endedItemIDs() []string {
	entries, _ := os.ReadDir(filepath.Join(w.home, "inbox", "_admiral"))
	var ids []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") { // the server writes an item under a temp name first
			continue
		}
		if b, err := os.ReadFile(filepath.Join(w.home, "inbox", "_admiral", e.Name())); err == nil && strings.Contains(string(b), draftedEnded) {
			ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return ids
}

func (w *world) launchDrafting() shipJSON {
	w.t.Helper()
	return w.launch(map[string]any{"fleet": "alpha", "harness": "claude", "dir": w.checkout("widgets"),
		"skill": customSkill("campaign-drafter"), "drafting": true, "text": "draft a campaign"})
}

func (w *world) waitDrafting(t *testing.T, session string, want bool) []shipJSON {
	t.Helper()
	return w.in.waitShipsList(t, 15*time.Second, "the drafting flag of "+session, func(l []shipJSON) bool {
		s, ok := shipIn(l, session)
		return ok && s.Drafting == want
	})
}

// A drafting ship that ends without filing leaves an Inbox item with its conversation; one that files is followed,
// even a campaign that was unfollowed, and a later ref to another issue is ignored with a line on the ship.
func (w *world) draftingShipsFollowTheirCampaign(t *testing.T, ended string) {
	t.Helper()
	skillDir := filepath.Join(w.userHom, ".claude", "skills", "campaign-drafter")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: campaign-drafter\ndescription: a fake drafting skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The first drafting ship sails off having filed nothing.
	w.swapCue("hook working", "wait end", "exit 0")
	quiet := w.launchDrafting()
	list := w.waitDrafting(t, quiet.Session, true)
	if s, _ := shipIn(list, quiet.Session); s.DraftingNotice != "" {
		t.Errorf("a ship that has written nothing shows the notice %q", s.DraftingNotice)
	}
	w.release(quiet.Session, "end")
	w.in.waitShipsList(t, 15*time.Second, "the drafting ship sailed off", func(l []shipJSON) bool { return statusOf(l, quiet.Session) == "" })
	waitCond(t, "an Inbox item that "+quiet.Name+" ended without filing", func() bool { return len(w.endedItemIDs()) == 1 })
	id := w.endedItemIDs()[0]
	if b, _ := os.ReadFile(filepath.Join(w.home, "inbox", "_admiral", id+".json")); !strings.Contains(string(b), quiet.Name) {
		t.Errorf("the item %s does not name the ship %s", b, quiet.Name)
	}
	if code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/inbox/_admiral/"+id+"/conversation", nil); code != http.StatusOK || !strings.Contains(body, `"state":"none"`) {
		// The fake writes no transcript in this fleet, so the page says none; that the binding reads ok is a unit check.
		t.Errorf("the item's conversation = %d %s, want 200 with state none", code, body)
	}
	if code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/inbox/_admiral/"+id+"/remove", nil); code != http.StatusOK {
		t.Fatalf("remove the item = %d: %s", code, body)
	}
	if code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/inbox/_admiral/"+id+"/conversation", nil); code != http.StatusNotFound {
		t.Errorf("the conversation of the removed item = %d %s, want 404", code, body)
	}

	// The second files a ref to the campaign the journey unfollowed, then names another issue.
	stop := `event Stop {"background_tasks":[]}`
	// Each Stop holds its needs_you, and the second turn works until the test has seen it: a turn end the poll cannot see is none.
	w.swapCue("hook working", "wait filed", stop, "wait again", "hook working", "wait seen", stop, "wait end", "exit 0")
	d := w.launchDrafting()
	w.waitDrafting(t, d.Session, true)
	w.writeCampaignRef(d.Session, campIssueURL(20))
	w.release(d.Session, "filed")
	waitCond(t, "the unfollowed campaign followed again", func() bool { return !w.campaign(ended).Ended })
	w.waitDrafting(t, d.Session, false)
	if _, err := os.Stat(filepath.Join(w.home, "ships", d.Session, "campaign-ref")); err == nil {
		t.Error("the ref file was left after it was followed")
	}

	w.writeCampaignRef(d.Session, campIssueURL(22))
	w.release(d.Session, "again")
	w.in.waitShipsList(t, 15*time.Second, "the ship working again", func(l []shipJSON) bool { return statusOf(l, d.Session) == "underway" })
	w.release(d.Session, "seen")
	w.in.waitShipsList(t, 15*time.Second, "a line on the ship about the other issue", func(l []shipJSON) bool {
		s, ok := shipIn(l, d.Session)
		return ok && (strings.Contains(s.DraftingNotice, "#22") || strings.Contains(s.DraftingNotice, "/issues/22"))
	})
	_, active := send(t, http.MethodGet, w.in.LocalURL+"/api/v1/campaigns?ended=false", nil)
	var open struct {
		Campaigns []campJSON `json:"campaigns"`
	}
	if err := json.Unmarshal([]byte(active), &open); err != nil || len(open.Campaigns) != 1 || open.Campaigns[0].ID != ended {
		t.Errorf("active campaigns = %s (%v), want only %s: the later ref to issue 22 is ignored", active, err, ended)
	}

	w.release(d.Session, "end")
	w.in.waitShipsList(t, 15*time.Second, "the drafting ship sailed off", func(l []shipJSON) bool { return statusOf(l, d.Session) == "" })
	holdThat(t, "a ship that filed has no ended-without-filing item", func() bool { return len(w.endedItemIDs()) == 0 })
}
