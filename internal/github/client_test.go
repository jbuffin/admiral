package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// fakeGH drives e2e/testdata/fakegh/gh through $FAKEGH_DIR.
type fakeGH struct {
	dir string
	env []string
}

func newFake(t *testing.T) *fakeGH {
	t.Helper()
	bin, err := filepath.Abs("../../e2e/testdata/fakegh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	env := append(os.Environ(), "FAKEGH_DIR="+dir, "GH_TOKEN=inherited-token")
	return &fakeGH{dir: dir, env: env}
}

func (f *fakeGH) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(f.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// answer makes every search answer with the given files (json, fail, exit).
func (f *fakeGH) answer(t *testing.T, files map[string]string) {
	t.Helper()
	f.write(t, "search/all.match", "query=")
	for ext, body := range files {
		f.write(t, "search/all."+ext, body)
	}
}

// call is one recorded gh call.
type call struct {
	argv []string
	env  string
}

func (f *fakeGH) calls(t *testing.T, first string) []call {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(f.dir, "calls", "call.*"))
	var out []call
	for _, d := range dirs {
		raw, err := os.ReadFile(filepath.Join(d, "argv"))
		if err != nil {
			t.Fatal(err)
		}
		argv := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
		if argv[0] != first {
			continue
		}
		env, _ := os.ReadFile(filepath.Join(d, "env"))
		out = append(out, call{argv: argv, env: string(env)})
	}
	return out
}

func (f *fakeGH) only(t *testing.T, first string) call {
	t.Helper()
	cs := f.calls(t, first)
	if len(cs) != 1 {
		t.Fatalf("want one gh %s call, got %d", first, len(cs))
	}
	return cs[0]
}

func TestTokenArgsAndEnv(t *testing.T) {
	cases := []struct {
		name, host, account, file string
		wantArgs                  string
	}{
		{"active account", "github.com", "", "token", "auth token -h github.com"},
		{"named account on another host", "ghe.example.com", "work", "token-work", "auth token -h ghe.example.com -u work"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t)
			f.write(t, c.file, "fake-token-work-2\n")
			cl := NewClient(c.host, c.account, nil, f.env)
			got, err := cl.Token(context.Background())
			if err != nil || got != "fake-token-work-2" {
				t.Fatalf("Token() = %q, %v; want fake-token-work-2", got, err)
			}
			call := f.only(t, "auth")
			if args := strings.Join(call.argv, " "); args != c.wantArgs {
				t.Errorf("argv = %q, want %q", args, c.wantArgs)
			}
			if strings.Contains(call.env, "GH_TOKEN") {
				t.Errorf("an inherited GH_TOKEN reached gh auth token: %q", call.env)
			}
			for _, want := range []string{"GH_NO_UPDATE_NOTIFIER=1", "GH_PROMPT_DISABLED=1"} {
				if !strings.Contains(call.env, want) {
					t.Errorf("env lacks %s: %q", want, call.env)
				}
			}
		})
	}
}

func TestTokenErrors(t *testing.T) {
	t.Run("signed out", func(t *testing.T) {
		f := newFake(t)
		_, err := NewClient("ghe.example.com", "", nil, f.env).Token(context.Background())
		var si *SignInError
		if !errors.As(err, &si) || err.Error() != "Run gh auth login -h ghe.example.com" {
			t.Fatalf("err = %v, want a SignInError saying Run gh auth login -h ghe.example.com", err)
		}
	})
	t.Run("gh missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := NewClient("github.com", "", nil, os.Environ()).Token(context.Background())
		var si *SignInError
		if !errors.As(err, &si) || !strings.Contains(err.Error(), "Install the GitHub CLI") || !strings.Contains(err.Error(), "gh auth login") {
			t.Fatalf("err = %v, want a SignInError telling to install gh and run gh auth login", err)
		}
	})
	t.Run("timeout is not a sign-in error", func(t *testing.T) {
		dir := hangingGH(t)
		cl := NewClient("github.com", "", nil, os.Environ())
		cl.Timeout = 300 * time.Millisecond
		_ = dir
		_, err := cl.Token(context.Background())
		var si *SignInError
		if err == nil || errors.As(err, &si) || err.Error() != "Couldn't reach github.com" {
			t.Fatalf("err = %v, want plain \"Couldn't reach github.com\"", err)
		}
	})
}

