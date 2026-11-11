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
	"sync"
	"testing"
)

// startCloning starts the world with one fleet, work, searching w.work. cloneRoot is the
// fleet's clone_root, left out when empty. The fleet's env carries FLEET_MARK so a test can see
// that gh ran with the fleet's environment.
func (w *world) startCloning(cloneRoot string) *fakeGH {
	w.t.Helper()
	gh := w.newFakeGH()
	fleet := map[string]any{"name": "Work", "search_roots": []string{w.work}, "claude": w.dock("claude"),
		"env": map[string]string{"FLEET_MARK": "work-mark"}}
	if cloneRoot != "" {
		fleet["clone_root"] = cloneRoot
	}
	b, err := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{"work": fleet}})
	if err != nil {
		w.t.Fatal(err)
	}
	writeConfig(w.t, w.home, string(b))
	w.writeCue("exit 3")
	w.start()
	return gh
}

func (w *world) mkdir(p string) string {
	w.t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		w.t.Fatal(err)
	}
	return p
}

// cloneLaunch is what the shell's form sends for an uncloned repo: no dir, clone true.
func cloneLaunch(text string) map[string]any {
	return map[string]any{"fleet": "work", "harness": "claude", "clone": true, "skill": noSkill, "text": text}
}

func issueText(repo string) string { return "look at https://github.com/" + repo + "/issues/7" }

// cloneCalls are the `gh repo clone` runs: the repo and the directory gh was given.
type cloneCall struct {
	repo, dir string
	call      ghCall
}

func (g *fakeGH) cloneCalls() []cloneCall {
	g.t.Helper()
	var out []cloneCall
	for _, c := range g.calls() {
		if !c.has("repo", "clone") {
			continue
		}
		var pos []string
		for _, a := range c.Argv[slices.Index(c.Argv, "clone")+1:] {
			if a != "--" {
				pos = append(pos, a)
			}
		}
		cc := cloneCall{call: c}
		if len(pos) > 0 {
			cc.repo = pos[0]
		}
		if len(pos) > 1 {
			cc.dir = pos[1]
		}
		out = append(out, cc)
	}
	return out
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func errorOf(t *testing.T, body string) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(body), &e) != nil || e.Error == "" {
		t.Fatalf("body must be JSON {\"error\": \"...\"}, got %s", body)
	}
	return e.Error
}

