package sources

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/moor"
)

// stackCheckTimeout bounds one background check of a chain or stack.
const stackCheckTimeout = 2 * time.Minute

// stackGroup is a chain or a GitHub stack of the view that the whole-stack check can answer for.
type stackGroup struct {
	check moor.StackCheck
	urls  []string
	kind  string // the order field the answer goes on: "chain" or "stack"
}

func (gr stackGroup) key() string {
	c := gr.check
	return c.Host + "/" + c.Repo + "#" + strconv.Itoa(c.PR) + "@" + c.Head + ">" + c.Base
}

// stackAnswers is the orders-side check's memory: the last answer per group, the checks in flight, and the last failure logged per group.
type stackAnswers struct {
	answer   map[string]string // group key -> the base it conflicts with, "" for clean
	inflight map[string]bool
	logged   map[string]string
}

// markStackConflicts serves each checked group's last answer on its orders and starts a background check per group with none in flight,
// so the refresh never waits on git. A changed answer queues one more refresh. It only adds a problem: no checker, an error or a
// group the check cannot cover leaves the orders as GitHub answered them.
func (g *githubConn) markStackConflicts(client *github.Client, items []github.Item, orders map[string]map[string]any) {
	if g.stackChecker == nil {
		return
	}
	groups := stackGroups(client.Host(), g.fleet, items, orders)
	g.mu.Lock()
	if g.stacks.answer == nil {
		g.stacks = stackAnswers{answer: map[string]string{}, inflight: map[string]bool{}, logged: map[string]string{}}
	}
	live := map[string]bool{}
	var start []stackGroup
	for _, gr := range groups {
		k := gr.key()
		live[k] = true
		if base := g.stacks.answer[k]; base != "" {
			for _, u := range gr.urls {
				if m, ok := orders[u][gr.kind].(map[string]any); ok {
					m["conflicts_with_base"] = base
				}
			}
		}
		if !g.stacks.inflight[k] {
			g.stacks.inflight[k] = true
			start = append(start, gr)
		}
	}
	for k := range g.stacks.answer {
		if !live[k] {
			delete(g.stacks.answer, k)
		}
	}
	g.mu.Unlock()
	for _, gr := range start {
		go g.checkStackGroup(gr)
	}
}

func (g *githubConn) checkStackGroup(gr stackGroup) {
	ctx, cancel := context.WithTimeout(g.ctx, stackCheckTimeout)
	defer cancel()
	conflicts, err := g.stackChecker.Conflicts(ctx, gr.check)
	k := gr.key()
	logKey := gr.check.Repo + "#" + strconv.Itoa(gr.check.PR)
	base := ""
	if conflicts {
		base = gr.check.Base
	}
	g.mu.Lock()
	delete(g.stacks.inflight, k)
	prev, had := g.stacks.answer[k]
	if err == nil {
		g.stacks.answer[k] = base
		delete(g.stacks.logged, logKey)
	}
	logIt := err != nil && g.ctx.Err() == nil && g.stacks.logged[logKey] != err.Error()
	if logIt {
		g.stacks.logged[logKey] = err.Error()
	}
	g.mu.Unlock()
	if logIt {
		g.logf("the base check of the stack ending at %s cannot run: %v", logKey, err)
	}
	if err == nil && g.ctx.Err() == nil && (prev != base || (!had && base != "")) {
		g.refreshNow()
	}
}

// stackGroups lists the chains and GitHub stacks of the view that are worth a whole-stack check: two or more open layers, every one
// MERGEABLE (a layer that conflicts says so itself), and for a stack every layer between the lowest and highest in the view in the view too.
func stackGroups(host, fleet string, items []github.Item, orders map[string]map[string]any) []stackGroup {
	mergeable := func(its []github.Item) bool {
		for _, it := range its {
			if it.PR.State != "OPEN" || it.PR.Mergeable != "MERGEABLE" || it.HeadSHA == "" {
				return false
			}
		}
		return true
	}
	group := func(kind string, layers []github.Item) (stackGroup, bool) {
		if len(layers) < 2 || !mergeable(layers) || layers[0].Base == "" {
			return stackGroup{}, false
		}
		top := layers[len(layers)-1]
		_, repo, number, ok := moor.ParsePR(top.URL)
		n, err := strconv.Atoi(number)
		if !ok || err != nil {
			return stackGroup{}, false
		}
		gr := stackGroup{kind: kind, check: moor.StackCheck{Fleet: fleet, Host: host, Repo: repo, Base: layers[0].Base, PR: n, Head: top.HeadSHA}}
		for _, l := range layers {
			gr.urls = append(gr.urls, l.URL)
		}
		return gr, true
	}
	var out []stackGroup
	seen := map[string]bool{}
	for _, it := range items {
		if c, ok := orders[it.URL]["chain"].(map[string]any); ok {
			id, _ := c["id"].(string)
			if seen[id] {
				continue
			}
			seen[id] = true
			var layers []github.Item
			for _, other := range items {
				if oc, ok := orders[other.URL]["chain"].(map[string]any); ok && oc["id"] == id && other.PR != nil {
					layers = append(layers, other)
				}
			}
			slices.SortFunc(layers, func(a, b github.Item) int {
				return orders[a.URL]["chain"].(map[string]any)["position"].(int) - orders[b.URL]["chain"].(map[string]any)["position"].(int)
			})
			if gr, ok := group("chain", layers); ok {
				out = append(out, gr)
			}
		}
	}
	stacks := map[string][]github.Item{}
	var order []string
	for _, it := range items {
		if it.Stack == nil || it.PR == nil || it.PR.State != "OPEN" {
			continue
		}
		if _, ok := stacks[it.Stack.URL]; !ok {
			order = append(order, it.Stack.URL)
		}
		stacks[it.Stack.URL] = append(stacks[it.Stack.URL], it)
	}
	for _, u := range order {
		layers := stacks[u]
		slices.SortFunc(layers, func(a, b github.Item) int { return a.Stack.Position - b.Stack.Position })
		contiguous := true
		for i := 1; i < len(layers); i++ {
			contiguous = contiguous && layers[i].Stack.Position == layers[i-1].Stack.Position+1
		}
		if !contiguous {
			continue
		}
		if gr, ok := group("stack", layers); ok {
			out = append(out, gr)
		}
	}
	return out
}
