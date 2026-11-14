package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func (w *world) stackURL(ship shipJSON) string {
	return w.in.LocalURL + "/api/ships/" + ship.Session + "/stack"
}

func (w *world) getStack(t *testing.T, ship shipJSON) (int, string) {
	t.Helper()
	resp, err := client().Get(w.stackURL(ship))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// doStack posts to the stack route with the token header as given; an empty token sends no header.
func (w *world) doStack(ship shipJSON, token string) (int, string, error) {
	req, err := http.NewRequest(http.MethodPost, w.stackURL(ship), bytes.NewReader([]byte("{}")))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Admiral-Merge-Token", token)
	}
	resp, err := client().Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

func (w *world) postStackWith(t *testing.T, ship shipJSON, token string) (int, string) {
	t.Helper()
	code, body, err := w.doStack(ship, token)
	if err != nil {
		t.Fatal(err)
	}
	return code, body
}

func (w *world) postStack(t *testing.T, ship shipJSON) (int, string) {
	t.Helper()
	return w.postStackWith(t, ship, w.pageMergeToken(t))
}

// wantStack fails unless body is the stack answer for this shape; number 0 means no stack_number at all.
func wantStack(t *testing.T, what, body, shape string, number int, prs ...string) {
	t.Helper()
	var got struct {
		Shape string   `json:"shape"`
		PRs   []string `json:"prs"`
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &got); err != nil || json.Unmarshal([]byte(body), &raw) != nil {
		t.Errorf("%s = %s, want a JSON object", what, body)
		return
	}
	if got.Shape != shape || !slices.Equal(got.PRs, prs) {
		t.Errorf("%s = %s, want shape %s over %v", what, body, shape, prs)
	}
	if string(raw["prs"]) == "null" {
		t.Errorf("%s = %s, want prs as a list, [] when empty", what, body)
	}
	if number == 0 {
		if _, has := raw["stack_number"]; has {
			t.Errorf("%s = %s, want no stack_number: only a stacked shape has one", what, body)
		}
	} else if string(raw["stack_number"]) != itoa(number) {
		t.Errorf("%s = %s, want stack_number %d", what, body, number)
	}
}

// stackCalls are the gh stack calls of one verb, in the order they ran.
func (g *fakeGH) stackCalls(verb string) []ghCall {
	var out []ghCall
	for _, c := range g.calls() {
		if len(c.Argv) >= 2 && c.Argv[0] == "stack" && c.Argv[1] == verb {
			out = append(out, c)
		}
	}
	return out
}

// stackLinkNumber is the number the fake gives the stack gh stack link makes.
func (g *fakeGH) stackLinkNumber(n int) { g.t.Helper(); g.write("stack_link.number", itoa(n)+"\n") }

// noStackCalls fails the test when the fake saw any gh stack call for the next hold seconds.
func noStackCalls(t *testing.T, g *fakeGH, why string) {
	t.Helper()
	holdThat(t, why, func() bool { return g.anyStackCalls() == 0 })
}

// A moored ship's two chained PRs get stacked on GitHub, and the stack then merges whole.
func TestStackAChainedMooredShipLinksOnGitHubAndThenMergesAsOneStack(t *testing.T) {
	t.Parallel()
	w, g, ship := seedMoored(t, "SQUASH", func(g *fakeGH) { g.chainedPRs(layer1, layer2); g.stackLinkNumber(9) }, layer1, layer2)

	code, body := w.getStack(t, ship)
	wantCode(t, "GET stack on chained PRs", http.StatusOK, code, body)
	wantStack(t, "GET stack on chained PRs", body, "chained", 0, layer1, layer2)
	code, body = w.postMerge(t, ship, map[string]any{})
	wantCode(t, "POST merge on chained PRs before they are stacked", http.StatusConflict, code, body)
	noStackCalls(t, g, "a read ran gh stack")

	// GitHub holds the stack, not the checkout: the ship's directory is not needed.
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

	code, body = w.postStack(t, ship)
	wantCode(t, "POST stack on chained PRs", http.StatusOK, code, body)
	wantStack(t, "POST stack on chained PRs", body, "stacked", 9, layer1, layer2)

	links := g.stackCalls("link")
	if len(links) != 1 {
		t.Fatalf("gh stack link calls = %v, want exactly one", links)
	}
	c := links[0]
	// PR numbers from GitHub, bottom to top: no branch (gh pushes branches), no URL and no flag (--open takes drafts out of draft).
	if want := []string{"stack", "link", "21", "22"}; !slices.Equal(c.Argv, want) {
		t.Errorf("argv = %q, want %q", c.Argv, want)
	}
	if c.Cwd == "" || c.Cwd == realPath(t, w.work) || strings.HasPrefix(c.Cwd, realPath(t, w.work)+string(filepath.Separator)) {
		t.Errorf("gh stack link ran in %q, want a folder of its own and not a ship's checkout under %q", c.Cwd, w.work)
	}
	if exists(c.Cwd) {
		t.Errorf("the folder gh stack link ran in, %q, is still there: it should be removed afterwards", c.Cwd)
	}
	if c.Env["GH_TOKEN"] != "fake-token-f" {
		t.Errorf("GH_TOKEN = %q, want the fleet's token", c.Env["GH_TOKEN"])
	}
	if c.Env["GH_HOST"] != "github.com" || c.Env["GH_REPO"] != "github.com/acme/widgets" {
		t.Errorf("stack link env GH_HOST=%q GH_REPO=%q, want github.com and github.com/acme/widgets", c.Env["GH_HOST"], c.Env["GH_REPO"])
	}
	if c.Env["GIT_CONFIG_COUNT"] != "3" || c.Env["GIT_CONFIG_KEY_0"] != "core.fsmonitor" || c.Env["GIT_CONFIG_KEY_1"] != "core.hooksPath" || c.Env["GIT_CONFIG_KEY_2"] != "core.sshCommand" {
		t.Errorf("stack link env = %v, want fsmonitor, hooksPath and sshCommand blanked", c.Env)
	}
	if n := len(g.mergeCalls()); n != 0 {
		t.Errorf("merge calls = %d, want none: linking merges nothing", n)
	}

	code, body = w.getStack(t, ship)
	wantCode(t, "GET stack after the link", http.StatusOK, code, body)
	wantStack(t, "GET stack after the link", body, "stacked", 9, layer1, layer2)

	code, body = w.postMerge(t, ship, map[string]any{})
	if code != http.StatusAccepted {
		t.Fatalf("POST merge after the link = %d, want 202: %s", code, body)
	}
	calls := g.mergeCalls()
	if len(calls) != 1 {
		t.Fatalf("merge calls = %v, want exactly one gh stack merge", calls)
	}
	if want := []string{"stack", "merge", "9", "--yes", "--merge-method", "squash"}; !slices.Equal(calls[0].Argv, want) {
		t.Errorf("merge argv = %q, want %q: the stack number the link made", calls[0].Argv, want)
	}
	if n := len(g.stackCalls("link")); n != 1 {
		t.Errorf("gh stack link calls after the merge = %d, want still one", n)
	}
}

// The bar's one source: what GET says for each way a ship's PRs can relate, and what POST does about it.
func TestStackShapesAnswerTheBarAndOnlyChainedPRsCanBeStacked(t *testing.T) {
	t.Parallel()
	const other = "https://github.com/acme/widgets/pull/24"
	for name, c := range map[string]struct {
		prs    []string
		seed   func(g *fakeGH)
		shape  string
		number int
		reason string // the refusal's words
	}{
		"already a stack on GitHub": {[]string{layer1, layer2}, func(g *fakeGH) { g.githubStack(4, layer1, layer2) }, "stacked", 4, "already a stack on GitHub"},
		"independent PRs":           {[]string{layer1, layer2}, func(g *fakeGH) { g.independentPRs(layer1, layer2) }, "independent", 0, ""},
		"chained and independent mixed": {[]string{layer1, layer2, other}, func(g *fakeGH) {
			g.chainedPRs(layer1, layer2)
			g.prDetail(other, branchOf(other), "main", nil)
		}, "mixed", 0, ""},
		"one PR on the default branch":           {[]string{layer1}, func(g *fakeGH) { g.independentPRs(layer1) }, "independent", 0, ""},
		"one PR on another branch":               {[]string{layer1}, func(g *fakeGH) { g.prDetail(layer1, branchOf(layer1), "develop", nil) }, "mixed", 0, ""},
		"one PR in a one-entry stack on GitHub":  {[]string{layer1}, func(g *fakeGH) { g.githubStack(4, layer1) }, "stacked", 4, ""},
		"two stacks that are not the ship's PRs": {[]string{layer1, layer2}, func(g *fakeGH) { g.githubStack(4, layer1); g.githubStack(5, layer2) }, "mixed", 0, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w, g, ship := seedMoored(t, "MERGE", c.seed, c.prs...)
			code, body := w.getStack(t, ship)
			wantCode(t, "GET stack", http.StatusOK, code, body)
			wantStack(t, "GET stack", body, c.shape, c.number, c.prs...)

			code, body = w.postStack(t, ship)
			wantCode(t, "POST stack", http.StatusConflict, code, body)
			if c.reason != "" && !strings.Contains(body, c.reason) {
				t.Errorf("refusal = %s, want it to say %q", body, c.reason)
			}
			notAboutACheckout(t, body)
			noStackCalls(t, g, "a refused stack ran gh stack")
			if sh, ok := w.shipNamed(ship); !ok || sh.Status != "moored" {
				t.Errorf("ship = %+v, want still moored", sh)
			}
		})
	}
}

