package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Scout mode is on while ADMIRAL_SCOUT names the checkout; the rule names below are the contract.
const (
	scoutEnv      = "ADMIRAL_SCOUT"
	scoutGit      = "scout_git"
	scoutGhPr     = "scout_gh_pr"
	scoutGhAPI    = "scout_gh_api"
	scoutGraphql  = "scout_graphql"
	scoutMcp      = "scout_mcp"
	scoutEdit     = "scout_edit"
	scoutWrite    = "scout_shell_write"
	scoutEnvRule  = "scout_env"
	scoutAnyRule  = "*" // refused, whichever rule says so
	scoutAllowed  = wantAllowed
	scoutPlainGQL = `query { viewer { login } }`
	scoutMutation = `mutation { addComment(input: {subjectId: "X", body: "hi"}) { clientMutationId } }`
)

// scoutRig is a scout's checkout, a directory outside it, and symlinks between the two.
type scoutRig struct {
	co, out string
	intoCo  string // out/into-co, a symlink to the checkout
	intoOut string // co/into-out, a symlink to the outside directory
}

func newScoutRig(t *testing.T) scoutRig {
	t.Helper()
	resolve := func(dir string) string {
		d, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	r := scoutRig{co: resolve(t.TempDir()), out: resolve(t.TempDir())}
	r.intoCo, r.intoOut = filepath.Join(r.out, "into-co"), filepath.Join(r.co, "into-out")
	if err := os.Symlink(r.co, r.intoCo); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(r.out, r.intoOut); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.co, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return r
}

// run drives Guard as a scout ship's hook would: ADMIRAL_SCOUT names the checkout, the payload's cwd is
// cwd (the checkout when empty).
func (r scoutRig) run(t *testing.T, autopilot, tool string, input map[string]any, cwd string) (err error, stdout, stderr string) {
	t.Helper()
	if cwd == "" {
		cwd = r.co
	}
	b, merr := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input, "cwd": cwd})
	if merr != nil {
		t.Fatal(merr)
	}
	t.Setenv(scoutEnv, r.co)
	return runGuardTest(t, string(b), autopilot)
}

type scoutCase struct {
	name  string
	tool  string
	input map[string]any
	cwd   string
	want  string
}

