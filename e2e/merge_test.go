package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	layer1 = "https://github.com/acme/widgets/pull/21"
	layer2 = "https://github.com/acme/widgets/pull/22"
	layer3 = "https://github.com/acme/widgets/pull/23"
)

// mergeCall is one `gh pr merge` or `gh stack merge` the fake recorded.
type mergeCall struct {
	Argv []string
	Env  map[string]string
}

func (c mergeCall) has(arg string) bool {
	for _, a := range c.Argv {
		if a == arg {
			return true
		}
	}
	return false
}

func (g *fakeGH) mergeCalls() []mergeCall {
	g.t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(g.dir, "calls", "call.*"))
	var out []mergeCall
	for _, d := range dirs {
		raw, err := os.ReadFile(filepath.Join(d, "argv"))
		if err != nil || len(raw) == 0 {
			continue
		}
		argv := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
		if len(argv) < 2 || argv[1] != "merge" || (argv[0] != "pr" && argv[0] != "stack") {
			continue
		}
		c := mergeCall{Argv: argv, Env: map[string]string{}}
		env, _ := os.ReadFile(filepath.Join(d, "env"))
		for _, l := range strings.Split(string(env), "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				c.Env[k] = v
			}
		}
		out = append(out, c)
	}
	return out
}

// defaultMergeMethod steers the repository's default merge method the fake answers: MERGE, SQUASH or REBASE.
func (g *fakeGH) defaultMergeMethod(m string) { g.t.Helper(); g.write("default_merge_method", m+"\n") }

// prReady makes the poll read the PR as open, approved, green and mergeable.
func (g *fakeGH) prReady(url string) {
	g.t.Helper()
	g.prState(url, "OPEN")
	g.prChips(url, "APPROVED", "SUCCESS", "MERGEABLE")
}

func (g *fakeGH) defaultMethodQueries() int {
	n := 0
	for _, c := range g.calls() {
		if c.has("api", "graphql") && c.mentions("viewerDefaultMergeMethod") {
			n++
		}
	}
	return n
}

// checkPRMergeArgv is the one place the single-PR argv is pinned: gh pr merge URL, exactly the method's flag, and no bypass.
func checkPRMergeArgv(t *testing.T, c mergeCall, url, method string) {
	t.Helper()
	if len(c.Argv) < 3 || c.Argv[0] != "pr" || c.Argv[1] != "merge" || c.Argv[2] != url {
		t.Errorf("argv = %q, want gh pr merge %s ...", c.Argv, url)
	}
	for _, m := range []string{"merge", "squash", "rebase"} {
		if c.has("--"+m) != (m == method) {
			t.Errorf("argv = %q, want --%s and no other method flag", c.Argv, method)
		}
	}
	if c.has("--admin") || c.has("--auto") {
		t.Errorf("argv = %q: Admiral merges only what is ready, with no bypass and no queueing", c.Argv)
	}
}

// moorPRs launches a ship that moors on these PRs, in layer order, and waits for the poll to read them.
func moorPRs(t *testing.T, w *world, prs ...string) shipJSON {
	t.Helper()
	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	w.exitWith(ship, mooredReport(prs...))
	w.in.waitShipsList(t, 20*time.Second, "the ship moored", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "moored" })
	return ship
}

func (w *world) mergeURL(ship shipJSON) string {
	return w.in.LocalURL + "/api/ships/" + ship.Session + "/merge"
}

func (w *world) shipNamed(ship shipJSON) (shipJSON, bool) {
	sh, ok := byName(w.in.ships(w.t))[ship.Name]
	return sh, ok
}

func wantRefused(t *testing.T, what string, code int, body string) {
	t.Helper()
	if code < 400 || code >= 500 || code == http.StatusMethodNotAllowed {
		t.Errorf("%s = %d, want a 4xx refusal: %s", what, code, body)
	}
}

func mergedEvents(w *world, ship shipJSON) int {
	n := 0
	for _, m := range w.outcomeLog() {
		if m["ship"] == ship.Session && m["event"] == "merged" {
			n++
		}
	}
	return n
}