// A running ship's PRs show on the bar, but Stack waits until the ship moors.
func TestStackARunningShipCarryingChainedPRsAnswersChainedAndRefusesToStack(t *testing.T) {
	t.Parallel()
	w, g, ship := runningWithPRs(t, layer1, layer2)
	g.chainedPRs(layer1, layer2)

	code, body := w.getStack(t, ship)
	wantCode(t, "GET stack on a running ship", http.StatusOK, code, body)
	wantStack(t, "GET stack on a running ship", body, "chained", 0, layer1, layer2)

	code, body = w.postStack(t, ship)
	wantCode(t, "POST stack on a running ship", http.StatusConflict, code, body)
	if !strings.Contains(body, "moors") {
		t.Errorf("refusal = %s, want it to say to stack after the ship moors", body)
	}
	noStackCalls(t, g, "a running ship's POST ran gh stack")
	if sh, ok := w.shipNamed(ship); !ok || sh.Status == "moored" {
		t.Errorf("ship = %+v, want still running", sh)
	}
}

func TestStackARunningShipCarryingNoPRAnswersNone(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	w.start()
	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))

	code, body := w.getStack(t, ship)
	wantCode(t, "GET stack on a ship with no PR", http.StatusOK, code, body)
	wantStack(t, "GET stack on a ship with no PR", body, "none", 0)
	code, body = w.postStack(t, ship)
	wantCode(t, "POST stack on a ship with no PR", http.StatusConflict, code, body)
	noStackCalls(t, g, "a ship with no PR ran gh stack")
}

