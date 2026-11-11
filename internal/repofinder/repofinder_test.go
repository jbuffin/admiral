package repofinder_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/repofinder"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// checkout makes a git checkout at root/rel with these remotes.
func checkout(t *testing.T, root, rel string, remotes ...string) string {
	t.Helper()
	d := filepath.Join(root, rel)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, d, "init", "-q")
	for i, r := range remotes {
		git(t, d, "remote", "add", "r"+string(rune('0'+i)), r)
	}
	return d
}

func paths(entries []repofinder.Entry) []string {
	p := []string{}
	for _, e := range entries {
		p = append(p, e.Path)
	}
	return p
}

func TestListFindsCheckoutsTwoLevelsDownThenTheRoots(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	a := checkout(t, root, "a", "git@github.com:acme/a.git")
	b := checkout(t, root, "grp/b", "https://github.com/acme/b")
	checkout(t, root, "a/inner")    // inside a checkout
	checkout(t, root, "d1/d2/deep") // three levels down
	if err := os.MkdirAll(filepath.Join(root, "plain", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, a, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(root, "wt")
	git(t, a, "worktree", "add", "-q", "-b", "wt", wt)

	got := repofinder.List([]string{root})
	if want := []string{a, b, wt, root}; !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("List = %v, want the checkouts then the root %v", paths(got), want)
	}
	if got[0].Kind != repofinder.KindCheckout || got[0].Name != "a" || got[3].Kind != repofinder.KindSearchRoot {
		t.Errorf("entries = %+v, want checkouts named by folder and the root last", got)
	}
	if !reflect.DeepEqual(got[2].Remotes, []string{"github.com/acme/a"}) {
		t.Errorf("worktree remotes = %v, want its main checkout's, read through the .git file", got[2].Remotes)
	}
}

func TestASearchRootThatIsACheckoutStaysARoot(t *testing.T) {
	root := checkout(t, t.TempDir(), "mono", "https://github.com/acme/mono.git")
	checkout(t, root, "vendored")
	got := repofinder.List([]string{root})
	if len(got) != 1 || got[0].Kind != repofinder.KindSearchRoot || !reflect.DeepEqual(got[0].Remotes, []string{"github.com/acme/mono"}) {
		t.Errorf("List = %+v, want only the root, with its remote, and nothing searched inside it", got)
	}
}

func TestIdentityMatchesByHostAndOwnerRepo(t *testing.T) {
	for _, c := range []struct{ url, want string }{
		{"git@GitHub.com:acme/one.git", "github.com/acme/one"},
		{"ssh://git@github.com/acme/one.git", "github.com/acme/one"},
		{"https://github.com/acme/y/pull/5", "github.com/acme/y"},
		{"https://github.com/acme/y/blob/main/img@2x.png", "github.com/acme/y"}, // an @ in the path is not userinfo
		{"https://gitlab.com/acme/three.git", "gitlab.com/acme/three"},
		{"https://oauth2:tok-secret-55@github.com/acme/five.git", "github.com/acme/five"},
		{"https://user:tok/secret-66@github.com/acme/six.git", "github.com/acme/six"}, // an unencoded / in the token
		{"just words", ""},
	} {
		got, ok := repofinder.Identity(c.url)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("Identity(%q) = %q, %v; want %q", c.url, got, ok, c.want)
		}
		if strings.Contains(got, "secret") || strings.Contains(got, "tok") {
			t.Errorf("Identity(%q) = %q carries a credential", c.url, got)
		}
	}
}

func TestURLsInTextInOrder(t *testing.T) {
	got := repofinder.URLs("see https://example.com/nope, then https://github.com/acme/y/issues/3. and git@github.com:acme/x.git")
	if want := []string{"github.com/acme/y", "github.com/acme/x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("URLs = %v, want %v", got, want)
	}
}

func TestContainsAnswersTheCleanedPath(t *testing.T) {
	root := t.TempDir()
	a := checkout(t, root, "a")
	if dir, ok := repofinder.Contains(repofinder.List([]string{root}), a+"/"); !ok || dir != a {
		t.Errorf("Contains(%q) = %q, %v; want %q, true", a+"/", dir, ok, a)
	}
}
