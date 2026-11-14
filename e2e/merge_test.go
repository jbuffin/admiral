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
	"sort"
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
	// The folder names end in a random suffix, so the order of the calls is the order they were written.
	modTime := func(d string) time.Time {
		fi, err := os.Stat(filepath.Join(d, "argv"))
		if err != nil {
			return time.Time{}
		}
		return fi.ModTime()
	}
	sort.SliceStable(dirs, func(i, j int) bool { return modTime(dirs[i]).Before(modTime(dirs[j])) })
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
	// A single PR has no shape to check, so Merge reads no PR detail.
	for _, c := range g.calls() {
		if strings.Contains(strings.Join(c.Argv, " "), "isDraft") {
			t.Errorf("a single-PR merge read the PR detail: %q", c.Argv)
		}
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

// The detail fixture is one compact line in GitHub's shape, in this field order: the fake gh finds a PR's stack by the
// text `"stack":{"number":N,` and flips `"state":"OPEN"` when it merges.
type detailFixture struct {
	Number         int           `json:"number"`
	Title          string        `json:"title"`
	Body           string        `json:"body"`
	State          string        `json:"state"`
	IsDraft        bool          `json:"isDraft"`
	HeadRefName    string        `json:"headRefName"`
	BaseRefName    string        `json:"baseRefName"`
	BaseRepository baseRepoFix   `json:"baseRepository"`
	Stack          *stackFixture `json:"stack"`
}

type baseRepoFix struct {
	DefaultBranchRef struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
}

type stackFixture struct {
	Number      int    `json:"number"`
	Size        int    `json:"size"`
	BaseRefName string `json:"baseRefName"`
	Entries     struct {
		Nodes []stackEntryFix `json:"nodes"`
	} `json:"entries"`
}

type stackEntryFix struct {
	Position    int `json:"position"`
	PullRequest struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		State       string `json:"state"`
		HeadRefName string `json:"headRefName"`
	} `json:"pullRequest"`
}

func prNumber(url string) int {
	n, _ := strconv.Atoi(url[strings.LastIndex(url, "/")+1:])
	return n
}

// branchOf is the head branch the fixtures give a PR.
func branchOf(url string) string { return "branch-" + strconv.Itoa(prNumber(url)) }

// stackIn is GitHub's stack number n holding the PRs bottom to top, entries listed in the order given by position (callers shuffle by listing positions).
func stackIn(n int, positions map[string]int) *stackFixture {
	st := &stackFixture{Number: n, Size: len(positions), BaseRefName: "main"}
	for url, pos := range positions {
		var e stackEntryFix
		e.Position = pos
		e.PullRequest.Number, e.PullRequest.URL, e.PullRequest.State, e.PullRequest.HeadRefName = prNumber(url), url, "OPEN", branchOf(url)
		st.Entries.Nodes = append(st.Entries.Nodes, e)
	}
	sort.Slice(st.Entries.Nodes, func(i, j int) bool {
		return st.Entries.Nodes[i].PullRequest.Number > st.Entries.Nodes[j].PullRequest.Number
	})
	return st
}

// prDetail seeds what GitHub answers the detail query with for one PR: its branches, and the stack it sits in (nil for none). The default branch is main.
func (g *fakeGH) prDetail(url, head, base string, stack *stackFixture) {
	g.t.Helper()
	d := detailFixture{Number: prNumber(url), Title: "PR " + strconv.Itoa(prNumber(url)), State: "OPEN", HeadRefName: head, BaseRefName: base, Stack: stack}
	d.BaseRepository.DefaultBranchRef.Name = "main"
	b, err := json.Marshal(d)
	if err != nil {
		g.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(g.dir, "pr_detail"), 0o755); err != nil {
		g.t.Fatal(err)
	}
	g.write("pr_detail/"+strings.NewReplacer("/", "_", ":", "_").Replace(url), string(b)+"\n")
}

// githubStack seeds GitHub's stack number n of these PRs, bottom first: every PR's detail holds it, each PR based on the one below.
func (g *fakeGH) githubStack(n int, prs ...string) {
	g.t.Helper()
	pos := map[string]int{}
	for i, u := range prs {
		pos[u] = i + 1
	}
	for i, u := range prs {
		base := "main"
		if i > 0 {
			base = branchOf(prs[i-1])
		}
		g.prDetail(u, branchOf(u), base, stackIn(n, pos))
	}
}