func scoutCases(r scoutRig) []scoutCase {
	sh := func(name, cmd, want string) scoutCase {
		return scoutCase{name: name, tool: "Bash", input: bashInput(cmd), want: want}
	}
	in := func(p string) string { return filepath.Join(r.co, p) }
	ex := func(p string) string { return filepath.Join(r.out, p) }
	file := func(name, tool, key, path, want string) scoutCase {
		return scoutCase{name: name, tool: tool, input: map[string]any{key: path}, want: want}
	}
	mcp := func(name, tool, want string) scoutCase {
		return scoutCase{name: name, tool: tool, input: map[string]any{"owner": "o", "repo": "r"}, want: want}
	}
	return []scoutCase{
		// git: only the read-only allowlist runs, wherever it runs.
		sh("git commit", `git commit -m x`, scoutGit),
		sh("git push", `git push origin HEAD`, scoutGit),
		sh("git add", `git add .`, scoutGit),
		sh("git -C into the checkout", `git -C `+r.co+` commit -m x`, scoutGit),
		sh("git -c before the subcommand", `git -c user.name=x commit -m x`, scoutGit),
		sh("git --git-dir and --work-tree", `git --git-dir=`+in(".git")+` --work-tree=`+r.co+` reset --hard`, scoutGit),
		sh("git commit after cd out of the checkout", `cd `+r.out+` && git commit -m x`, scoutGit),
		sh("git commit inside bash -c", `bash -c 'git commit -m x'`, scoutGit),
		sh("git commit in a substitution", `echo $(git commit -m x)`, scoutGit),
		sh("git commit under env", `env GIT_AUTHOR_NAME=x git commit -m x`, scoutGit),
		sh("git commit after a pipe", `echo x | git commit -F -`, scoutGit),
		sh("git checkout", `git checkout main`, scoutGit),
		sh("git switch", `git switch -c topic`, scoutGit),
		sh("git reset", `git reset --hard HEAD~1`, scoutGit),
		sh("git restore", `git restore a.go`, scoutGit),
		sh("git stash push", `git stash`, scoutGit),
		sh("git apply", `git apply fix.diff`, scoutGit),
		sh("git am", `git am p.mbox`, scoutGit),
		sh("git clean", `git clean -fd`, scoutGit),
		sh("git rebase", `git rebase main`, scoutGit),
		sh("git merge", `git merge topic`, scoutGit),
		sh("git cherry-pick", `git cherry-pick abc123`, scoutGit),
		sh("git revert", `git revert HEAD`, scoutGit),
		sh("plumbing commit-tree", `git commit-tree HEAD^{tree} -m x`, scoutGit),
		sh("plumbing update-ref", `git update-ref refs/heads/x HEAD`, scoutGit),
		sh("plumbing update-index", `git update-index --add a.go`, scoutGit),
		sh("plumbing hash-object -w", `git hash-object -w a.go`, scoutGit),
		sh("plumbing read-tree", `git read-tree HEAD`, scoutGit),
		sh("plumbing symbolic-ref", `git symbolic-ref HEAD refs/heads/x`, scoutGit),
		sh("a branch that is created", `git branch topic`, scoutGit),
		sh("a branch that is deleted", `git branch -D topic`, scoutGit),
		sh("a tag that is created", `git tag v1`, scoutGit),
		sh("a remote that is added", `git remote add o https://example.test/r.git`, scoutGit),
		sh("config that is set", `git config user.name x`, scoutGit),
		sh("a worktree that is added", `git worktree add ../w`, scoutGit),
		sh("a clone into the checkout", `git clone https://example.test/r.git `+in("sub"), scoutGit),
		sh("a clone into a relative path", `git clone https://example.test/r.git sub`, scoutGit),
		sh("an alias set with -c", `git -c alias.st=commit st -m x`, scoutGit),
		sh("an unknown subcommand could be an alias", `git frobnicate`, scoutGit),
		sh("a subcommand from a variable", `git $SUB -m x`, scoutGit),
		sh("a shell alias", `git -c alias.x='!rm -rf a.go' x`, scoutAnyRule),

		// git reads stay allowed.
		sh("git status", `git status`, scoutAllowed),
		sh("git log", `git log --oneline -5`, scoutAllowed),
		sh("git diff", `git diff HEAD~1 -- a.go`, scoutAllowed),
		sh("git -C status", `git -C `+r.co+` status --short`, scoutAllowed),
		sh("git show", `git show HEAD:a.go`, scoutAllowed),
		sh("git blame", `git blame a.go`, scoutAllowed),
		sh("git grep", `git grep -n package`, scoutAllowed),
		sh("git ls-files", `git ls-files`, scoutAllowed),
		sh("git ls-tree", `git ls-tree HEAD`, scoutAllowed),
		sh("git rev-parse", `git rev-parse HEAD`, scoutAllowed),
		sh("git rev-list", `git rev-list --count HEAD`, scoutAllowed),
		sh("git cat-file", `git cat-file -p HEAD`, scoutAllowed),
		sh("git describe", `git describe --tags`, scoutAllowed),
		sh("git shortlog", `git shortlog -sn`, scoutAllowed),
		sh("git fetch", `git fetch origin`, scoutAllowed),
		sh("git branch listing", `git branch --list`, scoutAllowed),
		sh("git tag listing", `git tag -l`, scoutAllowed),
		sh("git remote listing", `git remote -v`, scoutAllowed),
		sh("git config reading", `git config --get remote.origin.url`, scoutAllowed),
		sh("git stash listing", `git stash list`, scoutAllowed),
		sh("git worktree listing", `git worktree list`, scoutAllowed),
		sh("git log into a pipe", `git log --oneline | head -3`, scoutAllowed),
		sh("a clone outside the checkout", `git clone https://example.test/r.git `+ex("clone"), scoutAllowed),

		// gh: pull request changes are refused through every path the guard sees.
		sh("gh pr create", `gh pr create --title t --body b`, scoutGhPr),
		sh("gh pr edit", `gh pr edit 1 --title t`, scoutGhPr),
		sh("gh pr merge", `gh pr merge 1 --auto`, scoutGhPr),
		sh("gh pr close", `gh pr close 1`, scoutGhPr),
		sh("gh pr reopen", `gh pr reopen 1`, scoutGhPr),
		sh("gh pr ready", `gh pr ready 1`, scoutGhPr),
		sh("gh pr review comment", `gh pr review 1 --comment -b hi`, scoutGhPr),
		sh("gh pr comment", `gh pr comment 1 -b hi`, scoutGhPr),
		sh("gh pr checkout", `gh pr checkout 1`, scoutGhPr),
		sh("gh pr lock", `gh pr lock 1`, scoutGhPr),
		sh("gh -R before pr", `gh -R o/r pr create --title t`, scoutGhPr),
		sh("gh pr create after &&", `cd `+r.out+` && gh pr create --title t`, scoutGhPr),
		sh("gh pr create in bash -c", `bash -c "gh pr create --title t"`, scoutGhPr),
		sh("api POST to pulls", `gh api -X POST repos/o/r/pulls -f title=t -f head=h -f base=b`, scoutGhAPI),
		sh("api fields make a POST", `gh api repos/o/r/pulls/1/comments -f body=hi`, scoutGhAPI),
		sh("api PATCH", `gh api -X PATCH repos/o/r/pulls/1 -f state=closed`, scoutGhAPI),
		sh("api PUT merge", `gh api -X PUT repos/o/r/pulls/1/merge`, scoutGhAPI),
		sh("api DELETE", `gh api -X DELETE repos/o/r/git/refs/heads/x`, scoutGhAPI),
		sh("api write to an issue", `gh api -X POST repos/o/r/issues/1/comments -f body=hi`, scoutGhAPI),
		sh("api --input makes a POST", `gh api repos/o/r/pulls --input body.json`, scoutGhAPI),
		sh("graphql mutation", `gh api graphql -f query='`+scoutMutation+`'`, scoutGraphql),
		sh("graphql query set in a variable", `gh api graphql -f query="$Q"`, scoutGraphql),

		sh("pr view", `gh pr view 12`, scoutAllowed),
		sh("pr list", `gh pr list --state open`, scoutAllowed),
		sh("pr diff", `gh pr diff 12`, scoutAllowed),
		sh("pr checks", `gh pr checks 12`, scoutAllowed),
		sh("pr status", `gh pr status`, scoutAllowed),
		sh("api default GET", `gh api repos/o/r/pulls/1`, scoutAllowed),
		sh("api explicit GET with fields", `gh api -X GET search/issues -f q=is:pr`, scoutAllowed),
		sh("api GET with a jq filter", `gh api repos/o/r/pulls/1/comments --jq '.[].body'`, scoutAllowed),
		sh("graphql query", `gh api graphql -f query='`+scoutPlainGQL+`'`, scoutAllowed),
		sh("an issue read", `gh issue view 1`, scoutAllowed),

		// MCP: a tool that creates or changes a PR, or commits or pushes through the API, is refused unless its name starts with a read verb.
		mcp("mcp create_pull_request", "mcp__github__create_pull_request", scoutMcp),
		mcp("mcp update_pull_request", "mcp__github__update_pull_request", scoutMcp),
		mcp("mcp merge_pull_request", "mcp__github__merge_pull_request", scoutMcp),
		mcp("mcp update_pull_request_branch", "mcp__github__update_pull_request_branch", scoutMcp),
		mcp("mcp pull_request_review_write", "mcp__github__pull_request_review_write", scoutMcp),
		mcp("mcp add_comment_to_pending_review", "mcp__github__add_comment_to_pending_review", scoutMcp),
		mcp("mcp request_copilot_review", "mcp__github__request_copilot_review", scoutMcp),
		mcp("mcp push_files", "mcp__github__push_files", scoutMcp),
		mcp("mcp create_or_update_file", "mcp__github__create_or_update_file", scoutMcp),
		mcp("mcp delete_file", "mcp__github__delete_file", scoutMcp),
		mcp("mcp create_branch", "mcp__github__create_branch", scoutMcp),
		mcp("mcp on another server", "mcp__corp_git__create_pull_request", scoutMcp),
		mcp("mcp get_pull_request", "mcp__github__get_pull_request", scoutAllowed),
		mcp("mcp list_pull_requests", "mcp__github__list_pull_requests", scoutAllowed),
		mcp("mcp search_pull_requests", "mcp__github__search_pull_requests", scoutAllowed),
		mcp("mcp get_pull_request_files", "mcp__github__get_pull_request_files", scoutAllowed),
		mcp("mcp get_file_contents", "mcp__github__get_file_contents", scoutAllowed),
		mcp("mcp list_branches", "mcp__github__list_branches", scoutAllowed),
		mcp("mcp that has nothing to do with a repo", "mcp__slack__slack_send_message", scoutAllowed),

		// Edit tools: refused inside the checkout, allowed outside it.
		file("Edit in the checkout", "Edit", "file_path", in("a.go"), scoutEdit),
		file("Write a new file in the checkout", "Write", "file_path", in("new.txt"), scoutEdit),
		file("Write under a directory that does not exist yet", "Write", "file_path", in("x/y/z.txt"), scoutEdit),
		file("MultiEdit in the checkout", "MultiEdit", "file_path", in("a.go"), scoutEdit),
		file("NotebookEdit in the checkout", "NotebookEdit", "notebook_path", in("n.ipynb"), scoutEdit),
		file("Write through a symlink that points into the checkout", "Write", "file_path", filepath.Join(r.intoCo, "new.txt"), scoutEdit),
		file("Write through a symlink under a missing directory", "Write", "file_path", filepath.Join(r.intoCo, "x", "new.txt"), scoutEdit),
		file("Write with .. back into the checkout", "Write", "file_path", in("sub/../new.txt"), scoutEdit),
		file("Write with a relative path", "Write", "file_path", "new.txt", scoutEdit),
		file("Edit with an empty path", "Edit", "file_path", "", scoutEdit),
		{name: "Write with no path", tool: "Write", input: map[string]any{"content": "x"}, want: scoutEdit},
		{name: "Write with a path that is not text", tool: "Write", input: map[string]any{"file_path": 7}, want: scoutEdit},
		{name: "NotebookEdit with only file_path", tool: "NotebookEdit", input: map[string]any{"file_path": ex("n.ipynb")}, want: scoutEdit},
		file("Write outside the checkout", "Write", "file_path", ex("note.md"), scoutAllowed),
		file("Edit outside the checkout", "Edit", "file_path", ex("note.md"), scoutAllowed),
		file("NotebookEdit outside the checkout", "NotebookEdit", "notebook_path", ex("n.ipynb"), scoutAllowed),
		file("Write through a symlink that leaves the checkout", "Write", "file_path", filepath.Join(r.intoOut, "note.md"), scoutAllowed),
		file("Write beside the checkout under its name", "Write", "file_path", r.co+"-sibling/note.md", scoutAllowed),
		file("Write under /tmp", "Write", "file_path", "/tmp/admiral-scout-test-note.md", scoutAllowed),
		file("Read in the checkout", "Read", "file_path", in("a.go"), scoutAllowed),
		{name: "Grep", tool: "Grep", input: map[string]any{"pattern": "x"}, want: scoutAllowed},
		{name: "WebFetch", tool: "WebFetch", input: map[string]any{"url": "https://example.test"}, want: scoutAllowed},

		// Shell writes into the checkout.
		sh("redirect to a file", `echo hi > `+in("f.txt"), scoutWrite),
		sh("append", `echo hi >> `+in("f.txt"), scoutWrite),
		sh("clobber", `echo hi >| `+in("f.txt"), scoutWrite),
		sh("&> to a file", `make &> `+in("build.log"), scoutWrite),
		sh("stderr to a file", `make 2> `+in("err.log"), scoutWrite),
		sh("read-write redirect", `cat <> `+in("f.txt"), scoutWrite),
		sh("redirect to a relative path", `echo hi > f.txt`, scoutWrite),
		sh("redirect after a pipe", `echo hi | cat > f.txt`, scoutWrite),
		sh("heredoc into a file", "cat > f.txt <<'EOF'\nhi\nEOF", scoutWrite),
		sh("redirect in a compound", `{ echo hi; } > `+in("f.txt"), scoutWrite),
		sh("redirect in bash -c", `bash -c 'echo hi > f.txt'`, scoutWrite),
		sh("redirect to an unresolved variable", `echo hi > "$OUT"`, scoutWrite),
		sh("redirect to a path with an expansion", `echo hi > `+ex("$NAME"), scoutWrite),
		sh("rm", `rm `+in("a.go"), scoutWrite),
		sh("rm -rf", `rm -rf `+in("sub"), scoutWrite),
		sh("rm relative", `rm a.go`, scoutWrite),
		sh("rmdir", `rmdir `+in("sub"), scoutWrite),
		sh("mv into the checkout", `mv `+ex("a")+` `+in("b"), scoutWrite),
		sh("cp into the checkout", `cp `+ex("a")+` `+in("b"), scoutWrite),
		sh("ln", `ln -s a.go `+in("link"), scoutWrite),
		sh("touch", `touch `+in("f"), scoutWrite),
		sh("touch relative", `touch f`, scoutWrite),
		sh("touch after --", `touch -- f`, scoutWrite),
		sh("mkdir", `mkdir `+in("d"), scoutWrite),
		sh("mkdir -p relative", `mkdir -p d/e`, scoutWrite),
		sh("tee", `echo hi | tee `+in("f.txt"), scoutWrite),
		sh("tee -a", `echo hi | tee -a f.txt`, scoutWrite),
		sh("truncate", `truncate -s 0 `+in("a.go"), scoutWrite),
		sh("install", `install `+ex("a")+` `+in("b"), scoutWrite),
		sh("dd of=", `dd if=`+ex("a")+` of=`+in("b"), scoutWrite),
		sh("sed -i", `sed -i s/a/b/ `+in("a.go"), scoutWrite),
		sh("sed -i with a backup suffix", `sed -i.bak s/a/b/ a.go`, scoutWrite),
		sh("sed --in-place", `sed --in-place s/a/b/ a.go`, scoutWrite),
		sh("perl -i", `perl -i -pe s/a/b/ `+in("a.go"), scoutWrite),
		sh("patch", `patch `+in("a.go")+` fix.diff`, scoutWrite),
		sh("through a symlink that points into the checkout", `touch `+filepath.Join(r.intoCo, "f"), scoutWrite),
		sh("a write after && in the checkout", `ls && touch f`, scoutWrite),
		sh("a write in a loop", `for f in a b; do touch $f; done`, scoutWrite),
		sh("a relative write after cd", `cd sub && touch f`, scoutWrite),
		sh("a relative write after cd out", `cd `+r.out+` && touch f`, scoutWrite),
		sh("a relative write after pushd", `pushd `+r.out+` && touch f`, scoutWrite),
		sh("a relative write after popd", `popd && touch f`, scoutWrite),
		sh("a relative write under env -C", `env -C `+r.out+` touch f`, scoutWrite),
		sh("a relative write under env --chdir", `env --chdir=`+r.out+` touch f`, scoutWrite),
		sh("a relative write under sudo -D", `sudo -D `+r.out+` touch f`, scoutWrite),
		sh("a relative redirect after cd", `cd `+r.out+` && echo hi > f`, scoutWrite),
		sh("a target from a variable", `touch $F`, scoutWrite),
		sh("a target from a substitution", `touch $(mktemp)`, scoutWrite),
		sh("a target with a tilde", `touch ~/f`, scoutWrite),
		sh("a target with a glob", `rm `+in("*.go"), scoutWrite),

		sh("redirect to /tmp", `echo hi > `+ex("f.txt"), scoutAllowed),
		sh("redirect to /dev/null", `make > /dev/null 2>&1`, scoutAllowed),
		sh("redirect to /dev/stderr", `echo hi > /dev/stderr`, scoutAllowed),
		sh("redirect to /dev/stdout", `echo hi > /dev/stdout`, scoutAllowed),
		sh("fd duplication", `make 2>&1 | head`, scoutAllowed),
		sh("fd duplication to stderr", `echo hi >&2`, scoutAllowed),
		sh("redirect from a file", `wc -l < a.go`, scoutAllowed),
		sh("a here-string", `cat <<< hi`, scoutAllowed),
		sh("heredoc to a pipe", "cat <<'EOF' | wc -l\nhi\nEOF", scoutAllowed),
		sh("rm outside", `rm -rf `+ex("sub"), scoutAllowed),
		sh("mv outside", `mv `+ex("a")+` `+ex("b"), scoutAllowed),
		sh("cp out of the checkout", `cp `+in("a.go")+` `+ex("b"), scoutAllowed),
		sh("touch outside", `touch `+ex("f"), scoutAllowed),
		sh("mkdir outside", `mkdir -p `+ex("d/e"), scoutAllowed),
		sh("tee outside", `echo hi | tee `+ex("f.txt"), scoutAllowed),
		sh("sed -i outside", `sed -i s/a/b/ `+ex("f.txt"), scoutAllowed),
		sh("sed without -i", `sed s/a/b/ a.go`, scoutAllowed),
		sh("perl without -i", `perl -pe s/a/b/ a.go`, scoutAllowed),
		sh("dd to outside", `dd if=a.go of=`+ex("b"), scoutAllowed),
		sh("an absolute write after cd", `cd `+r.out+` && touch `+ex("f"), scoutAllowed),
		sh("through a symlink that leaves the checkout", `touch `+filepath.Join(r.intoOut, "f"), scoutAllowed),
		sh("a sibling directory with the checkout's name as a prefix", `touch `+r.co+`-sibling/f`, scoutAllowed),
		sh("cd in the checkout then a read", `cd `+r.co+` && ls`, scoutAllowed),
		sh("a read-only command", `ls -la && cat a.go | wc -l`, scoutAllowed),
		sh("the build tool writes by itself", `go test ./...`, scoutAllowed),
		sh("echo with a > inside quotes", `echo "a > b"`, scoutAllowed),
		sh("a command that is not a write", `grep -rn package .`, scoutAllowed),

		// Fail closed.
		sh("an unterminated quote", `echo "unterminated`, scoutAnyRule),
		sh("zsh syntax", `echo ${(f)x}`, scoutAnyRule),
		{name: "Bash with no command", tool: "Bash", input: map[string]any{}, want: scoutAnyRule},
		{name: "Bash with a command that is not text", tool: "Bash", input: map[string]any{"command": 7}, want: scoutAnyRule},
	}
}