func TestMergeASinglePRRunsOneGhMergeWithTheDefaultMethod(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.defaultMergeMethod("SQUASH")
	g.prReady(layer1)
	w.start()
	ship := moorPRs(t, w, layer1)
	w.kickMoored()
	got := byName(w.in.waitShipsList(t, 20*time.Second, "the PR read ready and the default method served", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return len(sh.PRs) == 1 && sh.PRs[0].Ready && sh.MergeMethod == "squash"
	}))[ship.Name]
	if !got.StackReady || got.ReadyUpTo != layer1 {
		t.Errorf("ship = ready %v, up to %q; want a ready stack up to %s", got.StackReady, got.ReadyUpTo, layer1)
	}

	// A ready PR is merged only by the engineer's act: polls, however many, run no merge and ask the default method once.
	w.kickMoored()
	w.kickMoored()
	holdThat(t, "gh merged, or asked the default method again, before anyone posted a merge", func() bool {
		sh, ok := w.shipNamed(ship)
		return len(g.mergeCalls()) == 0 && g.defaultMethodQueries() == 1 && ok && sh.Status == "moored" && mergedEvents(w, ship) == 0
	})

	code, body := send(t, http.MethodPost, w.mergeURL(ship), map[string]any{})
	if code != http.StatusAccepted {
		t.Fatalf("POST merge = %d, want 202: %s", code, body)
	}
	calls := g.mergeCalls()
	if len(calls) != 1 {
		t.Fatalf("merge calls = %v, want exactly one", calls)
	}
	checkPRMergeArgv(t, calls[0], layer1, "squash")
	if calls[0].Env["GH_TOKEN"] != "fake-token-f" {
		t.Errorf("GH_TOKEN = %q, want the fleet's token", calls[0].Env["GH_TOKEN"])
	}

	w.waitEvent(ship.Session, "merged", 1)
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the ship sailed off after the merge", func(l []shipJSON) bool { _, ok := byName(l)[ship.Name]; return !ok })
	if ev := lastOf(w.waitEvent(ship.Session, "unmoored", 1), "unmoored"); ev["reason"] != "merged" {
		t.Errorf("unmoored = %v, want reason merged", ev)
	}
	if n := len(g.mergeCalls()); n != 1 {
		t.Errorf("merge calls after the ship ended = %d, want still one", n)
	}
}

// gh stack merge reads a bare number as a stack number before a PR number, so it could merge another stack; until a
// form that cannot be misread exists, a stack is refused and its PRs are merged on GitHub.
func TestMergeAStackIsRefusedAndRunsNoGh(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.defaultMergeMethod("REBASE")
	for _, u := range []string{layer1, layer2, layer3} {
		g.prReady(u)
	}
	w.start()
	ship := moorPRs(t, w, layer1, layer2, layer3)
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the stack read ready", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return sh.StackReady && sh.ReadyUpTo == layer3 && sh.MergeMethod == "rebase"
	})

	for what, body := range map[string]map[string]any{"the whole stack": {}, "up to layer 2": {"upTo": layer2}} {
		code, b := send(t, http.MethodPost, w.mergeURL(ship), body)
		if code != http.StatusUnprocessableEntity {
			t.Errorf("POST merge %s = %d, want 422: %s", what, code, b)
		}
	}
	holdThat(t, "gh merged a stack, or the ship left its mooring", func() bool {
		sh, ok := w.shipNamed(ship)
		return len(g.mergeCalls()) == 0 && ok && sh.Status == "moored" && mergedEvents(w, ship) == 0
	})
}

func TestAStackReadsReadyUpToTheHighestLayerWhoseLowerLayersAreReady(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prReady(layer1)
	g.prReady(layer2)
	g.prState(layer3, "OPEN")
	g.prChips(layer3, "APPROVED", "PENDING", "MERGEABLE")
	w.start()
	ship := moorPRs(t, w, layer1, layer2, layer3)
	w.kickMoored()
	got := byName(w.in.waitShipsList(t, 20*time.Second, "layers 1 and 2 read ready", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return len(sh.PRs) == 3 && sh.PRs[0].Ready && sh.PRs[1].Ready
	}))[ship.Name]
	if got.PRs[2].Ready || got.StackReady || got.ReadyUpTo != layer2 {
		t.Errorf("ship = layer 3 ready %v, stack ready %v, up to %q; want layer 3 not ready, the stack not ready, up to %s",
			got.PRs[2].Ready, got.StackReady, got.ReadyUpTo, layer2)
	}
	code, b := send(t, http.MethodPost, w.mergeURL(ship), map[string]any{"upTo": "https://github.com/acme/widgets/pull/99"})
	wantRefused(t, "POST merge up to a PR that is not its own", code, b)
	if calls := g.mergeCalls(); len(calls) != 0 {
		t.Fatalf("a refused merge ran gh: %v", calls)
	}
}

