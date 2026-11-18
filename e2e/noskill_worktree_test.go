package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// originCheckout makes a checkout of acme/<name> whose origin is a local bare repo with default branch trunk, one commit ahead of the checkout's HEAD.
func (w *world) originCheckout(name string) (dir, tip string) {
	w.t.Helper()
	bare := filepath.Join(w.root, "origin-"+name+".git")
	scratch := filepath.Join(w.root, "scratch-"+name)
	for _, d := range []string{bare, scratch} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			w.t.Fatal(err)
		}
	}
	w.git(bare, "init", "-q", "--bare", "-b", "trunk")
	w.git(scratch, "init", "-q", "-b", "trunk")
	w.git(scratch, "remote", "add", "origin", bare)
	commit := func(file string) {
		if err := os.WriteFile(filepath.Join(scratch, file), []byte(file+"\n"), 0o644); err != nil {
			w.t.Fatal(err)
		}
		w.git(scratch, "add", file)
		w.git(scratch, "commit", "-q", "-m", file)
		w.git(scratch, "push", "-q", "origin", "trunk")
	}
	commit("a.txt")
	dir = w.plainCheckout(name)
	slug := "https://github.com/acme/" + name + ".git"
	w.git(dir, "remote", "add", "origin", slug)
	w.git(dir, "config", "url."+bare+".insteadOf", slug)
	w.git(dir, "fetch", "-q", "origin")
	w.git(dir, "checkout", "-q", "-B", "main", "origin/trunk")
	commit("b.txt")
	return dir, w.git(bare, "rev-parse", "trunk")
}

func samePathOf(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// worktreesOf lists the registered worktree paths of a checkout, symlinks resolved.
func (w *world) worktreesOf(dir string) []string {
	w.t.Helper()
	var out []string
	for _, l := range strings.Split(w.git(dir, "worktree", "list", "--porcelain"), "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			if r, err := filepath.EvalSymlinks(p); err == nil {
				p = r
			}
			out = append(out, p)
		}
	}
	return out
}

func (w *world) shipCwd(session string) string {
	w.t.Helper()
	return strings.TrimSpace(w.readFile(filepath.Join(w.rec, session, "cwd")))
}

func (w *world) isWorktreeOf(dir, path string) bool {
	w.t.Helper()
	r, err := filepath.EvalSymlinks(path)
	return err == nil && slices.Contains(w.worktreesOf(dir), r)
}

func (w *world) branches(dir string) string {
	w.t.Helper()
	return w.git(dir, "branch", "--format=%(refname:short)")
}

func worktreeWorld(t *testing.T, cues ...string) (w *world, dir, tip string) {
	t.Helper()
	w = newWorld(t)
	dir, tip = w.originCheckout("widgets")
	if len(cues) == 0 {
		cues = []string{"hook working"}
	}
	w.cueFleets(map[string][]string{"f": cues})
	w.start()
	return w, dir, tip
}