// chainedPRs seeds PRs that are chained and in no GitHub stack: each is based on the head branch of the one below.
func (g *fakeGH) chainedPRs(prs ...string) {
	g.t.Helper()
	for i, u := range prs {
		base := "main"
		if i > 0 {
			base = branchOf(prs[i-1])
		}
		g.prDetail(u, branchOf(u), base, nil)
	}
}

// independentPRs seeds PRs that are each based on the default branch and in no GitHub stack.
func (g *fakeGH) independentPRs(prs ...string) {
	g.t.Helper()
	for _, u := range prs {
		g.prDetail(u, branchOf(u), "main", nil)
	}
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

// anyStackCalls counts every gh stack call, whatever its verb.
func (g *fakeGH) anyStackCalls() int {
	n := 0
	for _, c := range g.calls() {
		if len(c.Argv) >= 1 && c.Argv[0] == "stack" {
			n++
		}
	}
	return n
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// seedMoored is the state every moored-ship test starts from: the PRs ready, the details of GitHub's answer seeded by seed, the ship moored on prs.
func seedMoored(t *testing.T, method string, seed func(g *fakeGH), prs ...string) (*world, *fakeGH, shipJSON) {
	t.Helper()
	w, g := mooring(t, "exit 0")
	g.defaultMergeMethod(method)
	for _, u := range prs {
		g.prReady(u)
	}
	seed(g)
	w.start()
	ship := moorPRs(t, w, prs...)
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "every PR read ready", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return sh.StackReady && sh.ReadyUpTo == prs[len(prs)-1]
	})
	return w, g, ship
}

// readyStack moors a ship on these PRs, all ready and held by GitHub stack 4, and waits until the server serves them ready.
func readyStack(t *testing.T, method string, prs ...string) (*world, *fakeGH, shipJSON) {
	t.Helper()
	return seedMoored(t, method, func(g *fakeGH) { g.githubStack(4, prs...) }, prs...)
}

// notAboutACheckout fails a refusal that blames the ship's checkout: GitHub's answer is the only record of a stack now.
func notAboutACheckout(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(strings.ToLower(body), "checkout") {
		t.Errorf("refusal = %s, want one from GitHub's answer and not about a checkout", body)
	}
}

func stillMooredWithNoMerge(w *world, g *fakeGH, ship shipJSON) func() bool {
	return func() bool {
		sh, ok := w.shipNamed(ship)
		return len(g.mergeCalls()) == 0 && ok && sh.Status == "moored" && mergedEvents(w, ship) == 0
	}
}

func TestMergeAGitHubStackRunsOneGhStackMergeByNumberWithNoCheckout(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "SQUASH", layer1, layer2)
	holdThat(t, "gh merged before anyone posted a merge", stillMooredWithNoMerge(w, g, ship))
	// GitHub is the only record of the stack: the ship's directory is not needed, and nothing reads it.
	var rec struct {
		Checkout string `json:"checkout"`
	}
	if b, err := os.ReadFile(w.mooredFile(ship.Name)); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(b, &rec); err != nil || rec.Checkout == "" {
		t.Fatalf("moored record = %s (%v), want a checkout", b, err)
	}
	if err := os.RemoveAll(rec.Checkout); err != nil {
		t.Fatal(err)
	}

	code, body := w.postMerge(t, ship, map[string]any{})
	if code != http.StatusAccepted {
		t.Fatalf("POST merge on a GitHub stack = %d, want 202: %s", code, body)
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
	// GitHub's stack number (4), never a PR number, a branch or anything from the request.
	if want := []string{"stack", "merge", "4", "--yes", "--merge-method", "squash"}; !slices.Equal(c.Argv, want) {
		t.Errorf("argv = %q, want %q", c.Argv, want)
	}
	if c.Cwd == "" || c.Cwd == realPath(t, w.work) || strings.HasPrefix(c.Cwd, realPath(t, w.work)+string(filepath.Separator)) {
		t.Errorf("gh stack merge ran in %q, want a folder of its own and not a ship's checkout under %q", c.Cwd, w.work)
	}
	if exists(c.Cwd) {
		t.Errorf("the folder gh stack merge ran in, %q, is still there: it should be removed afterwards", c.Cwd)
	}
	if c.Env["GH_TOKEN"] != "fake-token-f" {
		t.Errorf("GH_TOKEN = %q, want the fleet's token", c.Env["GH_TOKEN"])
	}
	// The repo and host are pinned, so no git config of any directory can steer gh-stack.
	if c.Env["GH_HOST"] != "github.com" || c.Env["GH_REPO"] != "github.com/acme/widgets" {
		t.Errorf("stack merge env GH_HOST=%q GH_REPO=%q, want github.com and github.com/acme/widgets", c.Env["GH_HOST"], c.Env["GH_REPO"])
	}
	if c.Env["GIT_CONFIG_COUNT"] != "3" || c.Env["GIT_CONFIG_KEY_0"] != "core.fsmonitor" || c.Env["GIT_CONFIG_KEY_1"] != "core.hooksPath" || c.Env["GIT_CONFIG_KEY_2"] != "core.sshCommand" {
		t.Errorf("stack merge env = %v, want fsmonitor, hooksPath and sshCommand blanked", c.Env)
	}
	if n := g.anyStackCalls(); n != 1 {
		t.Errorf("gh stack calls = %d, want only the merge: the stack is read through GitHub's detail query, not gh stack view", n)
	}

	w.waitEvent(ship.Session, "merged", 1)
	for _, m := range w.outcomeLog() {
		if m["ship"] == ship.Session && m["event"] == "merged" {
			if f, _ := json.Marshal(m["prs"]); string(f) != `["`+layer1+`","`+layer2+`"]` {
				t.Errorf("merged outcome prs = %s, want both PRs", f)
			}
		}
	}
	w.in.waitShipsList(t, 20*time.Second, "the ship left its mooring after the merge", func(l []shipJSON) bool { return byName(l)[ship.Name].Status != "moored" })
}