func TestStackOfAShipThatIsNotThereIsNotFound(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	w.start()
	ghost := shipJSON{Session: "no-such-ship"}
	code, body := w.getStack(t, ghost)
	wantCode(t, "GET stack on an unknown ship", http.StatusNotFound, code, body)
	code, body = w.postStack(t, ghost)
	wantCode(t, "POST stack on an unknown ship", http.StatusNotFound, code, body)
	noStackCalls(t, g, "an unknown ship ran gh stack")
}

// Independent PRs are never stacked, and the second one's readiness counts again once the first has merged.
func TestStackIndependentPRsAreNotStackedAndTheSecondReadiesTheShipAfterTheFirstMerges(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.defaultMergeMethod("SQUASH")
	g.independentPRs(layer1, layer2)
	g.prReady(layer1)
	g.prState(layer2, "OPEN")
	g.prChips(layer2, "APPROVED", "PENDING", "MERGEABLE")
	w.start()
	ship := moorPRs(t, w, layer1, layer2)
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the first PR read ready and the second not", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return len(sh.PRs) == 2 && sh.PRs[0].Ready && !sh.PRs[1].Ready
	})

	code, body := w.getStack(t, ship)
	wantCode(t, "GET stack on independent PRs", http.StatusOK, code, body)
	wantStack(t, "GET stack on independent PRs", body, "independent", 0, layer1, layer2)
	code, body = w.postStack(t, ship)
	wantCode(t, "POST stack on independent PRs", http.StatusConflict, code, body)
	noStackCalls(t, g, "independent PRs ran gh stack")

	code, body = w.postMerge(t, ship, map[string]any{"pr": layer1})
	if code != http.StatusAccepted {
		t.Fatalf("POST merge naming the first PR = %d, want 202: %s", code, body)
	}
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the poll saw the first PR merged", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return len(sh.PRs) == 2 && sh.PRs[0].State == "MERGED"
	})

	g.prChips(layer2, "APPROVED", "SUCCESS", "MERGEABLE")
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the second PR ready makes the ship ready to merge again", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return len(sh.PRs) == 2 && sh.PRs[1].Ready && sh.StackReady && sh.ReadyUpTo == layer2
	})
}

