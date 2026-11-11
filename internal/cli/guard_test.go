package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const wantGuardReason = "Autopilot ships leave reviews PENDING. The engineer submits them from the inbox."

const (
	wantRuleGhPrReview    = "gh_pr_review"
	wantRuleGhApiEvent    = "gh_api_review_event"
	wantRuleGhApiSubmit   = "gh_api_review_submit"
	wantRuleGraphqlReview = "graphql_review"
	wantRuleMcpReview     = "mcp_review"
	wantRuleUnparsed      = "unparsed"
	wantAllowed           = ""
	gqlSubmitApprove      = `mutation { submitPullRequestReview(input: {pullRequestId: "X", event: APPROVE}) { clientMutationId } }`
	gqlAddApprove         = `mutation { addPullRequestReview(input: {pullRequestId: "X", event: APPROVE}) { clientMutationId } }`
	gqlSubmitChanges      = `mutation { submitPullRequestReview(input: {pullRequestId: "X", event: REQUEST_CHANGES}) { clientMutationId } }`
	gqlSubmitVariable     = `mutation($e: PullRequestReviewEvent!) { submitPullRequestReview(input: {event: $e}) { clientMutationId } }`
	gqlSubmitInputBody    = `{"query":"mutation($e: PullRequestReviewEvent!) { submitPullRequestReview(input: {event: $e}) { clientMutationId } }","variables":{"e":"APPROVE"}}`
)

func bashInput(cmd string) map[string]any { return map[string]any{"command": cmd} }

type guardTestCase struct {
	name  string
	tool  string
	input map[string]any
	files map[string]string // written in the payload's cwd
	want  string            // the rule of a refusal, or wantAllowed
}

// runGuardTest unsets ADMIRAL_AUTOPILOT when autopilot is empty.
func runGuardTest(t *testing.T, payload, autopilot string) (error, string, string) {
	t.Helper()
	t.Setenv("ADMIRAL_AUTOPILOT", autopilot)
	t.Setenv("ADMIRAL_HOME", "")
	t.Setenv("ADMIRAL_REPORT", "")
	t.Setenv("ADMIRAL_SHIP", "")
	var out, errb bytes.Buffer
	err := Guard([]string{"pretooluse"}, IO{In: strings.NewReader(payload), Out: &out, Err: &errb})
	return err, out.String(), errb.String()
}

