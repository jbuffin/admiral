package sources

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/github"
)

// builtinGitHub is the plugin name of the GitHub source, which runs in Admiral
// itself and has no plugin folder.
const builtinGitHub = "github"

const builtinGitHubDescription = "Issues and pull requests from GitHub searches, through the gh CLI."

// githubIcon is the Octicons mark (see LICENSE-octicons).
//
//go:embed github-icon.svg
var githubIcon []byte

// githubSchema is the settings form of the GitHub source.
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

// RPC error codes the built-in source answers with, as the plugin did.
const (
	codeInvalidParams  = -32602
	codeMethodNotFound = -32601
	codeSourceError    = -32000
)

// openArgs is what opening a source's connection needs.
type openArgs struct {
	key    string
	def    config.Source
	cfg    func() *config.Config // the current config
	onNote func(method string, params json.RawMessage)
	logf   func(format string, a ...any)
	// setWarnings shows the router's warnings for the source's fleet.
	setWarnings func([]string)
}

// ghView is what one view last showed: its orders, and the error that came
// with the last search if it failed (the orders are then the last good ones).
type ghView struct {
	orders []map[string]any
	err    string
}

// githubConn is the GitHub source as a conn: it answers the plugin protocol in
// memory, so a runner drives it as it drives a plugin.
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

// call answers one request. A view's last search error comes back as an
// *rpcError, as a plugin's would.
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

func (g *githubConn) initialize(params any) (json.RawMessage, error) {
	var p struct {
		Settings json.RawMessage `json:"settings"`
	}
	b, _ := json.Marshal(params)
	_ = json.Unmarshal(b, &p)
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(p.Settings))
	dec.UseNumber()
	_ = dec.Decode(&m) // anything but an object reads as no settings
	s := github.ParseSettings(m)
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

// work searches at start, on orders.refresh and every refreshMinutes.
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

// cycle runs one refresh. A panic in it is logged and shown as the source's
// error on every view, so it cannot take Admiral down; the next cycle tries again.
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

// refresh searches every view and, once all are done, tells the runner if any
// changed, so one cycle shows as one snapshot. It keeps only the items whose repo routes to this source's fleet, with a
// router built now: a new clone changes no config.
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
			orders = append(orders, map[string]any{"id": it.ID, "title": it.Title, "text": it.Text, "url": it.URL,
				"repo": it.RepoURL, "hot": it.Hot, "labels": it.Labels})
		}
		changed = g.store(v.ID, orders, "") || changed
	}
}

// store keeps a view's result (its last orders when there are none) and says
// whether the view changed.
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