// hangingGH puts a gh that never answers first on PATH.
func hangingGH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestSearchRequest(t *testing.T) {
	f := newFake(t)
	f.answer(t, map[string]string{"json": `{"data":{"search":{"nodes":[]}}}`})
	cl := NewClient("acme.ghe.com", "", nil, f.env)
	if _, err := cl.Search(context.Background(), "fake-token-work-2", "is:open assignee:@me"); err != nil {
		t.Fatal(err)
	}
	c := f.only(t, "api")
	if got := strings.Join(c.argv[:4], " "); got != "api graphql --hostname acme.ghe.com" {
		t.Errorf("argv starts %q", got)
	}
	if c.argv[4] != "-f" || !strings.HasPrefix(c.argv[5], "query=") || c.argv[6] != "-f" || c.argv[7] != "q=is:open assignee:@me sort:updated-desc" {
		t.Errorf("argv = %q", c.argv)
	}
	for _, a := range c.argv {
		if strings.Contains(a, "fake-token-work-2") || a == "-F" {
			t.Errorf("argv carries the token or -F: %q", c.argv)
		}
	}
}

func TestSearchSortRule(t *testing.T) {
	cases := []struct{ query, want string }{
		{"is:open", "is:open sort:updated-desc"},
		{"SORT:updated-asc is:open", "SORT:updated-asc is:open"},
		{"-sort:updated is:open", "-sort:updated is:open"},
		{"is:open resort:x", "is:open resort:x sort:updated-desc"},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			f := newFake(t)
			f.answer(t, map[string]string{"json": `{"data":{"search":{"nodes":[]}}}`})
			if _, err := NewClient("github.com", "", nil, f.env).Search(context.Background(), "fake-token-work-2", c.query); err != nil {
				t.Fatal(err)
			}
			if got := f.only(t, "api").argv[7]; got != "q="+c.want {
				t.Errorf("q arg = %q, want %q", got, "q="+c.want)
			}
		})
	}
}

func node(repo string, n int, labels ...string) string {
	var ls []string
	for _, l := range labels {
		ls = append(ls, fmt.Sprintf(`{"name":%q}`, l))
	}
	return fmt.Sprintf(`{"number":%d,"title":"Title %d","url":"https://github.com/%s/issues/%d","repository":{"nameWithOwner":%q,"url":"https://github.com/%s"},"labels":{"nodes":[%s]}}`,
		n, n, repo, n, repo, repo, strings.Join(ls, ","))
}

func TestSearchItems(t *testing.T) {
	f := newFake(t)
	body := `{"data":{"search":{"nodes":[` + node("acme/widgets", 7, "bug", "P0") + `,{},` + node("acme/gears", 8, "docs") + `]}}}`
	f.answer(t, map[string]string{"json": body})
	got, err := NewClient("github.com", "", []string{"p0", "urgent"}, f.env).Search(context.Background(), "fake-token-work-2", "is:open")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2 (the empty node skipped): %+v", len(got), got)
	}
	want := Item{ID: "acme/widgets#7", Title: "Title 7", Text: "https://github.com/acme/widgets/issues/7",
		URL: "https://github.com/acme/widgets/issues/7", RepoURL: "https://github.com/acme/widgets",
		Repo: "acme/widgets", Labels: []string{"bug", "P0"}, Hot: true}
	if fmt.Sprint(got[0]) != fmt.Sprint(want) {
		t.Errorf("item 0 = %+v, want %+v", got[0], want)
	}
	if got[1].ID != "acme/gears#8" || got[1].Hot {
		t.Errorf("item 1 = %+v, want acme/gears#8, not hot", got[1])
	}
}

func TestSearchCapsAtFifty(t *testing.T) {
	f := newFake(t)
	var nodes []string
	for i := 1; i <= 60; i++ {
		nodes = append(nodes, node("acme/widgets", i))
	}
	f.answer(t, map[string]string{"json": `{"data":{"search":{"nodes":[` + strings.Join(nodes, ",") + `]}}}`})
	got, err := NewClient("github.com", "", nil, f.env).Search(context.Background(), "fake-token-work-2", "is:open")
	if err != nil || len(got) != 50 {
		t.Fatalf("got %d items, %v; want 50", len(got), err)
	}
}