func TestANoSkillShipRunsInAWorktreeCutFromOriginsTipAndTheCheckoutIsLeftAlone(t *testing.T) {
	t.Parallel()
	w, dir, tip := worktreeWorld(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head, branch, status := w.git(dir, "rev-parse", "HEAD"), w.git(dir, "branch", "--show-current"), w.git(dir, "status", "--porcelain")

	ship := w.launchIn("f", "claude", "widgets")
	cwd := w.waitCwd(ship.Session)

	if samePathOf(t, cwd, dir) || !w.isWorktreeOf(dir, cwd) {
		t.Fatalf("the harness ran in %s, want a worktree of %s", cwd, dir)
	}
	if got := w.git(cwd, "rev-parse", "HEAD"); got != tip {
		t.Errorf("the worktree's HEAD = %s, want origin's default-branch tip %s", got, tip)
	}
	if got := w.git(cwd, "branch", "--show-current"); got == "" || got == branch {
		t.Errorf("the worktree's branch = %q, want a new branch beside %q", got, branch)
	}
	if got := w.git(dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("the checkout's HEAD = %s, want %s", got, head)
	}
	if got := w.git(dir, "branch", "--show-current"); got != branch {
		t.Errorf("the checkout's branch = %q, want %q", got, branch)
	}
	if got := w.git(dir, "status", "--porcelain"); got != status {
		t.Errorf("the checkout's status = %q, want %q", got, status)
	}
	if exists(filepath.Join(dir, ".admiral")) {
		t.Error("the checkout holds .admiral/, want it untouched")
	}
	if got := w.worktreesOf(dir); len(got) != 2 {
		t.Errorf("worktrees = %v, want the checkout and the ship's one", got)
	}
}

// forgetRecord drops the launch's cwd and recorded files, so a later read sees only what the next launch writes.
func (w *world) forgetRecord(session string) {
	w.t.Helper()
	for _, f := range []string{"cwd", "recorded"} {
		if err := os.Remove(filepath.Join(w.rec, session, f)); err != nil {
			w.t.Fatal(err)
		}
	}
}

// wantWorktree fails the test unless cwd is a worktree of dir rather than dir itself.
func (w *world) wantWorktree(dir, cwd string) {
	w.t.Helper()
	if samePathOf(w.t, cwd, dir) || !w.isWorktreeOf(dir, cwd) {
		w.t.Fatalf("the first launch ran in %s, want a worktree of %s", cwd, dir)
	}
}

func (w *world) waitCwd(session string) string {
	w.t.Helper()
	w.waitFile(session, "recorded")
	return w.shipCwd(session)
}

func TestAShipLaunchedWithASkillStillRunsInTheCheckout(t *testing.T) {
	t.Parallel()
	w, dir, _ := worktreeWorld(t)
	skilled := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	if cwd := w.waitCwd(skilled.Session); !samePathOf(t, cwd, dir) {
		t.Errorf("a Commodore ship ran in %s, want the checkout %s", cwd, dir)
	}
	if got := w.worktreesOf(dir); len(got) != 1 {
		t.Errorf("worktrees = %v after the Commodore launch, want the checkout alone", got)
	}
	// Paired with a no-skill launch so the test is red until the worktree exists.
	plain := w.launchIn("f", "claude", "widgets")
	w.wantWorktree(dir, w.waitCwd(plain.Session))
}

func TestALaunchFromACheckoutWithNoOriginIsRefusedAndNoShipPutToSea(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.cueFleets(map[string][]string{"f": {"hook working"}})
	w.start()

	order := map[string]any{"fleet": "f", "harness": "claude", "dir": w.plainCheckout("bare"), "skill": noSkill, "order_id": "o-bare", "text": "x"}
	code, body := w.post(order)
	if code < 400 || code >= 500 {
		t.Errorf("POST /api/ships = %d, want a refusal: %s", code, body)
	}
	if ships := w.in.ships(t); len(ships) != 0 {
		t.Errorf("ships = %v, want none put to sea", ships)
	}
	if got := w.sessions(); len(got) != 0 {
		t.Errorf("tmux sessions = %v, want none", got)
	}
}

func TestStopShipLeavesTheWorktreeInPlace(t *testing.T) {
	t.Parallel()
	w, dir, _ := worktreeWorld(t, "record", "bracketedpaste", "hook working", "wait go", `event Stop {"background_tasks":[]}`)
	ship := w.launchIn("f", "claude", "widgets")
	cwd := w.waitCwd(ship.Session)
	if code, body := send(t, http.MethodPost, w.shipURL(ship.Session, "/stop"), nil); code != http.StatusOK {
		t.Fatalf("stop = %d: %s", code, body)
	}
	waitCond(t, 10*time.Second, "the stopped ship's session gone", func() bool { return !w.hasSession(ship.Session) })
	if samePathOf(t, cwd, dir) || !w.isWorktreeOf(dir, cwd) {
		t.Errorf("worktree %s: want a worktree of the checkout, still registered and in place after Stop ship", cwd)
	}
}

func TestDismissLeavesTheWorktreeInPlace(t *testing.T) {
	t.Parallel()
	w, dir, _ := worktreeWorld(t, "hook working", "exit 1")
	ship := w.launchIn("f", "claude", "widgets")
	cwd := w.waitCwd(ship.Session)
	w.in.waitShipsList(t, 20*time.Second, "the ship anchored", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "anchored" })
	if code := w.dismiss(ship.Session); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("dismiss = %d", code)
	}
	waitCond(t, 10*time.Second, "the dismissed ship gone", func() bool { return len(w.in.ships(t)) == 0 })
	if samePathOf(t, cwd, dir) || !w.isWorktreeOf(dir, cwd) {
		t.Errorf("worktree %s: want a worktree of the checkout, still registered and in place after Dismiss", cwd)
	}
}