// A stack whose bottom layer already merged: the rest merges as one gh stack merge.
func TestStackWhoseBottomLayerAlreadyMergedMergesTheRest(t *testing.T) {
	t.Parallel()
	w, g, ship := readyStack(t, "MERGE", layer1, layer2)
	g.prState(layer1, "MERGED")
	w.kickMoored()
	list := w.in.waitShipsList(t, 20*time.Second, "the bottom layer read merged and the top ready", func(l []shipJSON) bool {
		sh := byName(l)[ship.Name]
		return len(sh.PRs) == 2 && sh.PRs[0].State == "MERGED" && sh.PRs[1].Ready
	})
	if sh := byName(list)[ship.Name]; !sh.StackReady || sh.ReadyUpTo != layer2 {
		t.Errorf("ship = ready %v, up to %q; want ready up to %s: the merged layer does not hold it back", sh.StackReady, sh.ReadyUpTo, layer2)
	}

	code, body := w.postMerge(t, ship, map[string]any{})
	if code != http.StatusAccepted {
		t.Fatalf("POST merge on a stack whose bottom layer merged = %d, want 202: %s", code, body)
	}
	calls := g.mergeCalls()
	if len(calls) != 1 || !slices.Equal(calls[0].Argv, []string{"stack", "merge", "4", "--yes", "--merge-method", "merge"}) {
		t.Errorf("merge calls = %v, want one gh stack merge 4 --yes --merge-method merge", calls)
	}
}

func TestStackLinkWithoutTheShellsTokenIsRefusedAndRunsNoGh(t *testing.T) {
	t.Parallel()
	w, g, ship := seedMoored(t, "MERGE", func(g *fakeGH) { g.chainedPRs(layer1, layer2) }, layer1, layer2)
	real := w.pageMergeToken(t)
	for what, token := range map[string]string{
		"no header":                      "",
		"a wrong token":                  "0000000000000000000000000000000000000000000000000000000000000000",
		"the token with a character off": real[:len(real)-1] + "x",
	} {
		if code, b := w.postStackWith(t, ship, token); code != http.StatusForbidden {
			t.Errorf("POST stack with %s = %d, want 403: %s", what, code, b)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, w.stackURL(ship), strings.NewReader("{}"))
	req.Header.Set("X-Admiral-Merge-Token", real)
	req.Header.Set("Origin", "http://evil.example")
	if resp, err := client().Do(req); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("POST stack from another origin with the right token = %d, want 403", resp.StatusCode)
		}
	}
	noStackCalls(t, g, "gh stack ran without the token")
	if code, b := w.postStackWith(t, ship, real); code != http.StatusOK {
		t.Errorf("POST stack with the page's token = %d, want 200: %s", code, b)
	}
}

func TestStackLinkOfAShipWithAPROnAnotherHostIsRefusedAndRunsNoGh(t *testing.T) {
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
	g.chainedPRs(layer1)
	w.start()
	w.in.waitShipsList(t, 20*time.Second, "the foreign record moored", func(l []shipJSON) bool {
		return byName(l)["Foreign"].Status == "moored"
	})
	code, body := w.postStack(t, shipJSON{Session: "foreign"})
	wantCode(t, "POST stack on a ship with a PR on another host", http.StatusUnprocessableEntity, code, body)
	if !strings.Contains(body, "GitHub host") {
		t.Errorf("body = %s, want the host refusal", body)
	}
	noStackCalls(t, g, "gh stack ran for a PR on another host")
}

