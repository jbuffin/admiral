package stackmerge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

// upstream is a local repository standing in for the PR's GitHub repository, fetched by file URL.
type upstream struct {
	t   *testing.T
	dir string
}

func (u *upstream) git(args ...string) string {
	u.t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	c.Dir = u.dir
	c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + u.dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	out, err := c.CombinedOutput()
	if err != nil {
		u.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (u *upstream) commit(file, body, msg string) string {
	u.t.Helper()
	if err := os.WriteFile(filepath.Join(u.dir, file), []byte(body), 0o644); err != nil {
		u.t.Fatal(err)
	}
	u.git("add", file)
	u.git("commit", "-m", msg)
	return u.git("rev-parse", "HEAD")
}

// newUpstream has main with a shared.css of three lines.
func newUpstream(t *testing.T) *upstream {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	u := &upstream{t: t, dir: t.TempDir()}
	u.git("init", "-b", "main")
	u.commit("shared.css", "a\nb\nc\n", "base")
	return u
}

// pullHead makes refs/pull/<n>/head the commit at sha.
func (u *upstream) pullHead(n, sha string) { u.git("update-ref", "refs/pull/"+n+"/head", sha) }

func (u *upstream) checker(t *testing.T) *Git {
	return New(t.TempDir(), func(moor.StackCheck) string { return "file://" + u.dir })
}

func check(base string, pr int, head string) moor.StackCheck {
	return moor.StackCheck{Fleet: "work", Host: "github.com", Repo: "acme/widgets", Base: base, PR: pr, Head: head}
}

// stack builds a two-layer stack off main's first commit: the lower layer adds a file, the top rewrites line a of shared.css.
func (u *upstream) stack(topBody string) (top string) {
	u.git("checkout", "-b", "layer-1", "main")
	u.commit("one.txt", "1\n", "layer 1")
	top = u.commit("shared.css", topBody, "layer 2")
	u.git("checkout", "main")
	u.pullHead("8", top)
	return top
}

func TestAStackWhoseTopConflictsWithTheBaseAsAWholeIsReportedAsConflicting(t *testing.T) {
	u := newUpstream(t)
	top := u.stack("top\nb\nc\n")
	u.commit("shared.css", "main\nb\nc\n", "main moved")

	got, err := u.checker(t).Conflicts(context.Background(), check("main", 8, top))

	if err != nil || !got {
		t.Errorf("Conflicts = %v, %v, want true with no error", got, err)
	}
}

func TestAStackThatMergesCleanlyIntoTheMovedBaseIsNotConflicting(t *testing.T) {
	u := newUpstream(t)
	top := u.stack("top\nb\nc\n")
	u.commit("other.css", "x\n", "main moved elsewhere")

	got, err := u.checker(t).Conflicts(context.Background(), check("main", 8, top))

	if err != nil || got {
		t.Errorf("Conflicts = %v, %v, want false with no error", got, err)
	}
}

func TestTheAnswerFollowsTheBaseWhenItMovesBetweenChecks(t *testing.T) {
	u := newUpstream(t)
	top := u.stack("top\nb\nc\n")
	original := u.git("rev-parse", "main")
	u.commit("shared.css", "main\nb\nc\n", "main moved")
	c := u.checker(t)
	if got, err := c.Conflicts(context.Background(), check("main", 8, top)); err != nil || !got {
		t.Fatalf("the premise fails: Conflicts = %v, %v", got, err)
	}

	u.git("update-ref", "refs/heads/main", original)

	if got, err := c.Conflicts(context.Background(), check("main", 8, top)); err != nil || got {
		t.Errorf("Conflicts = %v, %v after the base moved back, want false with no error", got, err)
	}
}

func TestACheckThatCannotTellIsAnErrorNeverAnAnswer(t *testing.T) {
	u := newUpstream(t)
	top := u.stack("top\nb\nc\n")
	u.commit("shared.css", "main\nb\nc\n", "main moved")
	if got, err := u.checker(t).Conflicts(context.Background(), check("main", 8, top)); err != nil || !got {
		t.Fatalf("the premise fails: a good request answers %v, %v, want true with no error", got, err)
	}
	for name, req := range map[string]moor.StackCheck{
		"the PR's head is not the one asked about": check("main", 8, strings.Repeat("0", 40)),
		"the PR has no head ref":                   check("main", 99, top),
		"the base branch does not exist":           check("no-such-branch", 8, top),
		"a base that looks like an option":         check("--upload-pack=touch /tmp/pwned", 8, top),
		"an empty head":                            check("main", 8, ""),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := u.checker(t).Conflicts(context.Background(), req)

			if err == nil || got {
				t.Errorf("Conflicts = %v, %v, want false with an error: it could not tell", got, err)
			}
		})
	}
}

func TestACheckIgnoresTheGitConfigOfTheEnvironmentItRunsIn(t *testing.T) {
	u := newUpstream(t)
	top := u.stack("top\nb\nc\n")
	u.commit("shared.css", "main\nb\nc\n", "main moved")
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	// An ambient insteadOf would send the fetch to nowhere, and a credential helper or hook path would run a program.
	body := "[url \"file:///nowhere/\"]\n\tinsteadOf = file://" + u.dir + "\n[core]\n\thooksPath = /nowhere\n[credential]\n\thelper = !touch " + filepath.Join(t.TempDir(), "ran") + "\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("HOME", filepath.Dir(cfg))

	got, err := u.checker(t).Conflicts(context.Background(), check("main", 8, top))

	if err != nil || !got {
		t.Errorf("Conflicts = %v, %v, want true with no error: the ambient config must not reach git", got, err)
	}
}