func (c guardTestCase) payload(t *testing.T) string {
	t.Helper()
	cwd := t.TempDir()
	for name, content := range c.files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(cwd, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": c.tool, "tool_input": c.input, "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func guardTestCases() []guardTestCase {
	b := func(name, cmd, want string) guardTestCase {
		return guardTestCase{name: name, tool: "Bash", input: bashInput(cmd), want: want}
	}
	review := func(name, cmd string) guardTestCase { return b(name, cmd, wantRuleGhPrReview) }
	apiFile := func(name, cmd string, files map[string]string, want string) guardTestCase {
		return guardTestCase{name: name, tool: "Bash", input: bashInput(cmd), files: files, want: want}
	}
	mcp := func(name, tool string, input map[string]any, want string) guardTestCase {
		return guardTestCase{name: name, tool: tool, input: input, want: want}
	}
	const reviews = "repos/o/r/pulls/12/reviews"
	return []guardTestCase{
		// Each row is one way a call could submit, or hide, an approving or blocking review.
		review("pr review --approve", `gh pr review 12 --approve`),
		review("request changes after &&", `cd x && gh pr review 12 -r -b no`),
		review("bash -c", `bash -c "gh pr review 12 --approve"`),
		b("api reviews event=APPROVE", `gh api repos/o/r/pulls/12/reviews -f event=APPROVE`, wantRuleGhApiEvent),
		b("api submit a pending review", `gh api -X POST repos/o/r/pulls/12/reviews/9/events -f event=APPROVE`, wantRuleGhApiSubmit),
		b("graphql submitPullRequestReview", `gh api graphql -f query='`+gqlSubmitApprove+`'`, wantRuleGraphqlReview),

		review("approve=false is over-refused", `gh pr review 12 --approve=false`),
		review("short cluster -ab", `gh pr review 12 -ab ok`),
		review("after a pipe", `echo x | gh pr review 12 --approve`),
		review("env wrapper with an assignment", `FOO=1 env gh pr review 12 -a`),
		review("command wrapper", `command gh pr review 12 -a`),
		review("absolute path to gh", `/opt/homebrew/bin/gh pr review 12 -a`),
		review("command substitution", `echo $(gh pr review 12 --approve)`),
		review("backticks", "echo `gh pr review 12 --approve`"),
		review("a flag after the body", `gh pr review 12 -b ok --approve`),

		b("api placeholders and a lowercase value", `gh api /repos/{owner}/{repo}/pulls/12/reviews --raw-field event=approve`, wantRuleGhApiEvent),
		b("api full URL, --field with quotes", `gh api https://api.github.com/repos/o/r/pulls/12/reviews --field 'event=APPROVE'`, wantRuleGhApiEvent),
		b("api -X GET is refused whatever the method", `gh api -X GET `+reviews+` -f event=APPROVE`, wantRuleGhApiEvent),
		b("api query string on the endpoint", `gh api "`+reviews+`?per_page=1" -f event=APPROVE`, wantRuleGhApiEvent),
		apiFile("api --input file holding APPROVE", `gh api `+reviews+` --input body.json`, map[string]string{"body.json": `{"event":"APPROVE"}`}, wantRuleGhApiEvent),
		apiFile("api --input file that is missing", `gh api `+reviews+` --input body.json`, nil, wantRuleGhApiEvent),
		b("api --input - with a heredoc body", "gh api "+reviews+" --input - <<'EOF'\n{\"body\": \"x\", \"event\": \"REQUEST_CHANGES\"}\nEOF", wantRuleGhApiEvent),
		apiFile("api -F event=@file holding APPROVE", `gh api `+reviews+` -F event=@ev.txt`, map[string]string{"ev.txt": "APPROVE\n"}, wantRuleGhApiEvent),
		b("api -F event=@-", `gh api `+reviews+` -F event=@-`, wantRuleGhApiEvent),
		b("api submit endpoint with COMMENT is refused too", `gh api -X POST repos/o/r/pulls/12/reviews/9/events -f event=COMMENT`, wantRuleGhApiSubmit),

		b("graphql addPullRequestReview APPROVE", `gh api graphql -f query='`+gqlAddApprove+`'`, wantRuleGraphqlReview),
		b("graphql event in a variable", `gh api graphql -f query='`+gqlSubmitVariable+`' -f e=APPROVE`, wantRuleGraphqlReview),
		apiFile("graphql --input body with the variable", `gh api graphql --input q.json`, map[string]string{"q.json": gqlSubmitInputBody}, wantRuleGraphqlReview),

		// Shapes that hide the gh call: wrappers, compound commands, other shells.
		review("for loop", `for n in 1 2; do gh pr review $n -a; done`),
		review("function body", `f() { gh pr review 12 -a; }; f`),
		review("heredoc body with an apostrophe inside $(…)", "gh pr review 12 -a -b \"$(cat <<'EOF'\nIt's fine\nEOF\n)\""),
		review("redirect 2>&1 before the flag", `gh pr review 12 2>&1 -a`),
		review("env -u NAME", `env -u X gh pr review 12 --approve`),
		review("env -S", `env -S 'gh pr review 12 --approve'`),
		review("exec -a NAME", `exec -a x gh pr review 12 --approve`),
		review("timeout", `timeout 60 gh pr review 12 --approve`),
		review("sudo -u", `sudo -u bob gh pr review 12 --approve`),
		review("xargs", `echo 1 | xargs -I{} gh pr review {} --approve`),
		review("gh -R before pr", `gh -R o/r pr review 12 --approve`),
		review("eval", `eval 'gh pr review 12 --approve'`),
		review("bash reading a heredoc", "bash <<'EOF'\ngh pr review 12 --approve\nEOF"),
		review("bash reading a here-string", `bash <<< 'gh pr review 12 -a'`),
		review("a backslash delimiter hides nothing", "cat <<\\EOF\nx\nEOF\ngh pr review 12 --approve"),
		b("bash reading a pipe falls back to the text rule", `echo 'gh pr review 12 --approve' | bash`, wantRuleUnparsed),
		b("bash running a file that does not exist", `bash review-approve.sh`, "shell_script"),
		review("a program from a substitution", `$(which gh) pr review 12 --approve`),
		b("api endpoint with a fragment", `gh api 'repos/o/r/pulls/12/reviews#x' -f event=APPROVE`, wantRuleGhApiEvent),
		b("api repositories route", `gh api repositories/1234/pulls/12/reviews -f event=APPROVE`, wantRuleGhApiEvent),
		b("api --input - with a jq literal", `jq -n '{event: "APPROVE"}' | gh api repos/o/r/pulls/12/reviews --input -`, wantRuleGhApiEvent),
		b("graphql with a space before the colon", `gh api graphql -f query='mutation { submitPullRequestReview(input: {pullRequestId: "X", event : APPROVE}) { clientMutationId } }'`, wantRuleGraphqlReview),
		b("graphql variable default", `gh api graphql -f query='mutation($e: PullRequestReviewEvent = APPROVE) { submitPullRequestReview(input: {event: $e}) { clientMutationId } }'`, wantRuleGraphqlReview),
		apiFile("graphql --input body with a nested variable", `gh api graphql --input q.json`, map[string]string{"q.json": `{"query":"mutation($i: SubmitPullRequestReviewInput!) { submitPullRequestReview(input: $i) { clientMutationId } }","variables":{"i":{"pullRequestReviewId":"x","event":"APPROVE"}}}`}, wantRuleGraphqlReview),
		b("graphql on a GHE host", `gh api https://ghe.example/api/graphql -f query='`+gqlSubmitApprove+`'`, wantRuleGraphqlReview),

		// Expansions, parse errors and shell options.
		b("api reviews, $PR in the endpoint", `gh api repos/o/r/pulls/$PR/reviews -f event=APPROVE`, wantRuleGhApiEvent),
		b("api submit, $ID in the endpoint", `gh api repos/o/r/pulls/1/reviews/$ID/events -f event=APPROVE`, wantRuleGhApiSubmit),
		b("api reviews, event from a variable", `gh api repos/o/r/pulls/1/reviews -f event="$EV"`, wantRuleGhApiEvent),
		b("graphql, query from a substitution beside APPROVE", `gh api graphql -f query="$(cat q.graphql)" -f event=APPROVE`, wantRuleGraphqlReview),
		b("graphql, exact mutation with a variable value from a variable", `gh api graphql -f query='`+gqlSubmitVariable+`' -f e="$EV"`, wantRuleGraphqlReview),
		review("pr review, a flag from a variable", `F=--approve; gh pr review 1 $F`),
		review("a -c script with an expansion", `for n in 1 2; do bash -c "gh pr review $n -a"; done`),
		review("a parse error after the call (zsh syntax)", `gh pr review 1 -a; echo ${(f)x}`),
		review("an unterminated quote on a later line", "gh pr review 1 -a\necho \"x"),
		review("bash -o VALUE before -c", `bash -o pipefail -c 'gh pr review 1 -a'`),
		review("bash -ceo pipefail", `bash -ceo pipefail 'gh pr review 1 --approve'`),
		review("zsh noglob", `noglob gh pr review 1 -a`),
		review("zsh =gh", `=gh pr review 1 -a`),
		b("a process substitution into bash, short flag", `bash <(echo 'gh pr review 1 -a')`, "shell_script"),
		b("a command whose output is the program, short flag", `$(echo gh pr review 1 -a)`, wantRuleUnparsed),

		// The text rule applies only where a call could hide a submit.
		b("pr create with review and -race in a heredoc body", "gh pr create --title \"Add the guard\" --body \"$(cat <<'EOF'\nReady for review.\n- go test -race ./...\nEOF\n)\"", wantAllowed),
		b("api reviews --jq naming APPROVED", `gh api repos/o/r/pulls/$PR/reviews --jq '.[] | select(.state=="APPROVED") | .id'`, wantAllowed),
		b("pr review --comment with an unquoted body", `gh pr review 1 --comment -b $M`, wantAllowed),
		b("graphql addPullRequestReview, only the pull request in a variable", `gh api graphql -f query='mutation($id:ID!){addPullRequestReview(input:{pullRequestId:$id}){clientMutationId}}' -f id="$PR_ID"`, wantAllowed),
		b("graphql mutation with an expansion in its text", `gh api graphql -f query="mutation{addPullRequestReview(input:{pullRequestId:\"$ID\",event:COMMENT}){clientMutationId}}"`, "graphql_merge"),
		b("graphql event bound to a variable from an expansion", `gh api graphql -f query='mutation($e:PullRequestReviewEvent!,$id:ID!){addPullRequestReview(input:{pullRequestId:$id,event:$e}){clientMutationId}}' -f e="$E" -f id="$PR_ID"`, wantRuleGraphqlReview),
		b("an expansion in single quotes of a -c script (event)", `E=APPROVE; bash -c "gh api repos/o/r/pulls/1/reviews -f event='$E'"`, wantRuleGhApiEvent),
		review("pr review, an unquoted variable that splits", `X='1 -a'; gh pr review $X`),
		b("api submit under a leading expansion", `gh api "$BASE/pulls/1/reviews/$ID/events" -f event=COMMENT`, wantRuleGhApiSubmit),
		b("api with only an expansion for the endpoint", `gh api "$U" -f event=APPROVE`, wantRuleGhApiEvent),
		b("api reviews, a field key from an expansion", `gh api repos/o/r/pulls/1/reviews -f "$K=$V"`, wantRuleGhApiEvent),
		b("api --input - with an expansion in a here-string", `gh api repos/o/r/pulls/1/reviews --input - <<< "{\"event\":\"$E\"}"`, wantRuleGhApiEvent),
		review("a quoted heredoc keeps its backslashes", "bash <<'EOF'\necho \\'; gh pr review 1 -a; echo \\'\nEOF"),

		// Content set elsewhere on the line: the whole line is read for long spellings only.
		b("a program set in a variable", `CMD='gh pr review 1 --approve'; $CMD`, wantRuleUnparsed),
		b("bash -c of a variable", `C='gh pr review 1 --approve'; bash -c "$C"`, wantRuleUnparsed),
		b("a script written to a file, then run", "cat > /tmp/r.sh <<'EOF'\ngh pr review 1 --approve\nEOF\nbash /tmp/r.sh", "shell_script"),
		b("a graphql query set in a variable", "QUERY='mutation { submitPullRequestReview(input: {pullRequestReviewId: \"PRR_x\", event: APPROVE}) { clientMutationId } }'\ngh api graphql -f query=\"$QUERY\"", wantRuleGraphqlReview),
		b("a path with an expansion after a body that asks for review and approval", `gh pr create --title t --body 'Please review and approve when ready'; $HOME/bin/lint`, wantAllowed),
		// Known gap: the whole line has no short flags, or every line with review and -r would be refused.
		b("a program set in a variable, short flag", `CMD='gh pr review 1 -a'; $CMD`, "shell_script"),

		// MCP tools.
		mcp("mcp create review APPROVE", "mcp__github__create_pull_request_review", map[string]any{"event": "APPROVE"}, wantRuleMcpReview),
		mcp("mcp submit pending review APPROVE", "mcp__github__submit_pending_pull_request_review", map[string]any{"event": "APPROVE"}, wantRuleMcpReview),

		// Fail closed.
		b("unparseable with review and approve", `echo "unterminated review approve`, wantRuleUnparsed),
		b("unparseable with a trailing backslash", `gh pr review 12 --approve \`, wantRuleUnparsed),
		{name: "Bash with no command", tool: "Bash", input: map[string]any{}, want: wantRuleUnparsed},

		// Allowed on an autopilot ship.
		b("pr review --comment", `gh pr review 12 --comment -b hi`, wantAllowed),
		b("api reviews with no event", `gh api repos/o/r/pulls/12/reviews -f body=x`, wantAllowed),
		b("pr view", `gh pr view 12`, wantAllowed),

		b("api reviews, nothing else", `gh api `+reviews, wantAllowed),
		apiFile("api --input file with no event", `gh api `+reviews+` --input body.json`, map[string]string{"body.json": `{"body":"x"}`}, wantAllowed),
		b("api event COMMENT on reviews", `gh api `+reviews+` -f event=COMMENT`, wantAllowed),
		b("short -c comment", `gh pr review 12 -c -b hi`, wantAllowed),
		b("-ba is a body of a", `gh pr review 12 -ba`, wantAllowed),
		b("-b -a has a body of -a", `gh pr review 12 -b -a`, wantAllowed),
		b("a heredoc body is not a command", "cat <<EOF\ngh pr review 12 --approve\nEOF", wantAllowed),
		b("git commit mentioning approve", `git commit -m "approve the plan"`, wantAllowed),
		b("unparseable without review", `echo "unterminated approve`, wantAllowed),
		b("a comment body that says approve, in a heredoc inside $(…)", "gh pr review 12 --comment -b \"$(cat <<'EOF'\nI can't approve this yet\nEOF\n)\"", wantAllowed),
		b("graphql query that is not a review", `gh api graphql -f query='query { viewer { login } }'`, wantAllowed),
		b("advise with a heredoc", "admiral advise <<'EOF'\n{\"question\":\"Merge?\",\"options\":[\"yes\",\"no\"],\"recommendation\":\"yes\"}\nEOF", wantAllowed),
		b("advise after a piped echo", `echo '{"question":"Merge?","options":["yes","no"],"recommendation":"yes"}' | admiral advise`, wantAllowed),
		apiFile("advise reading a file", `admiral advise < question.json`, map[string]string{"question.json": `{"question":"Merge?","options":["yes","no"],"recommendation":"yes"}`}, wantAllowed),
		mcp("mcp review COMMENT", "mcp__github__create_pull_request_review", map[string]any{"event": "COMMENT"}, wantAllowed),
		mcp("mcp tool that is not a review", "mcp__github__get_pull_request", map[string]any{"event": "APPROVE"}, wantAllowed),
		mcp("another tool", "Read", map[string]any{"file_path": "/tmp/x"}, wantAllowed),
	}
}

func TestGuardRules(t *testing.T) {
	for _, c := range guardTestCases() {
		t.Run(c.name, func(t *testing.T) {
			payload := c.payload(t)
			err, out, _ := runGuardTest(t, payload, "1")
			if out != "" {
				t.Errorf("stdout %q, want nothing: Claude reads a hook's stdout", out)
			}
			var denied *GuardDenied
			switch {
			case c.want == wantAllowed && err != nil:
				t.Errorf("Guard refused on an autopilot ship: %v, want it allowed\npayload %s", err, payload)
			case c.want != wantAllowed && !errors.As(err, &denied):
				t.Errorf("Guard returned %v on an autopilot ship, want a *GuardDenied for rule %s\npayload %s", err, c.want, payload)
			case c.want != wantAllowed && denied.Rule != c.want:
				t.Errorf("rule = %q, want %q\npayload %s", denied.Rule, c.want, payload)
			}
		})
	}
}

func TestGuardIsOffOutsideAutopilot(t *testing.T) {
	deny := guardTestCase{tool: "Bash", input: bashInput("gh pr review 12 --approve")}
	for _, v := range []string{"", "true", "11"} {
		t.Run("ADMIRAL_AUTOPILOT="+v, func(t *testing.T) {
			err, out, errb := runGuardTest(t, deny.payload(t), v)
			if err != nil || out != "" || errb != "" {
				t.Errorf("Guard = %v, stdout %q, stderr %q, want nil and no output", err, out, errb)
			}
		})
	}
	t.Run("a payload that is not JSON", func(t *testing.T) {
		if err, out, errb := runGuardTest(t, `{"tool_name":`, ""); err != nil || out != "" || errb != "" {
			t.Errorf("Guard = %v, stdout %q, stderr %q, want nil and no output", err, out, errb)
		}
	})
}

func TestGuardRefusesAPayloadItCannotRead(t *testing.T) {
	for _, p := range []string{`not json`, `null`, `{"tool_name":"Bash","tool_input":{"command":7}}`} {
		err, _, _ := runGuardTest(t, p, "1")
		var denied *GuardDenied
		if !errors.As(err, &denied) || denied.Rule != wantRuleUnparsed {
			t.Errorf("Guard(%q) = %v, want a *GuardDenied for rule %s", p, err, wantRuleUnparsed)
		}
	}
}

func TestGuardReason(t *testing.T) {
	deny := guardTestCase{tool: "Bash", input: bashInput("gh pr review 12 --approve")}
	err, out, errb := runGuardTest(t, deny.payload(t), "1")
	var denied *GuardDenied
	if !errors.As(err, &denied) {
		t.Fatalf("Guard = %v, want a *GuardDenied", err)
	}
	if strings.TrimSuffix(errb, "\n") != wantGuardReason || out != "" {
		t.Errorf("stderr %q, stdout %q, want the reason %q alone on stderr", errb, out, wantGuardReason)
	}
	if denied.Reason != wantGuardReason {
		t.Errorf("Reason = %q, want %q", denied.Reason, wantGuardReason)
	}
	if strings.Contains(errb+denied.Error(), "12 --approve") {
		t.Errorf("the refusal repeats the command: %q", errb)
	}

	ok := guardTestCase{tool: "Bash", input: bashInput("gh pr view 12")}
	if err, out, errb := runGuardTest(t, ok.payload(t), "1"); err != nil || out != "" || errb != "" {
		t.Errorf("an allowed call gave %v, stdout %q, stderr %q, want nothing", err, out, errb)
	}
}

// The text rule must run once on the line, not once per level, to stay inside
// Claude Code's hook timeout.
func TestGuardDecidesNestedSubstitutionsFast(t *testing.T) {
	const n = 40000
	cmd := "echo " + strings.Repeat("$(", n) + "true" + strings.Repeat(")", n) + "; gh api repos/o/r/pulls/1/reviews/2/events --input ev.json"
	deny := guardTestCase{tool: "Bash", input: bashInput(cmd)}
	start := time.Now()
	err, _, _ := runGuardTest(t, deny.payload(t), "1")
	took := time.Since(start)
	var denied *GuardDenied
	if !errors.As(err, &denied) {
		t.Errorf("Guard = %v, want a *GuardDenied", err)
	}
	if took > time.Second {
		t.Errorf("Guard took %v on %d nested substitutions, want well under a second", took, n)
	}
}