func TestSearchErrors(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"stderr redacts the token and drops gh:", map[string]string{"fail": "gh: bad credentials fake-token-work-2 rejected\n"}, "Search failed: bad credentials *** rejected"},
		{"empty stderr", map[string]string{"fail": "", "exit": "3"}, "Search failed: gh exited with status 3"},
		{"unreachable", map[string]string{"fail": "error connecting to api.github.com\n"}, "Couldn't reach github.com"},
		{"unreachable after redaction", map[string]string{"fail": "dial tcp: lookup fake-token-work-2: i/o timeout"}, "Couldn't reach github.com"},
		{"not JSON", map[string]string{"json": "oops"}, "Search failed: GitHub's answer was not JSON"},
		{"not an object", map[string]string{"json": "[]"}, "Search failed: GitHub's answer was not understood"},
		{"token in a graphql message is redacted", map[string]string{"json": `{"errors":[{"message":"bad fake-token-work-2"}]}`}, "Search failed: bad ***"},
		{"null nodes", map[string]string{"json": `{"data":{"search":{"nodes":null}}}`}, "Search failed: GitHub's answer was not understood"},
		{"graphql errors", map[string]string{"json": `{"errors":[{"message":"Bad query"},{"message":"second"}]}`}, "Search failed: Bad query"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t)
			f.answer(t, c.files)
			_, err := NewClient("github.com", "", nil, f.env).Search(context.Background(), "fake-token-work-2", "is:open")
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	t.Run("gh missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := NewClient("github.com", "", nil, os.Environ()).Search(context.Background(), "fake-token-work-2", "is:open")
		if err == nil || err.Error() != "Couldn't reach github.com" {
			t.Fatalf("err = %v, want Couldn't reach github.com", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		hangingGH(t)
		cl := NewClient("github.com", "", nil, os.Environ())
		cl.Timeout = 300 * time.Millisecond
		_, err := cl.Search(context.Background(), "fake-token-work-2", "is:open")
		if err == nil || err.Error() != "Couldn't reach github.com" {
			t.Fatalf("err = %v, want Couldn't reach github.com", err)
		}
	})
}

func TestSearchTokenVariable(t *testing.T) {
	cases := []struct{ host, want string }{
		{"github.com", "GH_TOKEN"},
		{"acme.ghe.com", "GH_TOKEN"},
		{"ghe.example.com", "GH_ENTERPRISE_TOKEN"},
	}
	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			f := newFake(t)
			f.env = append(f.env, "GITHUB_TOKEN=a", "GH_ENTERPRISE_TOKEN=b", "GITHUB_ENTERPRISE_TOKEN=c")
			f.answer(t, map[string]string{"json": `{"data":{"search":{"nodes":[]}}}`})
			if _, err := NewClient(c.host, "", nil, f.env).Search(context.Background(), "fake-token-work-2", "is:open"); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, line := range strings.Fields(f.only(t, "api").env) {
				if strings.Contains(line, "TOKEN") {
					got = append(got, line)
				}
			}
			if want := c.want + "=fake-token-work-2"; len(got) != 1 || got[0] != want {
				t.Errorf("token variables = %q, want only %s", got, want)
			}
		})
	}
}

func TestCancelledContext(t *testing.T) {
	hangingGH(t)
	cl := NewClient("github.com", "", nil, os.Environ())
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	if _, err := cl.Token(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Token err = %v, want context.Canceled", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	if _, err := cl.Search(ctx, "fake-token-work-2", "is:open"); !errors.Is(err, context.Canceled) {
		t.Errorf("Search err = %v, want context.Canceled", err)
	}
}

func TestGhFoundOnEnvPath(t *testing.T) {
	f := newFake(t)
	f.write(t, "token", "fake-token-work-2\n")
	bin, _ := filepath.Abs("../../e2e/testdata/fakegh")
	t.Setenv("PATH", t.TempDir()) // Admiral's PATH has no gh; the fleet's does
	env := append(f.env, "PATH="+bin+":/usr/bin:/bin")
	if got, err := NewClient("github.com", "", nil, env).Token(context.Background()); err != nil || got != "fake-token-work-2" {
		t.Fatalf("Token() = %q, %v", got, err)
	}
}

