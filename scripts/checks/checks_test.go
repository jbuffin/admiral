// The per-rule check scripts, the runner that runs them and the check count history, against fixture git repos.
package checks_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type result struct {
	stdout, stderr string
	code           int
}

// repoRoot is the worktree these scripts live in, two levels above this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..")
}

// env isolates git from the engineer's config and from an enclosing repo's variables.
func env(date string) []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			out = append(out, kv)
		}
	}
	return append(out,
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
}

const midday = "2026-03-01T12:00:00+0000"

func exec1(t *testing.T, dir, date, name string, args ...string) result {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env(date)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := result{stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		res.code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return res
}

func git(t *testing.T, dir, date string, args ...string) {
	t.Helper()
	if res := exec1(t, dir, date, "git", args...); res.code != 0 {
		t.Fatalf("git %v: exit %d\n%s", args, res.code, res.stderr)
	}
}

// run runs a script of this worktree with sh, from dir as its working directory.
func run(t *testing.T, dir, script string) result {
	t.Helper()
	return exec1(t, dir, midday, "sh", filepath.Join(repoRoot(t), script))
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixtureRepo is a fresh git repo whose files are staged, which is all git grep reads.
func fixtureRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, midday, "init", "-q", "-b", "main")
	for rel, content := range files {
		write(t, dir, rel, content)
	}
	git(t, dir, midday, "add", "-A", "-f")
	return dir
}

// commitAt writes and removes files, then commits them with the given author date.
func commitAt(t *testing.T, dir, date string, files map[string]string, remove ...string) {
	t.Helper()
	for rel, content := range files {
		write(t, dir, rel, content)
	}
	git(t, dir, date, "add", "-A", "-f")
	for _, rel := range remove {
		git(t, dir, date, "rm", "-q", "--", rel)
	}
	git(t, dir, date, "commit", "-q", "-m", "change")
}

type check struct {
	name, hint string
	bad        map[string]string
	want       string
	clean      map[string]string
}

var checkCases = []check{
	{
		name: "issue-number-in-comment",
		hint: "issue numbers belong in the commit or PR, not a comment",
		bad:  map[string]string{"web/src/a.ts": "// ok\n// fixes #" + "12\n"},
		want: "web/src/a.ts:2:",
		clean: map[string]string{"web/src/a.ts": strings.Join([]string{
			"// see https://x.test/page#" + "12", // after a slash
			"// &#" + "39;",                      // after an ampersand
			"// a#" + "12",                       // after a letter
			"// see #" + "12a",                   // no word end
			"",
		}, "\n")},
	},
	{
		name:  "playwright-fixed-wait",
		hint:  "wait for what the page shows, not a fixed time",
		bad:   map[string]string{"web/e2e/a.spec.ts": "await page.goto('/');\nawait page.waitForTimeout(500);\n"},
		want:  "web/e2e/a.spec.ts:2:",
		clean: map[string]string{"web/e2e/a.spec.ts": "await page.goto('/');\n", "web/src/x.ts": "page.waitForTimeout(500);\n"},
	},
	{
		name: "playwright-own-browser",
		hint: "import test from ./fixtures, which silences the browser's audio, and open pages through it",
		bad:  map[string]string{"web/e2e/a.spec.ts": "// ok\nimport { test } from '@playwright/test';\n"},
		want: "web/e2e/a.spec.ts:2:",
		clean: map[string]string{
			"web/e2e/fixtures.ts": "import { test as base, webkit } from '@playwright/test';\nawait webkit.launch();\n",
			"web/e2e/a.spec.ts":   "import { test } from './fixtures';\n",
		},
	},
	{
		name:  "e2e-whole-second-sleep",
		hint:  "poll for the change instead of sleeping whole seconds",
		bad:   map[string]string{"e2e/a_test.go": "package e2e\n\nfunc f() { time.Sleep(2 * time.Second) }\n"},
		want:  "e2e/a_test.go:3:",
		clean: map[string]string{"e2e/a_test.go": "package e2e\n\nfunc f() { time.Sleep(50 * time.Millisecond) }\n", "internal/x.go": "package x\n\nfunc f() { time.Sleep(time.Second) }\n"},
	},
	{
		name: "probes-executable-write",
		hint: "write an executable fake with writeExe (exe_test.go); an in-process write fd makes the exec fail with text file busy on Linux",
		bad:  map[string]string{"internal/probes/a_test.go": "package probes_test\n\nvar _ = os.WriteFile(p, b, 0o755)\n"},
		want: "internal/probes/a_test.go:3:",
		clean: map[string]string{
			"internal/probes/exe_test.go": "package probes_test\n\nvar _ = os.WriteFile(p, b, 0o755)\n",
			"internal/probes/a_test.go":   "package probes_test\n\nvar _ = os.WriteFile(p, b, 0o644)\n",
			"internal/ships/a_test.go":    "package ships\n\nvar _ = os.WriteFile(p, b, 0o755)\n",
		},
	},
	{
		name: "gofmt",
		hint: "run gofmt -w on these files",
		bad:  map[string]string{"internal/a b.go": "package a\n\nfunc f( ) {}\n"},
		want: "internal/a b.go",
		clean: map[string]string{
			"internal/a.go": "package a\n\nfunc f() {}\n",
			"web/notes.txt": "func f( ) {}\n",
		},
	},
}