// An unusable checkout is no longer a reason to refuse: the stack is merged from GitHub's number.
func TestMergeOfAGitHubStackNeedsNoCheckoutInTheRecord(t *testing.T) {
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
			g.githubStack(4, layer1, layer2)
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
			w.kickMoored()
			w.in.waitShipsList(t, 20*time.Second, "the stack read ready", func(l []shipJSON) bool { return byName(l)["Adrift"].StackReady })
			code, body := w.postMerge(t, shipJSON{Session: "adrift"}, map[string]any{})
			wantCode(t, "POST merge on a stacked record with an unusable checkout", http.StatusAccepted, code, body)
			if calls := g.mergeCalls(); len(calls) != 1 || !slices.Equal(calls[0].Argv, []string{"stack", "merge", "4", "--yes", "--merge-method", "merge"}) {
				t.Errorf("merge calls = %v, want one gh stack merge 4", calls)
			}
		})
	}
}

// What GitHub holds is what decides: a stack that is not exactly the ship's PRs is refused, whatever the ship recorded.
func TestMergeOfShipsWhoseStackOnGitHubIsNotExactlyTheirPRsIsRefusedAndRunsNoMerge(t *testing.T) {
	t.Parallel()
	const other = "https://github.com/acme/widgets/pull/99"
	for what, seed := range map[string]func(g *fakeGH){
		"the stack holds one more PR": func(g *fakeGH) {
			g.githubStack(4, layer1, layer2, other)
		},
		"the stack holds only one of the two": func(g *fakeGH) {
			g.githubStack(4, layer1)
			g.prDetail(layer2, branchOf(layer2), branchOf(layer1), nil)
		},
		"the PRs sit in two different stacks": func(g *fakeGH) {
			g.githubStack(4, layer1)
			g.githubStack(5, layer2)
		},
		"one PR is in the stack and the other is not": func(g *fakeGH) {
			g.githubStack(4, layer1, layer2)
			g.prDetail(layer2, branchOf(layer2), branchOf(layer1), nil)
		},
		"part chained and part independent": func(g *fakeGH) {
			g.chainedPRs(layer1, layer2)
			g.prDetail(layer3, branchOf(layer3), "main", nil)
		},
		"based on a branch that is neither the default nor a sibling's": func(g *fakeGH) {
			g.independentPRs(layer1, layer2)
			g.prDetail(layer2, branchOf(layer2), "develop", nil)
		},
	} {
		t.Run(what, func(t *testing.T) {
			t.Parallel()
			prs := []string{layer1, layer2}
			if what == "part chained and part independent" {
				prs = []string{layer1, layer2, layer3}
			}
			w, g, ship := seedMoored(t, "MERGE", seed, prs...)
			code, body := w.postMerge(t, ship, map[string]any{})
			wantCode(t, "POST merge on a ship whose PRs are mixed", http.StatusConflict, code, body)
			notAboutACheckout(t, body)
			holdThat(t, "gh merged, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))
			if n := g.anyStackCalls(); n != 0 {
				t.Errorf("gh stack calls = %d, want none", n)
			}
		})
	}
}

func TestMergeOfChainedPRsGitHubDoesNotHoldAsAStackIsRefusedAndRunsNoMerge(t *testing.T) {
	t.Parallel()
	w, g, ship := seedMoored(t, "MERGE", func(g *fakeGH) { g.chainedPRs(layer1, layer2) }, layer1, layer2)
	code, body := w.postMerge(t, ship, map[string]any{})
	wantCode(t, "POST merge on chained PRs that are not a GitHub stack", http.StatusConflict, code, body)
	notAboutACheckout(t, body)
	if !strings.Contains(body, "stack it on GitHub") {
		t.Errorf("body = %s, want it to tell the engineer to stack it on GitHub first", body)
	}
	holdThat(t, "gh merged, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))
	if n := g.anyStackCalls(); n != 0 {
		t.Errorf("gh stack calls = %d, want none", n)
	}
}

func TestMergeOfAGitHubStackNamingAPRIsRefused(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "MERGE", layer1, layer2)
	code, body := w.postMerge(t, ship, map[string]any{"pr": layer1})
	wantCode(t, "POST merge naming one PR of a stack", http.StatusConflict, code, body)
	notAboutACheckout(t, body)
	holdThat(t, "gh merged, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))
	if n := g.anyStackCalls(); n != 0 {
		t.Errorf("gh stack calls = %d, want none: the refusal comes from GitHub's answer, not from gh", n)
	}
}

func TestMergeWhenGitHubDoesNotAnswerTheStackQuestionIsRefusedAndRunsNoMerge(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "MERGE", layer1, layer2)
	g.write("pr_detail.fail", "gh: HTTP 502\n")
	code, body := w.postMerge(t, ship, map[string]any{})
	wantCode(t, "POST merge when the detail read fails", http.StatusBadGateway, code, body)
	holdThat(t, "gh merged, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))
}

func TestMergeOfAShipWhosePRHasNoDetailOnGitHubIsRefusedAndRunsNoMerge(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "MERGE", layer1, layer2)
	if err := os.Remove(filepath.Join(g.dir, "pr_detail", strings.NewReplacer("/", "_", ":", "_").Replace(layer2))); err != nil {
		t.Fatal(err)
	}
	code, body := w.postMerge(t, ship, map[string]any{})
	wantCode(t, "POST merge when one PR's detail is not found", http.StatusBadGateway, code, body)
	holdThat(t, "gh merged, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))
}

func TestMergeOfAStackWithALayerNotReadyIsRefusedAndRunsNoMergeOfAnyKind(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "MERGE", layer1, layer2)
	// Layer 2 stops being ready after the last poll, while GitHub's stack still matches.
	g.prChips(layer2, "APPROVED", "PENDING", "MERGEABLE")
	code, body := w.postMerge(t, ship, map[string]any{})
	wantCode(t, "POST merge with layer 2 not ready", http.StatusConflict, code, body)
	if !strings.Contains(body, "not ready") {
		t.Errorf("refusal = %s, want the not-ready sentence", body)
	}
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
	g.githubStack(4, layer1)
	w.start()
	w.in.waitShipsList(t, 20*time.Second, "the foreign record moored", func(l []shipJSON) bool {
		return byName(l)["Foreign"].Status == "moored"
	})
	code, body := w.postMerge(t, shipJSON{Session: "foreign"}, map[string]any{})
	wantCode(t, "POST merge on a stack whose second PR is on another host", http.StatusUnprocessableEntity, code, body)
	if !strings.Contains(body, "GitHub host") {
		t.Errorf("body = %s, want the host refusal", body)
	}
	holdThat(t, "gh merged a PR on another host", func() bool { return len(g.mergeCalls()) == 0 && g.anyStackCalls() == 0 })
}

// upTo means the top of GitHub's stack, and GitHub's order is not the order the ship recorded its PRs in.
func TestMergeOfAStackUpToAnythingButGitHubsTopLayerIsRefusedAndTheTopLayerMergesTheWhole(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "REBASE", layer1, layer2, layer3)
	// GitHub's bottom to top is 21, 23, 22; the ship's record ends with 23.
	g.githubStack(4, layer1, layer3, layer2)
	for _, up := range []string{layer1, layer3} {
		code, b := w.postMerge(t, ship, map[string]any{"upTo": up})
		wantCode(t, "POST merge up to "+up, http.StatusBadRequest, code, b)
		if !strings.Contains(b, "whole stack") {
			t.Errorf("body = %s, want it to say the whole stack merges", b)
		}
	}
	holdThat(t, "gh merged for an upTo below the top, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))

	code, b := w.postMerge(t, ship, map[string]any{"upTo": layer2})
	if code != http.StatusAccepted {
		t.Fatalf("POST merge up to GitHub's top layer = %d, want 202: %s", code, b)
	}
	calls := g.mergeCalls()
	if len(calls) != 1 || !slices.Equal(calls[0].Argv, []string{"stack", "merge", "4", "--yes", "--merge-method", "rebase"}) {
		t.Errorf("merge calls = %v, want one gh stack merge 4 --yes --merge-method rebase", calls)
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

// A ship's independent PRs merge one at a time by URL, and the ship stays moored until the last one is merged.
func TestMergeOfIndependentPRsMergesOneAtATimeAndTheShipStaysMooredUntilTheLast(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.defaultMergeMethod("SQUASH")
	g.independentPRs(layer1, layer2)
	g.prReady(layer1)
	g.prState(layer2, "OPEN")
	g.prChips(layer2, "APPROVED", "PENDING", "MERGEABLE") // not ready, and its Merge is not the first one's business
	w.start()
	ship := moorPRs(t, w, layer1, layer2)
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the first PR read ready", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return len(sh.PRs) == 2 && sh.PRs[0].Ready && !sh.PRs[1].Ready
	})

	code, body := w.postMerge(t, ship, map[string]any{})
	wantCode(t, "POST merge on independent PRs naming none", http.StatusConflict, code, body)
	if strings.Contains(body, "not ready") || strings.Contains(strings.ToLower(body), "checkout") {
		t.Errorf("refusal = %s, want it to ask which PR to merge, not blame readiness or a checkout", body)
	}
	code, body = w.postMerge(t, ship, map[string]any{"pr": "https://github.com/acme/widgets/pull/99"})
	wantCode(t, "POST merge naming a PR that is not the ship's", http.StatusBadRequest, code, body)
	code, body = w.postMerge(t, ship, map[string]any{"pr": layer2})
	wantCode(t, "POST merge naming the PR that is not ready", http.StatusConflict, code, body)
	holdThat(t, "gh merged for a refused merge, or the ship left its mooring", stillMooredWithNoMerge(w, g, ship))

	code, body = w.postMerge(t, ship, map[string]any{"pr": layer1})
	if code != http.StatusAccepted {
		t.Fatalf("POST merge naming the first PR = %d, want 202: %s", code, body)
	}
	var got struct {
		Merged []string `json:"merged"`
		Method string   `json:"method"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || !slices.Equal(got.Merged, []string{layer1}) || got.Method != "squash" {
		t.Errorf("answer = %s, want only the first PR merged, with squash", body)
	}
	calls := g.mergeCalls()
	if len(calls) != 1 {
		t.Fatalf("merge calls = %v, want exactly one gh pr merge", calls)
	}
	checkPRMergeArgv(t, calls[0], layer1, "squash")
	if n := g.anyStackCalls(); n != 0 {
		t.Errorf("gh stack calls = %d, want none for an independent PR", n)
	}
	w.waitEvent(ship.Session, "merged", 1)
	for _, m := range w.outcomeLog() {
		if m["ship"] == ship.Session && m["event"] == "merged" {
			if f, _ := json.Marshal(m["prs"]); string(f) != `["`+layer1+`"]` {
				t.Errorf("merged outcome prs = %s, want only the first PR", f)
			}
		}
	}
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the poll saw the first PR merged", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return len(sh.PRs) == 2 && sh.PRs[0].State == "MERGED"
	})
	holdThat(t, "the ship left its mooring while one PR is still open", func() bool {
		sh, ok := w.shipNamed(ship)
		return ok && sh.Status == "moored" && lastOf(w.outcomeLog(), "unmoored") == nil
	})

	// The second PR is merged by its own Merge, when it is ready; the merged first PR does not hold it up.
	g.prChips(layer2, "APPROVED", "SUCCESS", "MERGEABLE")
	code, body = w.postMerge(t, ship, map[string]any{"pr": layer2})
	if code != http.StatusAccepted {
		t.Fatalf("POST merge naming the second PR = %d, want 202: %s", code, body)
	}
	calls = g.mergeCalls()
	if len(calls) != 2 {
		t.Fatalf("merge calls = %v, want the first and the second", calls)
	}
	checkPRMergeArgv(t, calls[1], layer2, "squash")
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the ship left its mooring after its last PR merged", func(l []shipJSON) bool { return byName(l)[ship.Name].Status != "moored" })
	if ev := lastOf(w.waitEvent(ship.Session, "unmoored", 1), "unmoored"); ev["reason"] != "merged" {
		t.Errorf("unmoored = %v, want reason merged", ev)
	}
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