func TestHostAccessor(t *testing.T) {
	if got := NewClient("ghe.example.com", "", nil, nil).Host(); got != "ghe.example.com" {
		t.Errorf("Host() = %q", got)
	}
}

// settings decodes JSON the way the server hands it to a source.
func settings(t *testing.T, js string) Settings {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatal(err)
	}
	return ParseSettings(m)
}

func ids(vs []View) string {
	var out []string
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return strings.Join(out, ",")
}

func TestViewsDefaultsAndSlugs(t *testing.T) {
	d := settings(t, `{}`)
	if ids(d.Views) != "assigned-to-me,review-requested,my-prs" {
		t.Fatalf("default view ids = %s", ids(d.Views))
	}
	wantQ := []string{"is:open assignee:@me", "is:open is:pr review-requested:@me", "is:open is:pr author:@me"}
	for i, v := range d.Views {
		if v.Query != wantQ[i] {
			t.Errorf("default %s query = %q, want %q", v.ID, v.Query, wantQ[i])
		}
	}

	s := settings(t, `{"views":[
		{"title":" Bugs! ","query":" q1 "},
		{"title":"bugs","query":"q2"},
		{"title":"  ","query":"dropped"},
		{"title":"no query"},
		"not an object",
		{"title":"!!!","query":"q3"},
		{"title":"Bugs","query":"q4"}]}`)
	if got := ids(s.Views); got != "assigned-to-me,review-requested,my-prs,bugs,bugs-2,panel,bugs-3" {
		t.Fatalf("view ids = %s", got)
	}
	if v := s.Views[3]; v.Title != "Bugs!" || v.Query != "q1" {
		t.Errorf("custom view not trimmed: %+v", v)
	}
}

func TestCustomReplacesDefaultByID(t *testing.T) {
	s := settings(t, `{"views":[
		{"title":"My PRs","query":"is:pr author:@me label:mine"},
		{"title":"Assigned to me","query":"is:open assignee:@me label:x"},
		{"title":"assigned to me","query":"second"}]}`)
	if got := ids(s.Views); got != "assigned-to-me,review-requested,my-prs,assigned-to-me-2" {
		t.Fatalf("view ids = %s", got)
	}
	if v := s.Views[0]; v.Query != "is:open assignee:@me label:x" {
		t.Errorf("Assigned to me not replaced in place: %+v", v)
	}
	if v := s.Views[2]; v.Title != "My PRs" || v.Query != "is:pr author:@me label:mine" {
		t.Errorf("My PRs not replaced in place: %+v", v)
	}
}

func TestSettingsScalars(t *testing.T) {
	d := settings(t, `{}`)
	if d.Host != "github.com" || d.Account != "" || d.RefreshMinutes != 5 {
		t.Errorf("defaults = %+v", d)
	}
	if got := strings.Join(sorted(d.HotLabels), ","); got != "p0,p1,priority: high,urgent" {
		t.Errorf("default hot labels = %s", got)
	}
	s := settings(t, `{"host":"  ghe.example.com ","account":" work ","hotLabels":[" Blocker ","",5,"URGENT"]}`)
	if s.Host != "ghe.example.com" || s.Account != "work" {
		t.Errorf("host/account = %q/%q", s.Host, s.Account)
	}
	if got := strings.Join(sorted(s.HotLabels), ","); got != "blocker,urgent" {
		t.Errorf("hot labels = %s", got)
	}
	if got := strings.Join(sorted(settings(t, `{"hotLabels":["",5]}`).HotLabels), ","); got != "p0,p1,priority: high,urgent" {
		t.Errorf("hot labels with nothing usable = %s, want the defaults", got)
	}
	minutes := map[string]int{`7`: 7, `0`: 5, `99999`: 1440, `true`: 5, `2.5`: 5}
	for in, want := range minutes {
		if got := settings(t, `{"refreshMinutes":`+in+`}`).RefreshMinutes; got != want {
			t.Errorf("refreshMinutes %s = %d, want %d", in, got, want)
		}
		// the server decodes with UseNumber
		dec := json.NewDecoder(strings.NewReader(`{"refreshMinutes":` + in + `}`))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		if got := ParseSettings(m).RefreshMinutes; got != want {
			t.Errorf("json.Number refreshMinutes %s = %d, want %d", in, got, want)
		}
	}
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestMergePRThatRunsPastItsLimitIsAnUnknownResult(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = auth ]; then echo fake-token-work-2; exit 0; fi\nexec sleep 5\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := MergeTimeout
	MergeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { MergeTimeout = old })
	cl := NewClient("github.com", "", nil, os.Environ())
	err := cl.MergePR(context.Background(), "https://github.com/acme/widgets/pull/7", "squash")
	if !errors.Is(err, moor.ErrMergeTimeout) {
		t.Fatalf("MergePR = %v, want moor.ErrMergeTimeout", err)
	}
}