func TestCheckFailsOnABreak(t *testing.T) {
	for _, c := range checkCases {
		t.Run(c.name, func(t *testing.T) {
			res := run(t, fixtureRepo(t, c.bad), "scripts/checks/"+c.name+".sh")
			if res.code != 1 {
				t.Fatalf("exit %d, want 1\nstderr: %s", res.code, res.stderr)
			}
			if first, _, _ := strings.Cut(res.stderr, "\n"); first != c.name+": "+c.hint {
				t.Errorf("first stderr line %q, want %q", first, c.name+": "+c.hint)
			}
			if !strings.Contains(res.stderr, c.want) {
				t.Errorf("stderr lacks the hit %q:\n%s", c.want, res.stderr)
			}
		})
	}
}

func TestCheckPassesQuietlyOnNearMisses(t *testing.T) {
	for _, c := range checkCases {
		t.Run(c.name, func(t *testing.T) {
			res := run(t, fixtureRepo(t, c.clean), "scripts/checks/"+c.name+".sh")
			if res.code != 0 || res.stdout != "" || res.stderr != "" {
				t.Fatalf("exit %d, want 0 and no output\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
			}
		})
	}
}

func TestCheckOutsideAGitRepoFailsLoudly(t *testing.T) {
	for _, c := range checkCases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command("sh", filepath.Join(repoRoot(t), "scripts/checks/"+c.name+".sh"))
			cmd.Dir = dir
			cmd.Env = append(env(midday), "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
			var stderr strings.Builder
			cmd.Stderr = &stderr
			var exit *exec.ExitError
			if err := cmd.Run(); !errors.As(err, &exit) || exit.ExitCode() != 2 {
				t.Fatalf("got %v, want exit 2", err)
			}
			if stderr.Len() == 0 {
				t.Error("no git message on stderr")
			}
		})
	}
}

// runnerRepo is a fixture holding this worktree's runner and checks, so the real files are tested.
func runnerRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := fixtureRepo(t, files)
	root := repoRoot(t)
	scripts, err := filepath.Glob(filepath.Join(root, "scripts", "checks", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) < len(checkCases) {
		t.Fatalf("scripts/checks holds %d checks, want the %d rules split out", len(scripts), len(checkCases))
	}
	for _, src := range append(scripts, filepath.Join(root, "scripts", "lint-comments.sh")) {
		body, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, src)
		if err != nil {
			t.Fatal(err)
		}
		write(t, dir, rel, string(body))
	}
	return dir
}