func TestStackLinkThatGhFailsAnswers502AndChangesNothing(t *testing.T) {
	t.Parallel()
	w, g, ship := seedMoored(t, "MERGE", func(g *fakeGH) { g.chainedPRs(layer1, layer2) }, layer1, layer2)
	g.write("stack_link.fail", "gh stack: pull request 22 cannot join a stack\n")

	code, body := w.postStack(t, ship)
	if code != http.StatusBadGateway || !strings.Contains(body, "pull request 22 cannot join a stack") {
		t.Errorf("POST stack = %d %s, want 502 carrying gh's message", code, body)
	}
	if n := len(g.stackCalls("link")); n != 1 {
		t.Errorf("gh stack link calls = %d, want exactly the failed one", n)
	}
	code, body = w.getStack(t, ship)
	wantCode(t, "GET stack after a failed link", http.StatusOK, code, body)
	wantStack(t, "GET stack after a failed link", body, "chained", 0, layer1, layer2)
	holdThat(t, "the failed link ended the ship or ran a merge", stillMooredWithNoMerge(w, g, ship))
}

// A link, a merge and a wake of one ship never overlap.
func TestStackLinkWhileAnotherLinkIsRunningIsRefused(t *testing.T) {
	t.Parallel()
	w, g, ship := seedMoored(t, "MERGE", func(g *fakeGH) { g.chainedPRs(layer1, layer2) }, layer1, layer2)
	g.write("stack_link.sleep", "2\n")
	type result struct {
		code int
		body string
	}
	token := w.pageMergeToken(t)
	first := make(chan result, 1)
	go func() {
		c, b, err := w.doStack(ship, token)
		if err != nil {
			b = err.Error()
		}
		first <- result{c, b}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(g.stackCalls("link")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first link never reached gh")
		}
		time.Sleep(20 * time.Millisecond)
	}

	code, body := w.postStack(t, ship)
	wantCode(t, "POST stack while a link is running", http.StatusConflict, code, body)
	r := <-first
	wantCode(t, "the first POST stack", http.StatusOK, r.code, r.body)
	if n := len(g.stackCalls("link")); n != 1 {
		t.Errorf("gh stack link calls = %d, want one: the refused POSTs ran nothing", n)
	}
}

// runningWithPRs launches a ship whose transcript shows each of the PRs it opened, and waits until the list carries them.
func runningWithPRs(t *testing.T, prs ...string) (*world, *fakeGH, shipJSON) {
	t.Helper()
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", "fake-token-f")
	w.checkoutOf(w.work, "widgets", "acme/widgets")
	cfgDir := filepath.Join(w.root, "cfg")
	for _, d := range []string{filepath.Join(cfgDir, "projects", "zz"), filepath.Join(cfgDir, "skills", "commodore")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "skills", "commodore", "SKILL.md"), []byte("---\nname: commodore\ndescription: a fake skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// One cue carries every URL: each toolresult line reuses one tool-call uuid, so a second cue would replace the first.
	lines := []string{"hook working", "toolresult " + strings.Join(prs, " "), "wait go", "sleep 1", "exit 0"}
	cue := filepath.Join(w.root, "cue-f")
	if err := os.WriteFile(cue, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := w.harnessEnv()
	env["FAKE_CUE"] = cue
	env["CLAUDE_CONFIG_DIR"] = cfgDir
	cfg := map[string]any{"version": 1, "fleets": map[string]any{"f": map[string]any{"name": "f", "color": "#336699", "search_roots": []string{w.work},
		"claude": map[string]any{"executable": w.fakeExe("claude"), "env": env}}}}
	cfg["sources"] = json.RawMessage(rawSourcesIn(firstFleet(t, cfg), srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Fleet: "f", Settings: map[string]any{"views": []any{}}}))
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(b))
	for _, u := range prs {
		g.prReady(u)
		g.prAuthored(u, "fake-viewer", time.Now().Add(time.Hour))
	}
	w.start()

	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	deadline := time.Now().Add(10 * time.Second)
	for !transcriptHas(cfgDir, prs[len(prs)-1]) {
		if time.Now().After(deadline) {
			t.Fatal("the fake never wrote the tool results to its transcript")
		}
		time.Sleep(50 * time.Millisecond)
	}
	w.kickMoored()
	w.in.waitShipsList(t, 20*time.Second, "the running ship carrying the PRs it opened", func(l []shipJSON) bool {
		s := byName(l)[ship.Name]
		return s.Status != "moored" && len(s.PRs) == len(prs)
	})
	return w, g, ship
}