func TestStackCallsPinTheRepoAndNeutraliseTheCheckoutsGitConfig(t *testing.T) {
	f := newFake(t)
	f.write(t, "token", "fake-token-work-2\n")
	f.write(t, "stack_view.json", `{"branches":[{"head":"abc","pr":{"number":7,"url":"https://ghe.example.com/acme/widgets/pull/7"}}]}`)
	cl := NewClient("ghe.example.com", "", nil, append(f.env, "GH_REPO=evil.example/x/y", "GH_HOST=evil.example"))
	dir := t.TempDir()
	layers, err := cl.StackPRs(context.Background(), dir, "acme/widgets")
	if err != nil || len(layers) != 1 || layers[0] != (moor.StackLayer{URL: "https://ghe.example.com/acme/widgets/pull/7", Number: 7, Head: "abc"}) {
		t.Fatalf("StackPRs = %+v, %v; want the one layer with its number and head", layers, err)
	}
	if err := cl.MergeStack(context.Background(), dir, "acme/widgets", "squash"); err != nil {
		t.Fatal(err)
	}
	cs := f.calls(t, "stack")
	if len(cs) != 2 {
		t.Fatalf("want a stack view and a stack merge, got %d calls", len(cs))
	}
	for _, c := range cs {
		for _, want := range []string{
			"GH_HOST=ghe.example.com\n", "GH_REPO=ghe.example.com/acme/widgets\n", "GIT_CONFIG_COUNT=3\n",
			"GIT_CONFIG_KEY_0=core.fsmonitor\n", "GIT_CONFIG_VALUE_0=false\n",
			"GIT_CONFIG_KEY_1=core.hooksPath\n", "GIT_CONFIG_VALUE_1=/dev/null\n",
			"GIT_CONFIG_KEY_2=core.sshCommand\n", "GIT_CONFIG_VALUE_2=\n",
		} {
			if !strings.Contains(c.env+"\n", want) {
				t.Errorf("gh %v env lacks %q: %q", c.argv, strings.TrimSpace(want), c.env)
			}
		}
		if strings.Contains(c.env, "evil.example") {
			t.Errorf("gh %v kept an inherited host or repo: %q", c.argv, c.env)
		}
	}
}

// prSearchNode is a search node for a pull request with GitHub's state fields; "" is null.
func prSearchNode(repo string, n int, state, review, checks, mergeable, mergeState string) string {
	opt := func(v string) string {
		if v == "" {
			return "null"
		}
		return fmt.Sprintf("%q", v)
	}
	rollup := "null"
	if checks != "" {
		rollup = fmt.Sprintf(`{"state":%q}`, checks)
	}
	return fmt.Sprintf(`{"number":%d,"title":"PR %d","url":"https://github.com/%s/pull/%d","repository":{"nameWithOwner":%q,"url":"https://github.com/%s"},"labels":{"nodes":[]},`+
		`"state":%q,"reviewDecision":%s,"mergeable":%s,"mergeStateStatus":%s,"statusCheckRollup":%s}`,
		n, n, repo, n, repo, repo, state, opt(review), opt(mergeable), opt(mergeState), rollup)
}