func TestRunnerFailsAndPrintsEveryFailingHint(t *testing.T) {
	dir := runnerRepo(t, map[string]string{
		"web/e2e/a.spec.ts": "await page.waitForTimeout(500);\n",
		"e2e/a_test.go":     "package e2e\n\nfunc f() { time.Sleep(time.Second) }\n",
	})
	res := exec1(t, dir, midday, "sh", "scripts/lint-comments.sh")
	if res.code != 1 {
		t.Fatalf("exit %d, want 1", res.code)
	}
	for _, want := range []string{
		"playwright-fixed-wait: wait for what the page shows, not a fixed time",
		"e2e-whole-second-sleep: poll for the change instead of sleeping whole seconds",
	} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, res.stderr)
		}
	}
}

func TestRunnerPassesOnACleanRepo(t *testing.T) {
	dir := runnerRepo(t, map[string]string{"web/e2e/a.spec.ts": "await page.goto('/');\n"})
	res := exec1(t, dir, midday, "sh", "scripts/lint-comments.sh")
	if res.code != 0 || res.stdout != "" || res.stderr != "" {
		t.Fatalf("exit %d, want 0 and no output\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
}

func TestRunnerPicksUpANewCheckFile(t *testing.T) {
	dir := runnerRepo(t, map[string]string{"web/e2e/a.spec.ts": "await page.goto('/');\n"})
	write(t, dir, "scripts/checks/always-fails.sh", "echo 'always-fails: a dropped-in check ran' >&2\nexit 1\n")
	res := exec1(t, dir, midday, "sh", "scripts/lint-comments.sh")
	if res.code != 1 || !strings.Contains(res.stderr, "always-fails: a dropped-in check ran") {
		t.Fatalf("exit %d, want 1 with the new check's message\nstderr: %s", res.code, res.stderr)
	}
}

// historyRepo builds the commits the history script reads and returns the repo.
func historyRepo(t *testing.T) string {
	t.Helper()
	dir := fixtureRepo(t, nil)
	commitAt(t, dir, "2026-03-01T12:00:00+0000", map[string]string{
		"scripts/checks/a.sh": "a\n", "scripts/checks/b.sh": "b\n", "scripts/checks/checks_test.go": "package checks_test\n",
	})
	commitAt(t, dir, "2026-03-02T12:00:00+0000", map[string]string{"web/x.txt": "x\n"})
	commitAt(t, dir, "2026-03-03T12:00:00+0000", map[string]string{
		"scripts/checks/a.sh": "a2\n", "scripts/checks/sub/d.sh": "d\n",
	})
	// 2026-03-05 in UTC, so the date shows the author's own offset.
	commitAt(t, dir, "2026-03-04T23:30:00-0800", map[string]string{"scripts/checks/c.sh": "c\n"})
	commitAt(t, dir, "2026-03-05T12:00:00+0000", nil, "scripts/checks/b.sh")
	git(t, dir, midday, "checkout", "-q", "-b", "side")
	commitAt(t, dir, "2026-03-06T12:00:00+0000", map[string]string{"scripts/checks/e.sh": "e\n"})
	git(t, dir, midday, "checkout", "-q", "main")
	git(t, dir, "2026-03-07T12:00:00+0000", "merge", "-q", "--no-ff", "-m", "merge side", "side")
	return dir
}

func TestHistoryPrintsOneLinePerCheckCommitOldestFirst(t *testing.T) {
	res := run(t, historyRepo(t), "scripts/checks-history.sh")
	want := strings.Join([]string{
		"2026-03-01 2", // a commit adding two checks
		"2026-03-03 2", // an edit and a nested script change nothing
		"2026-03-04 3", // a third check, dated in the author's offset
		"2026-03-05 2", // a deletion
		"2026-03-07 3", // a merge shows on the first-parent line only
		"",
	}, "\n")
	if res.code != 0 || res.stdout != want {
		t.Fatalf("exit %d\ngot:\n%swant:\n%s\nstderr: %s", res.code, res.stdout, want, res.stderr)
	}
}

func TestHistoryPrintsNothingWithoutCheckCommits(t *testing.T) {
	dir := fixtureRepo(t, nil)
	commitAt(t, dir, midday, map[string]string{"web/x.txt": "x\n"})
	res := run(t, dir, "scripts/checks-history.sh")
	if res.code != 0 || res.stdout != "" {
		t.Fatalf("exit %d, want 0 and no output\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
}
