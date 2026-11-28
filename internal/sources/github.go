package sources

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/moor"
)

// The GitHub source runs inside Admiral, so it has no plugin folder.
const builtinGitHub = "github"

const builtinGitHubDescription = "Issues and pull requests from GitHub searches, through the gh CLI."

// The Octicons mark; its licence is LICENSE-octicons.
//
//go:embed github-icon.svg
var githubIcon []byte

var githubSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "host": {"type": "string", "title": "Host", "description": "The GitHub host. Default: github.com"},
    "account": {"type": "string", "title": "Account", "description": "The gh user to sign in as. Default: the host's active account"},
    "views": {
      "type": "array",
      "title": "Views",
      "description": "The built-in views (For you, Assigned to me, Review requested, My PRs) and one custom view per GitHub search, in tab order",
      "items": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "hidden": {"type": "boolean"},
          "title": {"type": "string", "title": "Title", "description": "The view's title"},
          "query": {"type": "string", "title": "Query", "description": "A GitHub search, as on github.com, e.g. repo:acme/widgets is:open label:bug"}
        }
      }
    },
    "hotLabels": {
      "type": "array",
      "title": "Hot labels",
      "description": "Labels that mark an order hot, in any case. Default: priority: high, P0, P1, urgent",
      "items": {"type": "string"}
    },
    "refreshMinutes": {"type": "integer", "title": "Refresh minutes", "description": "Minutes between searches. Default: 5"}
  },
  "additionalProperties": false
}`)

const (
	codeInvalidParams  = -32602
	codeMethodNotFound = -32601
	codeSourceError    = -32000
)

type openArgs struct {
	key         string
	def         config.Source
	cfg         func() *config.Config // the current config
	onNote      func(method string, params json.RawMessage)
	logf        func(format string, a ...any)
	setWarnings func([]string)
	// emit delivers an event this source saw itself; nil when nothing listens.
	emit func(Event) bool
	// conn is the last connection the source had, carried over a restart; setConn keeps a new one.
	conn    *connState
	setConn func(connState)
	// setRefreshing tells the runner the refreshing flag flipped so it can push; nil when nothing listens.
	setRefreshing func()
	// stackChecker tells whether a chain's or stack's top merges into its base; nil skips the check.
	stackChecker moor.StackChecker
}

// A failed view keeps its last good orders next to the error.
type ghView struct {
	orders []map[string]any
	err    string
	// answered is when the last successful search finished; zero before the first.
	answered time.Time
}

func (g *githubConn) answeredAt(panel string) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.views[panel].answered
}

// githubConn answers the plugin protocol in memory, so a runner drives it like a plugin.
type githubConn struct {
	openArgs
	fleet string
	env   []string

	gone   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	once   sync.Once // the worker starts at the first panels.list

	mu       sync.Mutex
	settings github.Settings
	client   *github.Client
	views    map[string]ghView
	authMsg  string // the auth.required message in force
	viewer   string // the signed-in account the last search ran as
	conn     connState
	stopped  bool
	// refreshing is true from a requested or started cycle until one ends with no wake queued.
	refreshing bool
	// kinds is, per event search, when it last answered and the latest event time already delivered or baselined per item.
	kinds map[string]*kindPoll
	// stacks is the chains' and stacks' whole-stack check answers.
	stacks stackAnswers
}

// clockSkew is how far GitHub's clock may run behind this machine's. An event stamped up to this long before the last
// poll started is still offered; emitted keeps it from being offered twice.
const clockSkew = 15 * time.Minute

type kindPoll struct {
	at      time.Time
	emitted map[string]time.Time
}

// workers counts the running work loops, which outlive stop; tests wait on it before they restore a github package limit.
var workers sync.WaitGroup

func openGitHub(a openArgs) *githubConn {
	ctx, cancel := context.WithCancel(context.Background())
	g := &githubConn{openArgs: a, fleet: a.def.Fleet, gone: make(chan struct{}), ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), views: map[string]ghView{}, kinds: map[string]*kindPoll{}}
	if f, ok := a.cfg().Fleet(a.def.Fleet); ok {
		g.env = f.Environ()
	} else {
		g.env = config.Fleet{}.Environ()
	}
	if a.conn != nil {
		g.conn = *a.conn
	}
	return g
}

func (g *githubConn) exited() <-chan struct{} { return g.gone }

func (g *githubConn) exitReason() string { return "the order source exited" }

func (g *githubConn) stop() {
	g.mu.Lock()
	if !g.stopped {
		g.stopped = true
		g.cancel()
		close(g.gone)
	}
	g.mu.Unlock()
}

func (g *githubConn) kill() { g.stop() }

// A view's search error comes back as an *rpcError, as a plugin's would.
func (g *githubConn) call(ctx context.Context, method string, params any, _ time.Duration) (json.RawMessage, error) {
	select {
	case <-g.gone:
		return nil, errors.New(g.exitReason())
	default:
	}
	switch method {
	case "initialize":
		return g.initialize(params)
	case "panels.list":
		g.mu.Lock()
		searched := g.settings.SearchViews()
		list := make([]map[string]string, 0, len(searched))
		for _, v := range searched {
			list = append(list, map[string]string{"id": v.ID, "title": v.Title})
		}
		g.mu.Unlock()
		g.once.Do(func() { // only now may the source send notifications
			workers.Add(1)
			go func() { defer workers.Done(); g.work() }()
		})
		return json.Marshal(list)
	case "orders.list":
		var p struct {
			Panel string `json:"panel"`
		}
		b, _ := json.Marshal(params)
		_ = json.Unmarshal(b, &p)
		g.mu.Lock()
		v, ok := g.views[p.Panel]
		g.mu.Unlock()
		switch {
		case !ok:
			return nil, &rpcError{Code: codeInvalidParams, Message: "no such view"}
		case v.err != "":
			return nil, &rpcError{Code: codeSourceError, Message: v.err}
		}
		return json.Marshal(v.orders)
	case "orders.refresh":
		g.refreshNow()
		return json.RawMessage("{}"), nil
	case "events.catalog":
		g.mu.Lock()
		kinds := []EventKind{{Kind: "review_requested", Title: "Review requested of you"}, {Kind: "assigned", Title: "Assigned to you"},
			{Kind: "mentioned", Title: "You are mentioned"}}
		g.mu.Unlock()
		return json.Marshal(kinds)
	case "orders.launched": // Admiral records the launch
		return json.RawMessage("{}"), nil
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "no such method: " + method}
}

// refreshNow raises the flag and queues the wake under one lock, so work never clears the flag while a wake is queued.
func (g *githubConn) refreshNow() {
	g.mu.Lock()
	changed := !g.refreshing
	g.refreshing = true
	select {
	case g.wake <- struct{}{}:
	default:
	}
	g.mu.Unlock()
	if changed {
		g.flipped()
	}
}

// githubSettings reads a source's settings; anything but an object reads as none.
func githubSettings(raw json.RawMessage) github.Settings {
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	_ = dec.Decode(&m)
	return github.ParseSettings(m)
}

// FleetGitHub is the client of the fleet's built-in GitHub source, the first when
// there are several, built as that source builds it; nil when the fleet has none.
func FleetGitHub(cfg *config.Config, fleet string) *github.Client {
	f, ok := cfg.Fleet(fleet)
	if !ok {
		return nil
	}
	for _, def := range cfg.SourceList {
		if def.Fleet == fleet && def.Plugin == builtinGitHub {
			s := githubSettings(def.Settings)
			return github.NewClient(s.Host, s.Account, s.HotLabels, f.Environ())
		}
	}
	return nil
}

func (g *githubConn) initialize(params any) (json.RawMessage, error) {
	var p struct {
		Settings json.RawMessage `json:"settings"`
	}
	b, _ := json.Marshal(params)
	_ = json.Unmarshal(b, &p)
	s := githubSettings(p.Settings)
	g.mu.Lock()
	if g.settings.Account != s.Account || g.settings.Host != s.Host {
		g.viewer = "" // the stack route must not check authorship against the old account's login
	}
	g.settings = s
	g.client = github.NewClient(s.Host, s.Account, s.HotLabels, g.env)
	for _, v := range s.SearchViews() {
		g.views[v.ID] = ghView{orders: []map[string]any{}}
	}
	g.mu.Unlock()
	return json.Marshal(map[string]any{"protocolVersion": protocolVersion, "capabilities": []string{"refresh"}, "settingsSchema": githubSchema})
}

func (g *githubConn) notify(method string, params any) {
	var raw json.RawMessage
	if params != nil {
		raw, _ = json.Marshal(params)
	}
	g.onNote(method, raw)
}

// Refreshing says a refresh is requested or running.
func (g *githubConn) Refreshing() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refreshing
}

func (g *githubConn) setRefreshing(on bool) {
	g.mu.Lock()
	changed := g.refreshing != on
	g.refreshing = on
	g.mu.Unlock()
	if changed {
		g.flipped()
	}
}

func (g *githubConn) flipped() {
	if g.openArgs.setRefreshing != nil {
		g.openArgs.setRefreshing()
	}
}

// endRefreshing clears the flag unless a wake is queued, which runs one more cycle with the flag still up.
func (g *githubConn) endRefreshing() {
	g.mu.Lock()
	changed := g.refreshing && len(g.wake) == 0
	if changed {
		g.refreshing = false
	}
	g.mu.Unlock()
	if changed {
		g.flipped()
	}
}

func (g *githubConn) work() {
	g.mu.Lock()
	every := time.Duration(g.settings.RefreshMinutes) * time.Minute
	g.mu.Unlock()
	for {
		g.setRefreshing(true)
		g.cycle()
		g.endRefreshing()
		timer := time.NewTimer(every)
		select {
		case <-timer.C:
		case <-g.wake:
			timer.Stop()
		case <-g.ctx.Done():
			timer.Stop()
			return
		}
	}
}

// A panic in a refresh must not take Admiral down: it shows as an error on every view.
func (g *githubConn) cycle() {
	defer func() {
		if p := recover(); p != nil {
			g.logf("the refresh panicked: %v", p)
			g.mu.Lock()
			views := g.settings.SearchViews()
			g.mu.Unlock()
			changed := false
			for _, v := range views {
				changed = g.store(v.ID, nil, fmt.Sprintf("Refresh failed: %v", p)) || changed
			}
			if changed {
				g.notify("orders.changed", nil)
			}
		}
	}()
	g.refresh()
}

// The router is built each cycle because a new clone changes no config; orders.changed goes out once, after every view.
func (g *githubConn) refresh() {
	g.mu.Lock()
	client, views := g.client, g.settings.SearchViews()
	g.mu.Unlock()
	router := github.RouterFromFleets(g.cfg())
	warnings := append([]string{}, router.Warnings(g.fleet)...)
	g.setWarnings(warnings)

	changed := false
	defer func() {
		if changed {
			g.notify("orders.changed", nil) // every view
		}
	}()
	token, err := client.Token(g.ctx)
	var signIn *github.SignInError
	switch {
	case g.ctx.Err() != nil:
		return
	case errors.As(err, &signIn):
		g.settle(g.ctx, err, nil, "", false)
		return
	case err != nil: // the token call hung: keep the sign-in state
		g.settle(g.ctx, err, nil, "", false)
		for _, v := range views {
			changed = g.store(v.ID, nil, err.Error()) || changed
		}
		return
	}
	viewer, answered, worst := "", false, error(nil)
	for _, v := range views {
		items, login, err := client.SearchViewer(g.ctx, token, v.Query)
		if g.ctx.Err() != nil {
			return
		}
		if err != nil {
			worst = worseRefusal(worst, err)
			changed = g.store(v.ID, nil, err.Error()) || changed
			continue
		}
		answered = true
		if login != "" {
			viewer = login
		}
		orders := []map[string]any{}
		byURL := map[string]map[string]any{}
		for _, it := range items {
			if router.Route(client.Host(), it.Repo) != g.fleet {
				continue
			}
			order := map[string]any{"id": it.ID, "title": it.Title, "text": it.Text, "url": it.URL,
				"repo": it.RepoURL, "hot": it.Hot, "labels": it.Labels, "sub_issues": it.SubIssues}
			if p := it.Parent; p != nil {
				order["parent"] = map[string]any{"id": p.ID, "title": p.Title, "url": p.URL, "repo": p.RepoURL}
			}
			if st := it.Stack; st != nil {
				order["stack"] = map[string]any{"number": st.Number, "url": st.URL, "position": st.Position, "size": st.Size}
			}
			if it.Author != "" {
				order["author"] = it.Author
			}
			if !it.OpenedAt.IsZero() {
				order["opened_at"] = it.OpenedAt.UTC().Format(time.RFC3339)
			}
			if it.Excerpt != "" {
				order["excerpt"] = it.Excerpt
			}
			if p := it.PR; p != nil {
				order["pr"] = OrderPR{State: p.State, Review: p.Review, Checks: p.Checks, Mergeable: p.Mergeable, Conflicts: p.Conflicts, Ready: p.Ready, Draft: &it.Draft,
					FailingCheckCount: it.FailingChecks}
				order["reviewed_since_push"] = it.ReviewedSincePush
				if !it.ReviewRequestedAt.IsZero() {
					order["review_requested_at"] = it.ReviewRequestedAt.UTC().Format(time.RFC3339)
				}
			}
			orders = append(orders, order)
			byURL[it.URL] = order
		}
		addChains(items, byURL, login)
		g.markStackConflicts(client, items, byURL)
		changed = g.store(v.ID, orders, "") || changed
	}
	if len(views) == 0 { // no search to carry the login
		login, err := client.Viewer(g.ctx, token)
		if g.ctx.Err() != nil {
			return
		}
		answered, worst, viewer = err == nil, err, login
	}
	if answered {
		worst = nil
	}
	if _, ok := g.settle(g.ctx, nil, worst, viewer, false); !ok || github.IsBadCredentials(worst) {
		return
	}
	if viewer != "" {
		g.mu.Lock()
		g.viewer = viewer
		g.mu.Unlock()
	}
	g.pollEvents(client, token, router, viewer)
}

// worseRefusal picks the refusal that says most about the connection: a rejected token, then a network failure, then any other.
func worseRefusal(have, err error) error {
	switch {
	case have == nil || github.IsBadCredentials(err):
		return err
	case github.IsBadCredentials(have):
		return have
	case github.IsUnreachable(err):
		return err
	}
	return have
}

// eventSearches are the searches behind the events that need no view.
var eventSearches = []struct{ kind, query string }{
	{"review_requested", "is:open is:pr review-requested:@me"},
	{"assigned", "is:open assignee:@me"},
	{"mentioned", "is:open mentions:@me"},
}

// unknownActor stands for a login the source could not attribute; it matches routines.UnknownLogin.
const unknownActor = "(unknown)"

// pollEvents emits an event for each item whose latest timeline event aimed at the viewer is newer than the search's
// previous answer; a failed search keeps its last poll. The first answer is a baseline and emits nothing. An answer
// is capped at the search's top items, so the diff of two answers can't tell a new item from one that rose into it;
// the timeline's own times can.
func (g *githubConn) pollEvents(client *github.Client, token string, router *github.Router, viewer string) {
	if g.emit == nil {
		return
	}
	for _, es := range eventSearches {
		started := time.Now()
		items, login, err := client.SearchActing(g.ctx, token, es.query, es.kind)
		if g.ctx.Err() != nil {
			return
		}
		if err != nil {
			g.logf("the %s search failed: %v", es.kind, err)
			continue
		}
		if login == "" {
			login = viewer
		}
		var routed []github.Item
		for _, it := range items {
			if router.Route(client.Host(), it.Repo) == g.fleet {
				routed = append(routed, it)
			}
		}
		g.mu.Lock()
		kp := g.kinds[es.kind]
		if kp == nil {
			baseline := map[string]time.Time{}
			for _, it := range routed {
				if !it.EventAt.After(started) { // one stamped after the poll began is still new to the next
					baseline[it.URL] = it.EventAt
				}
			}
			g.kinds[es.kind] = &kindPoll{at: started, emitted: baseline}
			g.mu.Unlock()
			continue
		}
		floor := kp.at.Add(-clockSkew)
		var due []github.Item
		keep := map[string]time.Time{}
		for url, at := range kp.emitted {
			if at.After(floor) { // an item that leaves the answer and comes back must not repeat a recent event
				keep[url] = at
			}
		}
		for _, it := range routed {
			keep[it.URL] = kp.emitted[it.URL]
			if it.EventAt.After(floor) && it.EventAt.After(kp.emitted[it.URL]) {
				due = append(due, it)
			}
		}
		kp.emitted = keep
		g.mu.Unlock()
		allDelivered := true
		for _, it := range due {
			actor := it.Actor
			if actor == "" { // a mention names no mentioner, and a ghost account has no login
				actor = unknownActor
			}
			if !g.emit(Event{Kind: es.kind, Ref: it.URL, Title: it.Title, Actor: actor, Author: it.Author, Viewer: login, Verified: true}) {
				allDelivered = false
				continue
			}
			g.mu.Lock()
			kp.emitted[it.URL] = it.EventAt
			g.mu.Unlock()
		}
		if allDelivered { // an undelivered event stays newer than the last poll, so the next poll offers it again
			g.mu.Lock()
			kp.at = started
			g.mu.Unlock()
		}
	}
}

// store keeps a view's answer; a nil orders is a failed search, which keeps the last orders and time. It reports a
// change, and a successful search is one even when the orders are the same, since its time moved.
func (g *githubConn) store(id string, orders []map[string]any, errMsg string) bool {
	g.mu.Lock()
	old := g.views[id]
	next := ghView{orders: orders, err: errMsg, answered: old.answered}
	if orders == nil {
		next.orders = old.orders
	} else {
		next.answered = time.Now()
	}
	g.views[id] = next
	g.mu.Unlock()
	return !reflect.DeepEqual(old, next)
}

// GitHubClient is the client of the first built-in GitHub source of a fleet, in config order, whose host is host
// (compared ignoring case). It reads the client now, so a settings change is seen.
func (s *Supervisor) GitHubClient(fleet, host string) (*github.Client, bool) {
	s.mu.Lock()
	var runners []*runner
	for _, k := range s.order {
		if r := s.runners[k]; r != nil {
			runners = append(runners, r)
		}
	}
	s.mu.Unlock()
	for _, r := range runners {
		r.mu.Lock()
		def, p := r.def, r.p
		r.mu.Unlock()
		g, ok := p.(*githubConn)
		if !ok || def.Plugin != builtinGitHub || def.Fleet != fleet {
			continue
		}
		g.mu.Lock()
		c := g.client
		g.mu.Unlock()
		if c != nil && strings.EqualFold(c.Host(), host) {
			return c, true
		}
	}
	return nil, false
}

// Viewer is the login the built-in GitHub source's searches ran as; empty before its first answer.
func (g *githubConn) Viewer() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.viewer
}

// addChains gives the view's pull requests that chain by branch a "chain" field; mine is true when every PR in the
// chain is by login, the account this view's search ran as.
func addChains(items []github.Item, orders map[string]map[string]any, login string) {
	var prs []moor.ChainPR
	authors := map[string]string{}
	for _, it := range items {
		if it.PR == nil || it.Head == "" || orders[it.URL] == nil {
			continue
		}
		prs = append(prs, moor.ChainPR{URL: it.URL, State: it.PR.State, Head: it.Head, Base: it.Base, DefaultBranch: it.DefaultBranch, Stacked: it.Stack != nil})
		authors[it.URL] = it.Author
	}
	for _, chain := range moor.Chains(prs) {
		mine := login != ""
		for _, p := range chain {
			mine = mine && strings.EqualFold(authors[p.URL], login)
		}
		for i, p := range chain {
			orders[p.URL]["chain"] = map[string]any{"id": chain[0].URL, "position": i + 1, "size": len(chain), "head": p.Head, "base": p.Base, "mine": mine}
		}
	}
}