func TestScoutGuardRules(t *testing.T) {
	for _, auto := range []string{"", "1"} {
		name := "a scout that is not on autopilot"
		if auto == "1" {
			name = "a scout on autopilot"
		}
		t.Run(name, func(t *testing.T) {
			r := newScoutRig(t)
			for _, c := range scoutCases(r) {
				t.Run(c.name, func(t *testing.T) {
					err, out, _ := r.run(t, auto, c.tool, c.input, c.cwd)
					if out != "" {
						t.Errorf("stdout %q, want nothing: Claude reads a hook's stdout", out)
					}
					var denied *GuardDenied
					switch {
					case c.want == scoutAllowed && err != nil:
						t.Errorf("Guard refused a scout's %s: %v, want it allowed", c.tool, err)
					case c.want != scoutAllowed && !errors.As(err, &denied):
						t.Errorf("Guard returned %v for a scout's %s, want a *GuardDenied for rule %s", err, c.tool, c.want)
					case c.want != scoutAllowed && c.want != scoutAnyRule && denied.Rule != c.want:
						t.Errorf("rule = %q, want %q", denied.Rule, c.want)
					}
				})
			}
		})
	}
}

func TestScoutRefusalsShareOneReasonThatNamesNoCommand(t *testing.T) {
	r := newScoutRig(t)
	reasons := map[string]string{}
	for _, c := range scoutCases(r) {
		if c.want == scoutAllowed || c.want == scoutAnyRule {
			continue
		}
		err, _, errb := r.run(t, "", c.tool, c.input, c.cwd)
		var denied *GuardDenied
		if !errors.As(err, &denied) {
			continue // TestScoutGuardRules reports it
		}
		if strings.TrimSuffix(errb, "\n") != denied.Reason {
			t.Errorf("%s: stderr %q, want the reason %q alone", c.name, errb, denied.Reason)
		}
		reasons[c.want] = denied.Reason
		for _, text := range []string{"commit", "push", "touch", "rm ", "tee", "sed", "pull_request", "push_files", r.co, r.out, "a.go"} {
			if strings.Contains(denied.Reason+denied.Error(), text) {
				t.Errorf("%s: the reason %q names %q", c.name, denied.Reason, text)
			}
		}
	}
	var first string
	for rule, reason := range reasons {
		if first == "" {
			first = reason
		}
		if reason != first {
			t.Errorf("rule %s says %q, but another scout rule says %q: want one reason text", rule, reason, first)
		}
		if !strings.Contains(strings.ToLower(reason), "scout") {
			t.Errorf("reason %q does not say it is a scout's guard", reason)
		}
		if reason == wantGuardReason {
			t.Errorf("a scout refusal repeats the autopilot review reason %q", reason)
		}
	}
	if len(reasons) < 6 {
		t.Errorf("only %d scout rules refused anything, want each rule exercised", len(reasons))
	}
}