type dirsBody struct {
	Entries []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"entries"`
	Clone json.RawMessage `json:"clone"`
}

func (w *world) dirsFor(repo, text string) dirsBody {
	w.t.Helper()
	code, body := w.postDirs("work", "claude", repo, text)
	if code != http.StatusOK {
		w.t.Fatalf("POST /api/launch-orders/dirs = %d, want 200: %s", code, body)
	}
	var d dirsBody
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		w.t.Fatalf("dirs response is not JSON: %v\n%s", err, body)
	}
	return d
}

func (d dirsBody) checkouts() []string {
	var out []string
	for _, e := range d.Entries {
		if e.Kind == "checkout" {
			out = append(out, e.Path)
		}
	}
	return out
}

// A launch for a repo with no checkout clones it with gh, under the fleet's env, into a
// sibling of its final place that is renamed in, and the ship starts in the new checkout.
func TestCloneLaunchClonesAnUnclonedRepo(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	clones := w.mkdir(filepath.Join(w.work, "clones"))
	gh := w.startCloning(clones)
	want := filepath.Join(clones, "widget")
	if d := w.dirsFor("https://github.com/acme/widget", ""); slices.Contains(d.checkouts(), want) {
		t.Fatalf("the checkout %s exists before the launch", want)
	}

	ship := w.launch(cloneLaunch(issueText("acme/widget")))

	if ship.Dir != want {
		t.Errorf("ship dir = %q, want %q", ship.Dir, want)
	}
	if _, err := os.Lstat(filepath.Join(want, ".git")); err != nil {
		t.Errorf("%s is not a git checkout: %v", want, err)
	}
	if cwd := w.record(ship.Session).cwd; cwd != want {
		t.Errorf("the harness started in %q, want %q", cwd, want)
	}
	calls := gh.cloneCalls()
	if len(calls) != 1 {
		t.Fatalf("gh repo clone ran %d times, want 1: %v", len(calls), calls)
	}
	c := calls[0]
	if c.repo != "github.com/acme/widget" {
		t.Errorf("gh cloned %q, want github.com/acme/widget", c.repo)
	}
	if c.dir == "" || filepath.Dir(filepath.Dir(c.dir)) != clones || c.dir == want {
		t.Errorf("gh was asked to clone into %q, want a child of a temporary folder inside %s that is renamed to %s", c.dir, clones, want)
	}
	for k, v := range map[string]string{"GH_PROMPT_DISABLED": "1", "GH_NO_UPDATE_NOTIFIER": "1", "FLEET_MARK": "work-mark"} {
		if c.call.Env[k] != v {
			t.Errorf("gh ran with %s=%q, want %q", k, c.call.Env[k], v)
		}
	}
	if got := names(t, clones); !slices.Equal(got, []string{"widget"}) {
		t.Errorf("%s holds %v, want only widget", clones, got)
	}
	d := w.dirsFor("https://github.com/acme/widget", "")
	if !slices.Contains(d.checkouts(), want) {
		t.Errorf("the next dirs lookup lists checkouts %v, want %s among them", d.checkouts(), want)
	}
	if string(d.Clone) != "null" {
		t.Errorf("dirs clone = %s for a repo that has a checkout now, want null", d.Clone)
	}
}

// With no clone_root the first search root is used.
func TestCloneLaunchDefaultsToTheFirstSearchRoot(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.startCloning("")
	ship := w.launch(cloneLaunch(issueText("acme/widget")))
	if want := filepath.Join(w.work, "widget"); ship.Dir != want {
		t.Errorf("ship dir = %q, want %q", ship.Dir, want)
	}
}

// A clone_root of ~/... is the home directory of Admiral's user.
func TestCloneLaunchExpandsATildeCloneRoot(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.mkdir(filepath.Join(w.userHom, "clones"))
	w.startCloning("~/clones")
	ship := w.launch(cloneLaunch(issueText("acme/widget")))
	if want := filepath.Join(w.userHom, "clones", "widget"); ship.Dir != want {
		t.Errorf("ship dir = %q, want %q", ship.Dir, want)
	}
}

// The clone_root is one more place the fleet looks for projects, wherever it points.
func TestCloneLaunchRootOutsideTheSearchRootsStillCountsAsAProject(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	clones := w.mkdir(filepath.Join(w.root, "elsewhere"))
	w.startCloning(clones)
	ship := w.launch(cloneLaunch(issueText("acme/widget")))
	want := filepath.Join(clones, "widget")
	if ship.Dir != want {
		t.Errorf("ship dir = %q, want %q", ship.Dir, want)
	}
	if d := w.dirsFor("https://github.com/acme/widget", ""); !slices.Contains(d.checkouts(), want) {
		t.Errorf("the fleet's checkouts are %v, want %s among them though it is outside the search roots", d.checkouts(), want)
	}
	// A second launch there is a plain launch in a checkout of the fleet.
	second := w.launch(map[string]any{"fleet": "work", "harness": "claude", "dir": want, "skill": noSkill, "text": "again"})
	if second.Dir != want {
		t.Errorf("a launch with dir %s started in %q", want, second.Dir)
	}
}

// An existing folder at the target is never touched: the launch fails with a 409 and gh does not run.
func TestCloneLaunchRefusesAnExistingFolder(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	clones := w.mkdir(filepath.Join(w.work, "clones"))
	gh := w.startCloning(clones)
	w.launch(cloneLaunch(issueText("acme/control"))) // clone launches work, so a refusal isn't a missing route
	taken := w.mkdir(filepath.Join(clones, "widget"))
	keep := filepath.Join(taken, "keep.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := w.sessions()

	code, body := w.post(cloneLaunch(issueText("acme/widget")))

	if code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", code, body)
	}
	if msg := errorOf(t, body); !strings.Contains(msg, taken) {
		t.Errorf("error = %q, want it to name the existing folder %s", msg, taken)
	}
	if got, _ := os.ReadFile(keep); string(got) != "mine" || !slices.Equal(names(t, taken), []string{"keep.txt"}) {
		t.Errorf("the existing folder changed: keep.txt = %q, entries %v", got, names(t, taken))
	}
	if n := len(gh.cloneCalls()); n != 1 {
		t.Errorf("gh repo clone ran %d times, want only the control's", n)
	}
	if after := w.sessions(); !slices.Equal(before, after) {
		t.Errorf("a refused launch changed the sessions: before %v, after %v", before, after)
	}
}

// A gh failure fails the launch with gh's message, starts no ship and leaves no directory.
func TestCloneLaunchFailsWithGhsMessage(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	clones := w.mkdir(filepath.Join(w.work, "clones"))
	gh := w.startCloning(clones)
	w.launch(cloneLaunch(issueText("acme/control")))
	gh.write("repo_clone.fail", "GraphQL: Could not resolve to a Repository with the name 'acme/widget'.\n")
	before := w.sessions()

	code, body := w.post(cloneLaunch(issueText("acme/widget")))

	if code != http.StatusBadRequest && code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 400 or 502: %s", code, body)
	}
	msg := errorOf(t, body)
	if !strings.Contains(msg, "Could not resolve to a Repository") {
		t.Errorf("error = %q, want gh's message", msg)
	}
	if strings.Contains(msg, "\n") {
		t.Errorf("error = %q, want one line", msg)
	}
	if got := names(t, clones); !slices.Equal(got, []string{"control"}) {
		t.Errorf("%s holds %v, want only control: a failed clone leaves nothing behind", clones, got)
	}
	if after := w.sessions(); !slices.Equal(before, after) {
		t.Errorf("a failed clone started a ship: before %v, after %v", before, after)
	}
}

// An identity gh must not be asked to clone is refused before gh runs.
func TestCloneLaunchRefusesIdentitiesGhMustNotClone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	clones := w.mkdir(filepath.Join(w.work, "clones"))
	gh := w.startCloning(clones)
	w.launch(cloneLaunch(issueText("acme/control")))
	for name, text := range map[string]string{
		"a dash-led owner":  "https://github.com/-o/x/issues/1",
		"a dash-led repo":   "https://github.com/acme/-x",
		"dots for owner":    "https://github.com/../x",
		"dots for the repo": "https://github.com/acme/..",
		"a lone dot":        "https://github.com/acme/.",
		"a non-GitHub host": "https://gitlab.com/acme/widget",
		"no repo at all":    "nothing to clone here",
	} {
		t.Run(name, func(t *testing.T) {
			w := w.at(t)
			before := w.sessions()
			code, body := w.post(cloneLaunch(text))
			requireRefusal(t, code, body)
			if n := len(gh.cloneCalls()); n != 1 {
				t.Errorf("gh repo clone ran %d times, want only the control's", n)
			}
			if after := w.sessions(); !slices.Equal(before, after) {
				t.Errorf("a refused launch changed the sessions: before %v, after %v", before, after)
			}
			if got := names(t, clones); !slices.Equal(got, []string{"control"}) {
				t.Errorf("%s holds %v, want only control", clones, got)
			}
			if got := names(t, w.work); !slices.Equal(got, []string{"clones"}) {
				t.Errorf("the search root holds %v, want only clones: a name must not climb out of the clone root", got)
			}
		})
	}
}

// Two launches for one uncloned repo: one clones and launches, the other is refused as an existing folder.
func TestCloneLaunchRace(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	clones := w.mkdir(filepath.Join(w.work, "clones"))
	gh := w.startCloning(clones)
	gh.write("repo_clone.sleep", "1")

	type result struct {
		code int
		body string
		err  error
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, _ := json.Marshal(cloneLaunch(issueText("acme/widget")))
			resp, err := client().Post(w.in.LocalURL+"/api/ships", "application/json", bytes.NewReader(b))
			if err != nil {
				results[i] = result{err: err}
				return
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			results[i] = result{code: resp.StatusCode, body: string(data)}
		}()
	}
	wg.Wait()

	var created, refused int
	for _, r := range results {
		switch {
		case r.err != nil:
			t.Fatalf("POST /api/ships: %v", r.err)
		case r.code == http.StatusCreated:
			created++
		case r.code == http.StatusConflict:
			refused++
			errorOf(t, r.body)
		default:
			t.Errorf("status = %d, want 201 or 409: %s", r.code, r.body)
		}
	}
	if created != 1 || refused != 1 {
		t.Errorf("%d launches created and %d were refused, want 1 and 1: %+v", created, refused, results)
	}
	if got := w.sessions(); len(got) != 1 {
		t.Errorf("sessions = %v, want exactly one ship", got)
	}
	if got := names(t, clones); !slices.Equal(got, []string{"widget"}) {
		t.Errorf("%s holds %v, want only widget: the loser's temporary clone must be gone", clones, got)
	}
}