func TestReturnToPortOfALiveNoSkillShipRemovesItsWorktreeAndKeepsItsBranch(t *testing.T) {
	t.Parallel()
	w, dir, _ := worktreeWorld(t, "record", "bracketedpaste", "hook working", "wait go", `event Stop {"background_tasks":[]}`)
	ship := w.launchIn("f", "claude", "widgets")
	cwd := w.waitCwd(ship.Session)
	w.wantWorktree(dir, cwd)
	branch := w.git(cwd, "branch", "--show-current")
	pane := strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", "="+ship.Session+":0", "#{pane_id}"))
	w.touch(ship.Session, "go")
	w.waitFile(ship.Session, "cues-done")
	w.waitPaneState(ship.Session, "needs_you")

	if code, body := send(t, http.MethodPost, w.shipURL(ship.Session, "/return"), nil); code != http.StatusOK {
		t.Fatalf("return = %d: %s", code, body)
	}
	holdFor(t, "the clean-up prompt at the ship's terminal", func() bool { return strings.Contains(w.inputOf(ship.Session, ""), "Return to port") })
	w.waitPaneState(ship.Session, "working")
	w.hookAt(pane, "Stop", hookStop)

	waitCond(t, 20*time.Second, "the returned ship's session gone", func() bool { return !w.hasSession(ship.Session) })
	waitCond(t, 20*time.Second, "the worktree removed", func() bool { return !exists(cwd) })
	if w.isWorktreeOf(dir, cwd) || len(w.worktreesOf(dir)) != 1 {
		t.Errorf("worktrees = %v, want the checkout alone", w.worktreesOf(dir))
	}
	if !slices.Contains(strings.Fields(w.branches(dir)), branch) {
		t.Errorf("branches = %q, want %s kept", w.branches(dir), branch)
	}
}

func TestReturnToPortOfAMooredNoSkillShipWhosePRMergedRemovesItsWorktreeAndKeepsItsBranch(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", "fake-token-f")
	dir, _ := w.originCheckout("widgets")
	w.outFleets(map[string][]string{"f": {"hook working", "wait go", "sleep 1", "exit 0"}}, srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Fleet: "f", Settings: map[string]any{"views": []any{}}})
	g.prState(mooredPR, "OPEN")
	g.prChips(mooredPR, "APPROVED", "PENDING", "MERGEABLE")
	w.start()

	ship := w.launchIn("f", "claude", "widgets")
	cwd := w.waitCwd(ship.Session)
	w.wantWorktree(dir, cwd)
	branch := w.git(cwd, "branch", "--show-current")
	w.exitWith(ship, mooredReport(mooredPR))
	w.in.waitShipsList(t, 20*time.Second, "the ship moored", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "moored" })
	if !exists(cwd) || !w.isWorktreeOf(dir, cwd) {
		t.Fatalf("worktree %s: want it kept while the ship is moored", cwd)
	}

	g.prState(mooredPR, "MERGED")
	w.kickMoored()
	w.waitEvent(ship.Session, "woken", 1)
	waitUntil(t, "the moored record removed", func() bool { return !exists(w.mooredFile(ship.Name)) })
	waitCond(t, 20*time.Second, "the worktree removed", func() bool { return !exists(cwd) })
	if got := w.worktreesOf(dir); len(got) != 1 {
		t.Errorf("worktrees = %v, want the checkout alone", got)
	}
	if got := strings.Fields(w.branches(dir)); !slices.Contains(got, branch) || len(got) != 2 {
		t.Errorf("branches = %v, want main and %s only, with no branch made by the wake", got, branch)
	}
}

func TestAMooredNoSkillShipWakesInTheSameWorktreeItWasLaunchedIn(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", "fake-token-f")
	dir, _ := w.originCheckout("widgets")
	w.outFleets(map[string][]string{"f": {"hook working", "wait go", "sleep 1", "exit 0"}}, srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Fleet: "f", Settings: map[string]any{"views": []any{}}})
	g.prState(mooredPR, "OPEN")
	g.prChips(mooredPR, "APPROVED", "PENDING", "MERGEABLE")
	w.start()

	ship := w.launchIn("f", "claude", "widgets")
	cwd := w.waitCwd(ship.Session)
	w.wantWorktree(dir, cwd)
	branches := w.branches(dir)
	w.exitWith(ship, mooredReport(mooredPR))
	w.in.waitShipsList(t, 20*time.Second, "the ship moored", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "moored" })

	w.forgetRecord(ship.Session)
	g.prState(mooredPR, "MERGED")
	w.kickMoored()
	w.waitEvent(ship.Session, "woken", 1)
	if got := w.waitCwd(ship.Session); !samePathOf(t, got, cwd) {
		t.Errorf("the woken ship ran in %s, want its worktree %s", got, cwd)
	}
	if got := w.branches(dir); got != branches {
		t.Errorf("branches after the wake = %q, want %q: the wake makes no branch", got, branches)
	}
}