func TestScoutModeRunsWithoutAutopilotAndAutopilotRulesStackOnTop(t *testing.T) {
	r := newScoutRig(t)
	t.Run("a scout that is not on autopilot is still refused a commit", func(t *testing.T) {
		err, _, _ := r.run(t, "", "Bash", bashInput(`git commit -m x`), "")
		var denied *GuardDenied
		if !errors.As(err, &denied) || denied.Rule != scoutGit {
			t.Errorf("Guard = %v, want a *GuardDenied for rule %s", err, scoutGit)
		}
	})
	t.Run("an approving review is refused on a scout on autopilot", func(t *testing.T) {
		err, _, _ := r.run(t, "1", "Bash", bashInput(`gh pr review 12 --approve`), "")
		var denied *GuardDenied
		if !errors.As(err, &denied) {
			t.Errorf("Guard = %v, want a refusal", err)
		}
	})
	t.Run("an opaque command is refused on a scout on autopilot and allowed on one that is not", func(t *testing.T) {
		cmd := `bash review-approve.sh`
		if err, _, _ := r.run(t, "1", "Bash", bashInput(cmd), ""); err == nil {
			t.Error("autopilot's shell_script rule did not apply to a scout on autopilot")
		}
		if err, _, _ := r.run(t, "", "Bash", bashInput(cmd), ""); err != nil {
			t.Errorf("Guard = %v for a script run by a scout not on autopilot, want it allowed: the autopilot rules apply only with ADMIRAL_AUTOPILOT=1", err)
		}
	})
}