func TestSearchGivesAPullRequestTheStateThePollGivesForTheSameAnswer(t *testing.T) {
	for _, c := range []struct {
		name                                         string
		state, review, checks, mergeable, mergeState string
		ready, conflicts                             bool
	}{
		{"ready", "OPEN", "APPROVED", "SUCCESS", "MERGEABLE", "CLEAN", true, false},
		{"review required", "OPEN", "REVIEW_REQUIRED", "SUCCESS", "MERGEABLE", "BLOCKED", false, false},
		{"checks failing", "OPEN", "APPROVED", "FAILURE", "MERGEABLE", "UNSTABLE", false, false},
		{"conflicting", "OPEN", "APPROVED", "SUCCESS", "CONFLICTING", "DIRTY", false, true},
		{"approved and green but blocked by its base", "OPEN", "APPROVED", "SUCCESS", "MERGEABLE", "BLOCKED", false, false},
		{"merged", "MERGED", "APPROVED", "SUCCESS", "CONFLICTING", "CLEAN", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t)
			f.write(t, "token", "fake-token-work-2\n")
			f.answer(t, map[string]string{"json": `{"data":{"search":{"nodes":[` +
				prSearchNode("acme/widgets", 7, c.state, c.review, c.checks, c.mergeable, c.mergeState) + `]}}}`})
			cl := NewClient("github.com", "", nil, f.env)
			items, err := cl.Search(context.Background(), "fake-token-work-2", "is:open is:pr author:@me")
			if err != nil || len(items) != 1 {
				t.Fatalf("Search = %+v, %v; want one item", items, err)
			}
			if items[0].PR == nil {
				t.Fatalf("a pull request item has no PR state: %+v", items[0])
			}
			if got := *items[0].PR; got.State != c.state || got.Review != c.review || got.Checks != c.checks ||
				got.Mergeable != c.mergeable || got.Ready != c.ready || got.Conflicts != c.conflicts {
				t.Errorf("search PR = %+v, want state %s review %q checks %q mergeable %s ready %v conflicts %v",
					got, c.state, c.review, c.checks, c.mergeable, c.ready, c.conflicts)
			}

			// The moor poll, given the same GitHub answer, serves the same values.
			const url = "https://github.com/acme/widgets/pull/7"
			f.pr(t, url, c.state, c.review, c.checks, c.mergeable)
			f.prEvents(t, url, `{"mergeStateStatus":"`+c.mergeState+`"}`)
			polled, err := cl.PullRequests(context.Background(), []string{url})
			if err != nil {
				t.Fatalf("PullRequests: %v", err)
			}
			got, want := *items[0].PR, moor.View(url, polled[url], true)
			got.URL = url // the order carries its own url
			if got != want {
				t.Errorf("search PR = %+v, the poll's = %+v: they must agree", got, want)
			}
		})
	}
}

func TestSearchGivesAnIssueNoPRState(t *testing.T) {
	f := newFake(t)
	f.answer(t, map[string]string{"json": `{"data":{"search":{"nodes":[` + node("acme/widgets", 7, "bug") + `]}}}`})
	items, err := NewClient("github.com", "", nil, f.env).Search(context.Background(), "fake-token-work-2", "is:open")
	if err != nil || len(items) != 1 {
		t.Fatalf("Search = %+v, %v; want one item", items, err)
	}
	if items[0].PR != nil {
		t.Errorf("an issue carries PR state %+v, want none", *items[0].PR)
	}
}

func TestSearchAsksForOnlyThePullRequestFieldsTheChipsRead(t *testing.T) {
	f := newFake(t)
	f.answer(t, map[string]string{"json": `{"data":{"search":{"nodes":[]}}}`})
	if _, err := NewClient("github.com", "", nil, f.env).Search(context.Background(), "fake-token-work-2", "is:open"); err != nil {
		t.Fatal(err)
	}
	q := strings.Join(f.only(t, "api").argv, " ")
	for _, want := range []string{"reviewDecision", "mergeable", "mergeStateStatus", "statusCheckRollup"} {
		if !strings.Contains(q, want) {
			t.Errorf("the search asks for no %s: %s", want, q)
		}
	}
	for _, heavy := range []string{"reviews(", "reviewThreads", "isRequired"} {
		if strings.Contains(q, heavy) {
			t.Errorf("the search asks for %s: it would make every search heavy", heavy)
		}
	}
}