func TestHandingANoSkillShipToCommodoreRelaunchesInItsWorktreeAndKeepsIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir, _ := w.originCheckout("widgets")
	w.cueFleets(map[string][]string{"f": {"record", "bracketedpaste", "hook working", "wait go", `event Stop {"background_tasks":[]}`}})
	cfgDir := filepath.Join(w.root, "claude-cfg")
	skill := filepath.Join(cfgDir, "skills", "commodore")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: commodore\ndescription: a fake skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.readFile(configPath(w.home))), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["fleets"].(map[string]any)["f"].(map[string]any)["claude"].(map[string]any)["env"].(map[string]any)["CLAUDE_CONFIG_DIR"] = cfgDir
	b, _ := json.Marshal(cfg)
	writeConfig(t, w.home, string(b))
	w.start()

	ship := w.launchIn("f", "claude", "widgets")
	first := w.record(ship.Session)
	seedRound(t, cfgDir, first, "the earlier design talk")
	pane := strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", "="+ship.Session+":0", "#{pane_id}"))
	w.waitPaneWorking(pane)
	w.wantWorktree(dir, first.cwd)
	w.forgetRecord(ship.Session)
	if code, body := send(t, http.MethodPost, w.shipURL(ship.Session, "/commodore"), map[string]any{"kind": "issue"}); code != http.StatusAccepted {
		t.Fatalf("hand = %d: %s", code, body)
	}
	w.touch(ship.Session, "go")
	refRe := regexp.MustCompile(regexp.QuoteMeta(w.home) + `/ships/` + regexp.QuoteMeta(ship.Session) + `/[\w.\-]+`)
	var ref string
	holdFor(t, "the prompt naming the ref file", func() bool {
		ref = strings.TrimRight(refRe.FindString(w.inputOf(ship.Session, "")), ".,;:)\"'`")
		return ref != ""
	})
	if err := os.MkdirAll(filepath.Dir(ref), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref, []byte(filepath.Join(dir, "a.txt")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.hookAt(pane, "Stop", hookStop)

	argvPath := filepath.Join(w.rec, ship.Session, "argv")
	holdFor(t, "the ship relaunched with --resume", func() bool {
		raw, _ := os.ReadFile(argvPath)
		return slices.Contains(strings.Split(string(raw), "\x00"), "--resume")
	})
	if got := w.waitCwd(ship.Session); !samePathOf(t, got, first.cwd) {
		t.Errorf("the relaunch ran in %s, want the ship's worktree %s", got, first.cwd)
	}
	if !exists(first.cwd) || !w.isWorktreeOf(dir, first.cwd) {
		t.Errorf("worktree %s: want it kept after the hand", first.cwd)
	}
}

func TestARoutineRunOfNoSkillGetsAWorktree(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard()
	w.writeCue("wait go", "exit 0")
	now := time.Now()
	skew := now.Truncate(time.Minute).Add(45 * time.Second).Sub(now)
	if skew < 0 {
		skew += time.Minute
	}
	w.extra = append(w.extra, "ADMIRAL_E2E_CLOCK_SKEW="+skew.String())
	dir, tip := w.originCheckout("sched")
	w.start()

	due := time.Now().Add(skew).Add(5 * time.Second).Truncate(time.Minute).Add(time.Minute).In(time.Local)
	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/fleets/work/routines", map[string]any{
		"name": "Every minute test", "enabled": true, "when": due.Format("4 15") + " * * *", "before_acting": "act",
		"do": map[string]any{"launch": map[string]any{"harness": "claude", "dir": dir, "text": "go"}},
	})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("POST routine = %d: %s", code, body)
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &made); err != nil || made.ID == "" {
		t.Fatalf("created routine has no id: %v\n%s", err, body)
	}
	var ship string
	waitCond(t, 90*time.Second, "the routine's launched run", func() bool {
		for _, r := range w.routineRuns(made.ID) {
			if r.Outcome == "launched" {
				ship = r.Ship
			}
		}
		return ship != ""
	})
	cwd := w.waitCwd(ship)
	if samePathOf(t, cwd, dir) || !w.isWorktreeOf(dir, cwd) {
		t.Fatalf("the routine's ship ran in %s, want a worktree of %s", cwd, dir)
	}
	if got := w.git(cwd, "rev-parse", "HEAD"); got != tip {
		t.Errorf("the worktree's HEAD = %s, want origin's tip %s", got, tip)
	}
}