func TestScoutModeRecordsTheRuleAsAGuardDeniedOutcome(t *testing.T) {
	r := newScoutRig(t)
	home := t.TempDir()
	t.Setenv("ADMIRAL_SHIP", "")
	payload, _ := json.Marshal(map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": filepath.Join(r.co, "x-secret-name.txt")}, "cwd": r.co})
	t.Setenv(scoutEnv, r.co)
	t.Setenv("ADMIRAL_AUTOPILOT", "")
	t.Setenv("ADMIRAL_SHIP", "scout-ship")
	t.Setenv("ADMIRAL_REPORT", filepath.Join(home, "reports", "scout-ship.json"))
	t.Setenv("ADMIRAL_HOME", "")
	var errb bytes.Buffer
	if err := Guard([]string{"pretooluse"}, IO{In: strings.NewReader(string(payload)), Out: &bytes.Buffer{}, Err: &errb}); err == nil {
		t.Fatal("Guard allowed a scout's Write into its checkout")
	}
	files, _ := filepath.Glob(filepath.Join(home, "outcomes", "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("outcome files = %v, want one month file", files)
	}
	raw, _ := os.ReadFile(files[0])
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &rec); err != nil {
		t.Fatalf("the log is not one JSON line: %s", raw)
	}
	if rec["event"] != "guard_denied" || rec["ship"] != "scout-ship" || rec["tool"] != "Write" || rec["rule"] != scoutEdit {
		t.Errorf("record = %v, want guard_denied for scout-ship, tool Write, rule %s", rec, scoutEdit)
	}
	if strings.Contains(string(raw), "secret-name") {
		t.Errorf("the log holds the path the scout wrote: %s", raw)
	}
}

func TestScoutModeRefusesEveryCallWhenTheCheckoutIsNotAnAbsolutePath(t *testing.T) {
	r := newScoutRig(t)
	for _, v := range []string{"1", "true", "checkout", "./checkout", "../checkout", "~/checkout", "relative/path", " "} {
		for _, call := range []struct {
			tool  string
			input map[string]any
		}{
			{"Bash", bashInput(`git status`)},
			{"Bash", bashInput(`ls`)},
			{"Read", map[string]any{"file_path": "/tmp/x"}},
			{"Write", map[string]any{"file_path": filepath.Join(r.out, "x")}},
			{"mcp__github__get_pull_request", map[string]any{}},
		} {
			t.Run(v+" "+call.tool, func(t *testing.T) {
				payload, _ := json.Marshal(map[string]any{"tool_name": call.tool, "tool_input": call.input, "cwd": r.out})
				t.Setenv(scoutEnv, v)
				err, _, _ := runGuardTest(t, string(payload), "")
				var denied *GuardDenied
				if !errors.As(err, &denied) {
					t.Fatalf("Guard = %v with %s=%q, want a refusal: a value that is not a clean absolute path fails closed", err, scoutEnv, v)
				}
				if denied.Rule != scoutEnvRule {
					t.Errorf("rule = %q, want %q", denied.Rule, scoutEnvRule)
				}
			})
		}
	}
}

func TestScoutModeRefusesACheckoutPathThatIsNotClean(t *testing.T) {
	r := newScoutRig(t)
	for _, v := range []string{r.co + "/", r.co + "/../" + filepath.Base(r.co), r.co + "/./sub", "/"} {
		payload, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": bashInput(`ls`), "cwd": r.out})
		t.Setenv(scoutEnv, v)
		err, _, _ := runGuardTest(t, string(payload), "")
		var denied *GuardDenied
		if !errors.As(err, &denied) {
			t.Errorf("Guard = %v with %s=%q, want a refusal", err, scoutEnv, v)
		}
	}
}

func TestScoutModeRefusesAPayloadItCannotRead(t *testing.T) {
	r := newScoutRig(t)
	for _, p := range []string{`not json`, `null`, `{"tool_name":"Bash","tool_input":{"command":7}}`, `{"tool_name":"Write","tool_input":"x"}`} {
		t.Setenv(scoutEnv, r.co)
		err, _, _ := runGuardTest(t, p, "")
		var denied *GuardDenied
		if !errors.As(err, &denied) {
			t.Errorf("Guard(%q) = %v, want a *GuardDenied", p, err)
		}
	}
}

func TestScoutModeOffEditPayloadsAreAllowed(t *testing.T) {
	r := newScoutRig(t)
	payload, _ := json.Marshal(map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": filepath.Join(r.co, "a.go")}, "cwd": r.co})
	t.Setenv(scoutEnv, "")
	for _, auto := range []string{"", "1"} {
		if err, out, errb := runGuardTest(t, string(payload), auto); err != nil || out != "" || errb != "" {
			t.Errorf("ADMIRAL_AUTOPILOT=%q: Guard = %v, stdout %q, stderr %q, want nil and no output: only a scout's guard refuses an edit", auto, err, out, errb)
		}
	}
	for _, cmd := range []string{`git commit -m x`, `echo hi > f.txt`, `gh pr create --title t`, `rm -rf x`} {
		p, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": bashInput(cmd), "cwd": r.co})
		if err, _, _ := runGuardTest(t, string(p), "1"); err != nil {
			t.Errorf("Guard refused %q outside scout mode: %v, want the autopilot rules alone", cmd, err)
		}
	}
}