func TestMergeIsRefusedWhenGitHubNowSaysTheStackIsNotReady(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prReady(layer1)
	w.start()
	ship := moorPRs(t, w, layer1)
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the PR read ready", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return len(sh.PRs) == 1 && sh.PRs[0].Ready
	})

	// A reviewer asks for changes after the last poll: the served readiness is stale, so Admiral looks again before it runs gh.
	g.prChips(layer1, "CHANGES_REQUESTED", "SUCCESS", "MERGEABLE")
	code, body := send(t, http.MethodPost, w.mergeURL(ship), map[string]any{})
	if code != http.StatusConflict {
		t.Errorf("POST merge on a PR that stopped being ready = %d, want 409: %s", code, body)
	}
	if calls := g.mergeCalls(); len(calls) != 0 {
		t.Errorf("a refused merge ran gh: %v", calls)
	}
	if sh, ok := w.shipNamed(ship); !ok || sh.Status != "moored" {
		t.Errorf("ship = %+v, want still moored", sh)
	}
}

func TestMergeOfAShipThatIsNotMooredIsRefused(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	w.start()
	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/ships/no-such-ship/merge", map[string]any{})
	wantRefused(t, "POST merge on a ship that is not there", code, body)
	if calls := g.mergeCalls(); len(calls) != 0 {
		t.Errorf("a refused merge ran gh: %v", calls)
	}
}

func TestAFailedGhMergeReturnsItsMessageAndKeepsTheShipMoored(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prReady(layer1)
	g.write("pr_merge.fail", "GraphQL: Pull request is not mergeable: the base branch policy prohibits the merge\n")
	w.start()
	ship := moorPRs(t, w, layer1)
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the PR read ready", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return len(sh.PRs) == 1 && sh.PRs[0].Ready
	})

	code, body := send(t, http.MethodPost, w.mergeURL(ship), map[string]any{})
	if code < 400 || !strings.Contains(body, "the base branch policy prohibits the merge") {
		t.Errorf("POST merge = %d %s, want an error carrying gh's message", code, body)
	}
	calls := g.mergeCalls()
	if len(calls) != 1 {
		t.Fatalf("merge calls = %v, want exactly one", calls)
	}
	checkPRMergeArgv(t, calls[0], layer1, "merge")
	holdThat(t, "the failed merge ended the ship or wrote a merged outcome", func() bool {
		sh, ok := w.shipNamed(ship)
		return ok && sh.Status == "moored" && mergedEvents(w, ship) == 0 && exists(w.mooredFile(ship.Name))
	})

	// The failure frees the ship: the engineer can try again, and it goes through.
	if err := os.Remove(filepath.Join(g.dir, "pr_merge.fail")); err != nil {
		t.Fatal(err)
	}
	if code, body := send(t, http.MethodPost, w.mergeURL(ship), map[string]any{}); code != http.StatusAccepted {
		t.Errorf("POST merge after the failure = %d, want 202: %s", code, body)
	}
	if n := len(g.mergeCalls()); n != 2 {
		t.Errorf("merge calls = %d, want the failed one and the retry", n)
	}
}

func TestASecondMergeWhileOneIsRunningIsRefused(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prReady(layer1)
	g.write("pr_merge.sleep", "3\n")
	w.start()
	ship := moorPRs(t, w, layer1)
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the PR read ready", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return len(sh.PRs) == 1 && sh.PRs[0].Ready
	})

	first := make(chan int, 1)
	go func() { // not send: it fails the test, which only the test's own goroutine may do
		resp, err := http.Post(w.mergeURL(ship), "application/json", strings.NewReader("{}"))
		if err != nil {
			first <- 0
			return
		}
		resp.Body.Close()
		first <- resp.StatusCode
	}()
	for end := time.Now().Add(20 * time.Second); len(g.mergeCalls()) == 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the first merge never reached gh")
		}
	}
	code, body := send(t, http.MethodPost, w.mergeURL(ship), map[string]any{})
	if code != http.StatusConflict {
		t.Errorf("POST merge while one runs = %d, want 409: %s", code, body)
	}
	if code := <-first; code != http.StatusAccepted {
		t.Errorf("the first POST merge = %d, want 202", code)
	}
	if n := len(g.mergeCalls()); n != 1 {
		t.Errorf("merge calls = %d, want one", n)
	}
}
