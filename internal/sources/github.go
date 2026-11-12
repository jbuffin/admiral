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
	"github.com/jbuffin/admiral/internal/ships"
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
      "description": "One custom view per GitHub search, after the built-in views: Assigned to me, Review requested, My PRs",
      "items": {
        "type": "object",
        "properties": {
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
}

// A failed view keeps its last good orders next to the error.
type ghView struct {
	orders []map[string]any
	err    string
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
	stopped  bool
}

func openGitHub(a openArgs) *githubConn {
	ctx, cancel := context.WithCancel(context.Background())
	g := &githubConn{openArgs: a, fleet: a.def.Fleet, gone: make(chan struct{}), ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), views: map[string]ghView{}}
	if f, ok := a.cfg().Fleet(a.def.Fleet); ok {
		g.env = f.Environ()
	} else {
		g.env = config.Fleet{}.Environ()
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
		list := make([]map[string]string, 0, len(g.settings.Views))
		for _, v := range g.settings.Views {
			list = append(list, map[string]string{"id": v.ID, "title": v.Title})
		}
		g.mu.Unlock()
		g.once.Do(func() { go g.work() }) // only now may the source send notifications
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
	case "orders.launched": // Admiral hides the order while its ship is at sea
		return json.RawMessage("{}"), nil
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "no such method: " + method}
}

func (g *githubConn) refreshNow() {
	select {
	case g.wake <- struct{}{}:
	default:
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
	g.settings = s
	g.client = github.NewClient(s.Host, s.Account, s.HotLabels, g.env)
	for _, v := range s.Views {
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

func (g *githubConn) work() {
	g.mu.Lock()
	every := time.Duration(g.settings.RefreshMinutes) * time.Minute
	g.mu.Unlock()
	for {
		g.cycle()
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
			views := g.settings.Views
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
	client, views := g.client, g.settings.Views
	g.mu.Unlock()
	router := github.RouterFromFleets(g.cfg().Fleets)
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
		g.mu.Lock()
		fresh := g.authMsg != signIn.Message
		g.authMsg = signIn.Message
		g.mu.Unlock()
		if fresh {
			g.notify("auth.required", map[string]string{"message": signIn.Message})
		}
		return
	case err != nil: // the token call hung: keep the sign-in state
		for _, v := range views {
			changed = g.store(v.ID, nil, err.Error()) || changed
		}
		return
	}
	g.mu.Lock()
	wasOut := g.authMsg != ""
	g.authMsg = ""
	g.mu.Unlock()
	if wasOut {
		g.notify("auth.ok", nil)
	}
	for _, v := range views {
		items, err := client.Search(g.ctx, token, v.Query)
		if g.ctx.Err() != nil {
			return
		}
		if err != nil {
			changed = g.store(v.ID, nil, err.Error()) || changed
			continue
		}
		orders := []map[string]any{}
		for _, it := range items {
			if router.Route(client.Host(), it.Repo) != g.fleet {
				continue
			}
			order := map[string]any{"id": it.ID, "title": it.Title, "text": it.Text, "url": it.URL,
				"repo": it.RepoURL, "hot": it.Hot, "labels": it.Labels, "sub_issues": it.SubIssues}
			if p := it.PR; p != nil {
				order["pr"] = OrderPR{State: p.State, Review: p.Review, Checks: p.Checks, Mergeable: p.Mergeable, Conflicts: p.Conflicts, Ready: p.Ready}
			}
			orders = append(orders, order)
		}
		changed = g.store(v.ID, orders, "") || changed
	}
}

func (g *githubConn) store(id string, orders []map[string]any, errMsg string) bool {
	g.mu.Lock()
	old := g.views[id]
	next := ghView{orders: orders, err: errMsg}
	if orders == nil {
		next.orders = old.orders
	}
	g.views[id] = next
	g.mu.Unlock()
	return !reflect.DeepEqual(old, next)
}

func claimKey(url string) string { k, _ := ships.ClaimKey(url); return k }

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
