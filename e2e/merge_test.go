package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	Cwd  string // where gh ran
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
		cwd, _ := os.ReadFile(filepath.Join(d, "cwd"))
		c.Cwd = strings.TrimSpace(string(cwd))
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

// pageMergeToken reads the token from the app page's <meta>, the way the shell does.
func (w *world) pageMergeToken(t *testing.T) string {
	t.Helper()
	resp, err := client().Get(w.in.LocalURL + "/app/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	page, _ := io.ReadAll(resp.Body)
	m := regexp.MustCompile(`<meta name="admiral-merge-token" content="([0-9a-f]+)"`).FindSubmatch(page)
	if m == nil {
		t.Fatalf("the /app/ page has no merge token meta: %.300s", page)
	}
	return string(m[1])
}

// postMergeWith posts to the merge route with the token header as given; an empty token sends no header.
func (w *world) postMergeWith(t *testing.T, ship shipJSON, body any, token string) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, w.mergeURL(ship), bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Admiral-Merge-Token", token)
	}
	resp, err := client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func (w *world) postMerge(t *testing.T, ship shipJSON, body any) (int, string) {
	t.Helper()
	return w.postMergeWith(t, ship, body, w.pageMergeToken(t))
}

func (w *world) shipNamed(ship shipJSON) (shipJSON, bool) {
	sh, ok := byName(w.in.ships(w.t))[ship.Name]
	return sh, ok
}

func wantCode(t *testing.T, what string, want, code int, body string) {
	t.Helper()
	if code != want {
		t.Errorf("%s = %d, want %d: %s", what, code, want, body)
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

	code, body := w.postMerge(t, ship, map[string]any{})
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
	// No kick here: the 2-minute poll can't end the ship in time, so the server's own refresh after the merge does.
	w.in.waitShipsList(t, 20*time.Second, "the ship sailed off after the merge", func(l []shipJSON) bool { _, ok := byName(l)[ship.Name]; return !ok })
	if ev := lastOf(w.waitEvent(ship.Session, "unmoored", 1), "unmoored"); ev["reason"] != "merged" {
		t.Errorf("unmoored = %v, want reason merged", ev)
	}
	if n := len(g.mergeCalls()); n != 1 {
		t.Errorf("merge calls after the ship ended = %d, want still one", n)
	}
}

// stackView seeds what `gh stack view --json` prints: an open stack of these PRs, bottom first.
func (g *fakeGH) stackView(prs ...string) {
	g.t.Helper()
	branches := []map[string]any{}
	for i, u := range prs {
		n, _ := strconv.Atoi(u[strings.LastIndex(u, "/")+1:])
		branches = append(branches, map[string]any{"name": "layer-" + strconv.Itoa(i+1), "head": "abc" + strconv.Itoa(i), "base": "main",
			"isCurrent": i == len(prs)-1, "isMerged": false, "isQueued": false, "needsRebase": false, "futureBranchKey": []int{1},
			"pr": map[string]any{"number": n, "url": u, "state": "OPEN", "futurePRKey": "x"}})
	}
	b, _ := json.Marshal(map[string]any{"trunk": "main", "currentBranch": "layer-" + strconv.Itoa(len(prs)), "branches": branches, "futureTopKey": map[string]any{"a": 1}})
	g.write("stack_view.json", string(b)+"\n")
}

func (g *fakeGH) stackCalls(verb string) []ghCall {
	var out []ghCall
	for _, c := range g.calls() {
		if len(c.Argv) >= 2 && c.Argv[0] == "stack" && c.Argv[1] == verb {
			out = append(out, c)
		}
	}
	return out
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// readyStack moors a ship on these PRs, all ready, and waits until the server serves the stack ready.
func readyStack(t *testing.T, method string, prs ...string) (*world, *fakeGH, shipJSON) {
	t.Helper()
	w, g := mooring(t, "exit 0")
	g.defaultMergeMethod(method)
	for _, u := range prs {
		g.prReady(u)
	}
	g.stackView(prs...)
	w.start()
	ship := moorPRs(t, w, prs...)
	var rec struct {
		Checkout string `json:"checkout"`
	}
	if b, err := os.ReadFile(w.mooredFile(ship.Name)); err != nil {
		t.Fatalf("moored record of %s: %v", ship.Name, err)
	} else if err := json.Unmarshal(b, &rec); err != nil || realPath(t, rec.Checkout) != realPath(t, w.checkout("widgets")) {
		t.Fatalf("seeding: the moored record's checkout = %q (%v), want the widgets checkout", rec.Checkout, err)
	}
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the stack read ready", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return sh.StackReady && sh.ReadyUpTo == prs[len(prs)-1]
	})
	return w, g, ship
}

