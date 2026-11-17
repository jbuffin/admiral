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
	if want := []string{"stack", "link", "--base", "main", "21", "22"}; !slices.Equal(c.Argv, want) {
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