func stillMooredWithNoMerge(w *world, g *fakeGH, ship shipJSON) func() bool {
	return func() bool {
		sh, ok := w.shipNamed(ship)
		return len(g.mergeCalls()) == 0 && ok && sh.Status == "moored" && mergedEvents(w, ship) == 0
	}
}

func TestMergeAReadyStackRunsOneArglessGhStackMergeInTheCheckout(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "SQUASH", layer1, layer2)
	holdThat(t, "gh merged before anyone posted a merge", stillMooredWithNoMerge(w, g, ship))

	code, body := w.postMerge(t, ship, map[string]any{})
	if code != http.StatusAccepted {
		t.Fatalf("POST merge on a ready stack = %d, want 202: %s", code, body)
	}
	var got struct {
		Merged []string `json:"merged"`
		Method string   `json:"method"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || !slices.Equal(got.Merged, []string{layer1, layer2}) || got.Method != "squash" {
		t.Errorf("answer = %s, want both PRs merged with squash", body)
	}

	calls := g.mergeCalls()
	if len(calls) != 1 {
		t.Fatalf("merge calls = %v, want exactly one gh stack merge", calls)
	}
	c := calls[0]
	// No target: gh-stack merges the current branch's whole stack, and a bare number could name another stack.
	if len(c.Argv) != 5 || c.Argv[0] != "stack" || c.Argv[1] != "merge" || !c.has("--yes") || !c.has("--merge-method") || !c.has("squash") {
		t.Errorf("argv = %q, want gh stack merge --yes --merge-method squash and nothing else", c.Argv)
	}
	if want := realPath(t, w.checkout("widgets")); c.Cwd != want {
		t.Errorf("gh stack merge ran in %q, want the ship's checkout %q", c.Cwd, want)
	}
	if c.Env["GH_TOKEN"] != "fake-token-f" {
		t.Errorf("GH_TOKEN = %q, want the fleet's token", c.Env["GH_TOKEN"])
	}
	views := g.stackCalls("view")
	if len(views) == 0 || !views[0].has("--json") || views[0].Cwd != c.Cwd {
		t.Errorf("stack views = %v, want gh stack view --json in the same checkout before the merge", views)
	}
	var order []string
	for _, x := range g.calls() {
		if len(x.Argv) >= 2 && x.Argv[0] == "stack" {
			order = append(order, x.Argv[1])
		}
	}
	if !slices.Equal(order, []string{"view", "merge"}) {
		t.Errorf("gh stack calls in order = %v, want view then merge", order)
	}

	w.waitEvent(ship.Session, "merged", 1)
	for _, m := range w.outcomeLog() {
		if m["ship"] == ship.Session && m["event"] == "merged" {
			if f, _ := json.Marshal(m["prs"]); string(f) != `["`+layer1+`","`+layer2+`"]` {
				t.Errorf("merged outcome prs = %s, want both PRs", f)
			}
		}
	}
	w.in.waitShipsList(t, 20*time.Second, "the ship sailed off after the merge", func(l []shipJSON) bool { _, ok := byName(l)[ship.Name]; return !ok })
}

func TestMergeOfAStackWhoseCheckoutHoldsAnotherStackIsRefusedAndRunsNoMerge(t *testing.T) {
	t.Parallel()
	const other = "https://github.com/acme/widgets/pull/99"
	for what, view := range map[string][]string{
		"other PRs":       {"https://github.com/acme/widgets/pull/90", other},
		"an extra layer":  {layer1, layer2, layer3},
		"a missing layer": {layer1},
		"another order":   {layer2, layer1},
	} {
		t.Run(what, func(t *testing.T) {
			t.Parallel()
			w, g, ship := readyStack(t, "MERGE", layer1, layer2)
			g.stackView(view...)
			code, body := w.postMerge(t, ship, map[string]any{})
			wantCode(t, "POST merge on a stack the checkout does not hold", http.StatusConflict, code, body)
			if len(g.stackCalls("view")) == 0 {
				t.Errorf("the checkout's stack was never read, so the 409 came from elsewhere: %s", body)
			}
			holdThat(t, "gh stack merge ran, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))
		})
	}
}

// A layer without a pr is a layer that is not the ship's, so it is an extra layer.
func TestMergeOfAStackWhoseCheckoutHasALayerWithNoPRIsRefusedAndRunsNoMerge(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "MERGE", layer1, layer2)
	g.write("stack_view.json", `{"trunk":"main","branches":[{"name":"a","pr":{"number":1,"url":"`+layer1+`"}},{"name":"b","pr":{"number":2,"url":"`+layer2+`"}},{"name":"c"}]}`+"\n")
	code, body := w.postMerge(t, ship, map[string]any{})
	wantCode(t, "POST merge on a stack with a layer that has no PR", http.StatusConflict, code, body)
	holdThat(t, "gh stack merge ran, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))
}

func TestMergeOfAStackWhoseViewFailsIsRefusedAndRunsNoMerge(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "MERGE", layer1, layer2)
	g.write("stack_view.fail", "gh stack: not a stack\n")
	code, body := w.postMerge(t, ship, map[string]any{})
	if code < 400 {
		t.Fatalf("POST merge when gh stack view fails = %d, want a refusal: %s", code, body)
	}
	if len(g.stackCalls("view")) == 0 {
		t.Errorf("the checkout's stack was never read, so the refusal came from elsewhere: %s", body)
	}
	holdThat(t, "gh merged, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))

}

func TestMergeOfAStackWhoseCheckoutIsEmptyOrGoneIsRefusedAndRunsNothing(t *testing.T) {
	t.Parallel()
	for what, checkout := range map[string]func(w *world) string{
		"no checkout":      func(*world) string { return "" },
		"a missing folder": func(w *world) string { return filepath.Join(w.work, "gone") },
	} {
		t.Run(what, func(t *testing.T) {
			t.Parallel()
			w, g := mooring(t, "exit 0")
			g.prReady(layer1)
			g.prReady(layer2)
			g.stackView(layer1, layer2)
			b, _ := json.Marshal(map[string]any{"name": "Adrift", "session": "adrift", "fleet": "f", "harness": "claude",
				"checkout": checkout(w), "prs": []string{layer1, layer2}, "moored_at": time.Now().UTC()})
			if err := os.MkdirAll(filepath.Dir(w.mooredFile("adrift")), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(w.mooredFile("adrift"), b, 0o600); err != nil {
				t.Fatal(err)
			}
			w.start()
			w.in.waitShipsList(t, 20*time.Second, "the record moored", func(l []shipJSON) bool { return byName(l)["Adrift"].Status == "moored" })
			code, body := w.postMerge(t, shipJSON{Session: "adrift"}, map[string]any{})
			wantCode(t, "POST merge on a record whose checkout is unusable", http.StatusConflict, code, body)
			holdThat(t, "gh stack ran for a record with no usable checkout", func() bool {
				return len(g.mergeCalls()) == 0 && len(g.stackCalls("view")) == 0 && len(g.stackCalls("merge")) == 0
			})
		})
	}
}

func TestMergeOfAStackWithALayerNotReadyIsRefusedAndRunsNoMergeOfAnyKind(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "MERGE", layer1, layer2)
	// Layer 2 stops being ready after the last poll, while the checkout's stack still matches.
	g.prChips(layer2, "APPROVED", "PENDING", "MERGEABLE")
	code, body := w.postMerge(t, ship, map[string]any{})
	wantCode(t, "POST merge with layer 2 not ready", http.StatusConflict, code, body)
	holdThat(t, "gh merged something with a layer not ready, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))
}

func TestMergeOfAStackWithAPROnAnotherHostIsRefusedAndRunsNoGh(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prReady(layer1)
	const foreign = "https://attacker.example/acme/widgets/pull/22"
	b, _ := json.Marshal(map[string]any{"name": "Foreign", "session": "foreign", "fleet": "f", "harness": "claude",
		"checkout": w.checkout("widgets"), "prs": []string{layer1, foreign}, "moored_at": time.Now().UTC()})
	if err := os.MkdirAll(filepath.Dir(w.mooredFile("foreign")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.mooredFile("foreign"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	g.stackView(layer1, foreign)
	w.start()
	w.in.waitShipsList(t, 20*time.Second, "the foreign record moored", func(l []shipJSON) bool {
		return byName(l)["Foreign"].Status == "moored"
	})
	code, body := w.postMerge(t, shipJSON{Session: "foreign"}, map[string]any{})
	wantCode(t, "POST merge on a stack whose second PR is on another host", http.StatusUnprocessableEntity, code, body)
	if !strings.Contains(body, "GitHub host") {
		t.Errorf("body = %s, want the host refusal and not the old stack refusal", body)
	}
	holdThat(t, "gh merged a PR on another host", func() bool { return len(g.mergeCalls()) == 0 && len(g.stackCalls("merge")) == 0 })
}

func TestMergeOfAStackUpToALowerLayerIsRefusedAndTheTopLayerMergesTheWhole(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "REBASE", layer1, layer2, layer3)
	for _, up := range []string{layer1, layer2} {
		code, b := w.postMerge(t, ship, map[string]any{"upTo": up})
		wantCode(t, "POST merge up to "+up, http.StatusBadRequest, code, b)
		if !strings.Contains(b, "whole stack") {
			t.Errorf("body = %s, want it to say the whole stack merges", b)
		}
	}
	holdThat(t, "gh merged for an upTo below the top, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))

	code, b := w.postMerge(t, ship, map[string]any{"upTo": layer3})
	if code != http.StatusAccepted {
		t.Fatalf("POST merge up to the top layer = %d, want 202: %s", code, b)
	}
	calls := g.mergeCalls()
	if len(calls) != 1 || calls[0].Argv[0] != "stack" || len(calls[0].Argv) != 5 || !calls[0].has("rebase") {
		t.Errorf("merge calls = %v, want one argless gh stack merge --yes --merge-method rebase", calls)
	}
	if !strings.Contains(b, layer1) || !strings.Contains(b, layer2) || !strings.Contains(b, layer3) {
		t.Errorf("answer = %s, want all three PRs", b)
	}
}

func TestAFailedGhStackMergeReturnsItsMessageAndKeepsTheShipMoored(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "MERGE", layer1, layer2)
	g.write("stack_merge.fail", "gh stack: layer 2 has a conflict with its base\n")
	code, body := w.postMerge(t, ship, map[string]any{})
	if code != http.StatusBadGateway || !strings.Contains(body, "layer 2 has a conflict with its base") {
		t.Errorf("POST merge = %d %s, want 502 carrying gh's message", code, body)
	}
	if n := len(g.mergeCalls()); n != 1 {
		t.Errorf("merge calls = %d, want exactly the failed one", n)
	}
	holdThat(t, "the failed merge ended the ship or wrote a merged outcome", func() bool {
		sh, ok := w.shipNamed(ship)
		return ok && sh.Status == "moored" && mergedEvents(w, ship) == 0
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
	code, b := w.postMerge(t, ship, map[string]any{"upTo": "https://github.com/acme/widgets/pull/99"})
	wantCode(t, "POST merge up to a PR that is not its own", http.StatusBadRequest, code, b)
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
	code, body := w.postMerge(t, ship, map[string]any{})
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
	live := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	code, body := w.postMerge(t, live, map[string]any{})
	wantCode(t, "POST merge on a live ship that is not moored", http.StatusConflict, code, body)
	code, body = w.postMerge(t, shipJSON{Session: "no-such-ship"}, map[string]any{})
	wantCode(t, "POST merge on a ship that is not there", http.StatusNotFound, code, body)
	if calls := g.mergeCalls(); len(calls) != 0 {
		t.Errorf("a refused merge ran gh: %v", calls)
	}
}

func TestMergeOfAPRNamingAnotherHostIsRefusedAndRunsNoGh(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prReady(layer1)
	const foreign = "https://attacker.example/acme/widgets/pull/21"
	b, _ := json.Marshal(map[string]any{"name": "Foreign", "session": "foreign", "fleet": "f", "harness": "claude",
		"checkout": w.checkout("widgets"), "prs": []string{foreign}, "moored_at": time.Now().UTC()})
	if err := os.MkdirAll(filepath.Dir(w.mooredFile("foreign")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.mooredFile("foreign"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	w.start()
	w.in.waitShipsList(t, 20*time.Second, "the foreign record moored", func(l []shipJSON) bool {
		return byName(l)["Foreign"].Status == "moored"
	})
	code, body := w.postMerge(t, shipJSON{Session: "foreign"}, map[string]any{})
	wantCode(t, "POST merge on a PR on another host", http.StatusUnprocessableEntity, code, body)
	holdThat(t, "gh merged a PR on another host", func() bool { return len(g.mergeCalls()) == 0 })
}

func TestMergeRouteTokenAlsoGuardsAnEscapedSessionPath(t *testing.T) {
	t.Parallel()
	w, _ := mooring(t, "exit 0")
	w.start()
	req, _ := http.NewRequest(http.MethodPost, w.in.LocalURL+"/api/ships/a%2Fb/merge", strings.NewReader("{}"))
	resp, err := client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	wantCode(t, "POST merge on an escaped path with no token", http.StatusForbidden, resp.StatusCode, "")
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

	code, body := w.postMerge(t, ship, map[string]any{})
	if code != http.StatusBadGateway || !strings.Contains(body, "the base branch policy prohibits the merge") {
		t.Errorf("POST merge = %d %s, want 502 carrying gh's message", code, body)
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
	if code, body := w.postMerge(t, ship, map[string]any{}); code != http.StatusAccepted {
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

	token := w.pageMergeToken(t)
	first := make(chan int, 1)
	go func() { // not postMerge: it fails the test, which only the test's own goroutine may do
		req, _ := http.NewRequest(http.MethodPost, w.mergeURL(ship), strings.NewReader("{}"))
		req.Header.Set("X-Admiral-Merge-Token", token)
		resp, err := client().Do(req)
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
	code, body := w.postMerge(t, ship, map[string]any{})
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

func TestMergeWithoutTheShellsTokenIsRefusedAndRunsNoGh(t *testing.T) {
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

	real := w.pageMergeToken(t)
	for what, token := range map[string]string{
		"no header":                      "",
		"a wrong token":                  "0000000000000000000000000000000000000000000000000000000000000000",
		"the token with a character off": real[:len(real)-1] + "x",
	} {
		if code, b := w.postMergeWith(t, ship, map[string]any{}, token); code != http.StatusForbidden {
			t.Errorf("POST merge with %s = %d, want 403: %s", what, code, b)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, w.mergeURL(ship), strings.NewReader("{}"))
	req.Header.Set("X-Admiral-Merge-Token", real)
	req.Header.Set("Origin", "http://evil.example")
	if resp, err := client().Do(req); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("POST merge from another origin with the right token = %d, want 403", resp.StatusCode)
		}
	}
	holdThat(t, "gh merged without the token, or the ship left its mooring", func() bool {
		sh, ok := w.shipNamed(ship)
		return len(g.mergeCalls()) == 0 && ok && sh.Status == "moored"
	})
	if code, b := w.postMergeWith(t, ship, map[string]any{}, real); code != http.StatusAccepted {
		t.Errorf("POST merge with the page's token = %d, want 202: %s", code, b)
	}
}
