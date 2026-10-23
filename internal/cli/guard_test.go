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

// `admiral guard pretooluse` (#115) refuses, on an autopilot ship, a tool call
// that would submit a pull-request review as APPROVE or REQUEST_CHANGES. Guard
// reads Claude's PreToolUse JSON and returns nil to allow, or a *GuardDenied to
// refuse; the rule is the code the outcome line carries. Each row runs through
// Guard itself, so the JSON decoding and the command parsing are exercised
// together. Rows come from the issue's "Desired behavior" and acceptance
// criteria; rows marked [D] are rules the issue leaves open and the coordinator decided.

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

// bashInput is a Bash tool call's tool_input.
func bashInput(cmd string) map[string]any { return map[string]any{"command": cmd} }

type guardTestCase struct {
	name  string
	tool  string
	input map[string]any
	files map[string]string // written in the payload's cwd
	want  string            // the rule of a refusal, or wantAllowed
}

// runGuardTest runs Guard on a payload with ADMIRAL_AUTOPILOT set to autopilot (unset
// when empty) and no home to record in. It returns Guard's error and stderr.
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
		// The issue's seven refused commands (AC1), verbatim.
		review("AC1 pr review --approve", `gh pr review 12 --approve`),
		review("AC1 pr review -a -b", `gh pr review 12 -a -b ok`),
		review("AC1 request changes after &&", `cd x && gh pr review 12 -r -b no`),
		review("AC1 bash -c", `bash -c "gh pr review 12 --approve"`),
		b("AC1 api reviews event=APPROVE", `gh api repos/o/r/pulls/12/reviews -f event=APPROVE`, wantRuleGhApiEvent),
		b("AC1 api submit a pending review", `gh api -X POST repos/o/r/pulls/12/reviews/9/events -f event=APPROVE`, wantRuleGhApiSubmit),
		b("AC1 graphql submitPullRequestReview", `gh api graphql -f query='`+gqlSubmitApprove+`'`, wantRuleGraphqlReview),

		// gh pr review in other forms.
		review("request-changes long flag", `gh pr review 12 --request-changes -b no`),
		review("flag before the number", `gh pr review --approve 12`),
		review("approve=true", `gh pr review 12 --approve=true`),
		review("approve=false is over-refused [D]", `gh pr review 12 --approve=false`),
		review("short cluster -ab", `gh pr review 12 -ab ok`),
		review("after ;", `ls; gh pr review 12 -a`),
		review("after a pipe", `echo x | gh pr review 12 --approve`),
		review("after ||", `false || gh pr review 12 -a`),
		review("after a newline", "ls\ngh pr review 12 -a"),
		review("after a background &", `sleep 1 & gh pr review 12 -a`),
		review("sh -c with single quotes", `sh -c 'gh pr review 12 -r -b no'`),
		review("zsh -c [D]", `zsh -c 'gh pr review 12 -a'`),
		review("env wrapper with an assignment", `FOO=1 env gh pr review 12 -a`),
		review("command wrapper", `command gh pr review 12 -a`),
		review("absolute path to gh", `/opt/homebrew/bin/gh pr review 12 -a`),
		review("command substitution", `echo $(gh pr review 12 --approve)`),
		review("backticks", "echo `gh pr review 12 --approve`"),
		review("a flag after the body", `gh pr review 12 -b ok --approve`),

		// gh api to the reviews endpoint.
		b("api -F event=REQUEST_CHANGES", `gh api `+reviews+` -F event=REQUEST_CHANGES`, wantRuleGhApiEvent),
		b("api placeholders and a lowercase value", `gh api /repos/{owner}/{repo}/pulls/12/reviews --raw-field event=approve`, wantRuleGhApiEvent),
		b("api full URL, --field with quotes", `gh api https://api.github.com/repos/o/r/pulls/12/reviews --field 'event=APPROVE'`, wantRuleGhApiEvent),
		b("api --method=POST attached", `gh api --method=POST `+reviews+` -f event=APPROVE`, wantRuleGhApiEvent),
		b("api -X GET is refused whatever the method", `gh api -X GET `+reviews+` -f event=APPROVE`, wantRuleGhApiEvent),
		b("api attached -fevent=", `gh api `+reviews+` -fevent=APPROVE`, wantRuleGhApiEvent),
		b("api query string on the endpoint", `gh api "`+reviews+`?per_page=1" -f event=APPROVE`, wantRuleGhApiEvent),
		apiFile("api --input file holding APPROVE", `gh api `+reviews+` --input body.json`, map[string]string{"body.json": `{"event":"APPROVE"}`}, wantRuleGhApiEvent),
		apiFile("api --input file that is missing", `gh api `+reviews+` --input body.json`, nil, wantRuleGhApiEvent),
		apiFile("api --input file that is not JSON", `gh api `+reviews+` --input body.json`, map[string]string{"body.json": `not json`}, wantRuleGhApiEvent),
		b("api --input - with a heredoc body", "gh api "+reviews+" --input - <<'EOF'\n{\"body\": \"x\", \"event\": \"REQUEST_CHANGES\"}\nEOF", wantRuleGhApiEvent),
		apiFile("api -F event=@file holding APPROVE", `gh api `+reviews+` -F event=@ev.txt`, map[string]string{"ev.txt": "APPROVE\n"}, wantRuleGhApiEvent),
		apiFile("api -F event=@file that is missing", `gh api `+reviews+` -F event=@ev.txt`, nil, wantRuleGhApiEvent),
		b("api -F event=@- [D]", `gh api `+reviews+` -F event=@-`, wantRuleGhApiEvent),
		b("api submit endpoint with COMMENT is refused too [D]", `gh api -X POST repos/o/r/pulls/12/reviews/9/events -f event=COMMENT`, wantRuleGhApiSubmit),
		b("api submit endpoint, attached -XPOST, no event", `gh api -XPOST repos/o/r/pulls/12/reviews/9/events`, wantRuleGhApiSubmit),

		// gh api graphql.
		b("graphql addPullRequestReview APPROVE", `gh api graphql -f query='`+gqlAddApprove+`'`, wantRuleGraphqlReview),
		b("graphql submitPullRequestReview REQUEST_CHANGES", `gh api graphql -f query='`+gqlSubmitChanges+`'`, wantRuleGraphqlReview),
		b("graphql event in a variable", `gh api graphql -f query='`+gqlSubmitVariable+`' -f e=APPROVE`, wantRuleGraphqlReview),
		b("graphql event in a -F variable, lowercase [D]", `gh api graphql -F query='`+gqlSubmitVariable+`' -F e=approve`, wantRuleGraphqlReview),
		apiFile("graphql --input body with the variable", `gh api graphql --input q.json`, map[string]string{"q.json": gqlSubmitInputBody}, wantRuleGraphqlReview),

		// Round 1: shapes that hid the gh call from the first parser.
		review("for loop", `for n in 1 2; do gh pr review $n -a; done`),
		review("while loop", `while read n; do gh pr review "$n" --approve; done < prs`),
		review("negation", `! gh pr review 12 -a`),
		review("function body", `f() { gh pr review 12 -a; }; f`),
		review("heredoc body with an apostrophe inside $(…)", "gh pr review 12 -a -b \"$(cat <<'EOF'\nIt's fine\nEOF\n)\""),
		review("request changes, body with an apostrophe", "gh pr review 12 -r -b \"$(cat <<'EOF'\nDon't merge\nEOF\n)\""),
		review("redirect 2>&1 before the flag", `gh pr review 12 2>&1 -a`),
		review("redirect >/dev/null 2>&1 before the flag", `gh pr review 12 >/dev/null 2>&1 --approve`),
		review("a leading redirect", `2>/dev/null gh pr review 12 --approve`),
		review("env -u NAME", `env -u X gh pr review 12 --approve`),
		review("env -S", `env -S 'gh pr review 12 --approve'`),
		review("exec -a NAME", `exec -a x gh pr review 12 --approve`),
		review("timeout", `timeout 60 gh pr review 12 --approve`),
		review("sudo -u", `sudo -u bob gh pr review 12 --approve`),
		review("xargs", `echo 1 | xargs -I{} gh pr review {} --approve`),
		review("gh -R before pr", `gh -R o/r pr review 12 --approve`),
		review("gh pr -R before review", `gh pr -R o/r review 12 -a`),
		review("gh pr --repo= before review", `gh pr --repo=o/r review 12 --approve`),
		review("gh --hostname before pr", `gh --hostname h pr review 12 -a`),
		review("bash -c -- script", `bash -c -- 'gh pr review 12 --approve'`),
		review("eval", `eval 'gh pr review 12 --approve'`),
		review("bash reading a heredoc", "bash <<'EOF'\ngh pr review 12 --approve\nEOF"),
		review("bash reading a here-string", `bash <<< 'gh pr review 12 -a'`),
		review("a backslash delimiter hides nothing", "cat <<\\EOF\nx\nEOF\ngh pr review 12 --approve"),
		b("bash reading a pipe falls back to the text rule", `echo 'gh pr review 12 --approve' | bash`, wantRuleUnparsed),
		b("bash running a file is refused (#120 D8)", `bash review-approve.sh`, "shell_script"),
		review("a program from a substitution", `$(which gh) pr review 12 --approve`),
		review("a program from a variable", `GH=gh; $GH pr review 12 -a`),
		b("api endpoint with a fragment", `gh api 'repos/o/r/pulls/12/reviews#x' -f event=APPROVE`, wantRuleGhApiEvent),
		b("api -F event=@/dev/stdin", `echo APPROVE | gh api repos/o/r/pulls/12/reviews -F event=@/dev/stdin`, wantRuleGhApiEvent),
		b("api repositories route", `gh api repositories/1234/pulls/12/reviews -f event=APPROVE`, wantRuleGhApiEvent),
		b("api repositories submit route", `gh api -X POST repositories/1234/pulls/12/reviews/9/events`, wantRuleGhApiSubmit),
		b("api --hostname before the endpoint", `gh api --hostname ghe.example repos/o/r/pulls/12/reviews -f event=APPROVE`, wantRuleGhApiEvent),
		b("api --input - with escaped quotes", `echo "{\"event\":\"APPROVE\"}" | gh api repos/o/r/pulls/12/reviews --input -`, wantRuleGhApiEvent),
		b("api --input - with a jq literal", `jq -n '{event: "APPROVE"}' | gh api repos/o/r/pulls/12/reviews --input -`, wantRuleGhApiEvent),
		b("api --input - with a here-string", `gh api repos/o/r/pulls/12/reviews --input - <<< "{\"event\":\"APPROVE\"}"`, wantRuleGhApiEvent),
		b("graphql with a space before the colon", `gh api graphql -f query='mutation { submitPullRequestReview(input: {pullRequestId: "X", event : APPROVE}) { clientMutationId } }'`, wantRuleGraphqlReview),
		b("graphql variable default", `gh api graphql -f query='mutation($e: PullRequestReviewEvent = APPROVE) { submitPullRequestReview(input: {event: $e}) { clientMutationId } }'`, wantRuleGraphqlReview),
		apiFile("graphql --input body with a nested variable", `gh api graphql --input q.json`, map[string]string{"q.json": `{"query":"mutation($i: SubmitPullRequestReviewInput!) { submitPullRequestReview(input: $i) { clientMutationId } }","variables":{"i":{"pullRequestReviewId":"x","event":"APPROVE"}}}`}, wantRuleGraphqlReview),
		apiFile("graphql variable read from a file", `gh api graphql -f query='`+gqlSubmitVariable+`' -F e=@ev.txt`, map[string]string{"ev.txt": "APPROVE\n"}, wantRuleGraphqlReview),
		b("graphql on a GHE host", `gh api https://ghe.example/api/graphql -f query='`+gqlSubmitApprove+`'`, wantRuleGraphqlReview),

		// Round 2: expansions, parse errors, shell options, the fallbacks.
		b("api reviews, $PR in the endpoint", `gh api repos/o/r/pulls/$PR/reviews -f event=APPROVE`, wantRuleGhApiEvent),
		b("api reviews, $REPO spanning owner and repo", `gh api "repos/$REPO/pulls/1/reviews" -f event=APPROVE`, wantRuleGhApiEvent),
		b("api submit, $ID in the endpoint", `gh api repos/o/r/pulls/1/reviews/$ID/events -f event=APPROVE`, wantRuleGhApiSubmit),
		b("api submit COMMENT, $PR and $ID", `gh api repos/o/r/pulls/$PR/reviews/$ID/events -f event=COMMENT`, wantRuleGhApiSubmit),
		b("api reviews, event from a variable", `gh api repos/o/r/pulls/1/reviews -f event="$EV"`, wantRuleGhApiEvent),
		b("graphql, query from a substitution beside APPROVE", `gh api graphql -f query="$(cat q.graphql)" -f event=APPROVE`, wantRuleGraphqlReview),
		b("graphql, exact mutation with a variable value from a variable", `gh api graphql -f query='`+gqlSubmitVariable+`' -f e="$EV"`, wantRuleGraphqlReview),
		review("pr review, a flag from a variable", `F=--approve; gh pr review 1 $F`),
		review("pr review, a flag from a substitution", `gh pr review 12 $(printf -- -a)`),
		review("pr review, a long flag with a variable inside", `gh pr review 1 "--${A}"`),
		review("pr review, an unknown program word followed by pr review", `"$GH" pr review 1 -r`),
		review("a -c script with an expansion", `for n in 1 2; do bash -c "gh pr review $n -a"; done`),
		review("a parse error after the call (zsh syntax)", `gh pr review 1 -a; echo ${(f)x}`),
		review("a parse error after the call (zsh =(…))", `gh pr review 1 -a; echo =(true)`),
		review("an unterminated quote on a later line", "gh pr review 1 -a\necho \"x"),
		b("a parse error after an api submit", `gh api repos/o/r/pulls/1/reviews/2/events -f event=COMMENT; echo ${(f)x}`, wantRuleGhApiSubmit),
		review("bash -o VALUE before -c", `bash -o pipefail -c 'gh pr review 1 -a'`),
		review("bash --rcfile VALUE before -c", `bash --rcfile x -c 'gh pr review 1 -a'`),
		review("bash -ceo pipefail", `bash -ceo pipefail 'gh pr review 1 --approve'`),
		review("dash -c", `dash -c 'gh pr review 1 --approve'`),
		review("env -iS", `env -iS 'gh pr review 12 --approve'`),
		review("sudo -Eu", `sudo -Eu x gh pr review 1 --approve`),
		review("env -P (BSD)", `env -P /usr/bin gh pr review 1 --approve`),
		review("xargs -J (BSD)", `xargs -J % gh pr review % --approve`),
		review("zsh noglob", `noglob gh pr review 1 -a`),
		review("zsh =gh", `=gh pr review 1 -a`),
		b("a pipe into bash, short flag", `echo 'gh pr review 1 -a' | bash`, wantRuleUnparsed),
		b("a process substitution into bash, short flag", `bash <(echo 'gh pr review 1 -a')`, "shell_script"),
		b("a command whose output is the program, short flag", `$(echo gh pr review 1 -a)`, wantRuleUnparsed),

		// Round 4: the text rule only where a call could hide a submit.
		b("pr create with review and -race in a heredoc body", "gh pr create --title \"Add the guard\" --body \"$(cat <<'EOF'\nReady for review.\n- go test -race ./...\nEOF\n)\"", wantAllowed),
		b("a reply to a review comment, $PR and $CID", `gh api repos/o/r/pulls/$PR/comments/$CID/replies -f body="Fixed per review; checked with grep -r foo"`, wantAllowed),
		b("pr comment mentioning review and -run, $PR", `gh pr comment $PR --body "Addressed the review: ran go test -run TestX"`, wantAllowed),
		b("api reviews --jq naming APPROVED", `gh api repos/o/r/pulls/$PR/reviews --jq '.[] | select(.state=="APPROVED") | .id'`, wantAllowed),
		b("pr view piped into jq -r", `gh pr view "$PR" --json reviews | jq -r '.reviews[].state'`, wantAllowed),
		b("pr review --comment with an unquoted body", `gh pr review 1 --comment -b $M`, wantAllowed),
		b("graphql addPullRequestReview, only the pull request in a variable", `gh api graphql -f query='mutation($id:ID!){addPullRequestReview(input:{pullRequestId:$id}){clientMutationId}}' -f id="$PR_ID"`, wantAllowed),
		b("graphql mutation with COMMENT and an expansion elsewhere (refused since #120: pass $ID as a variable)", `gh api graphql -f query="mutation{addPullRequestReview(input:{pullRequestId:\"$ID\",event:COMMENT}){clientMutationId}}"`, "graphql_merge"),
		b("graphql event bound to a variable from an expansion", `gh api graphql -f query='mutation($e:PullRequestReviewEvent!,$id:ID!){addPullRequestReview(input:{pullRequestId:$id,event:$e}){clientMutationId}}' -f e="$E" -f id="$PR_ID"`, wantRuleGraphqlReview),
		b("graphql event position is an expansion", `gh api graphql -f query="mutation{submitPullRequestReview(input:{pullRequestReviewId:\"x\",event:$E}){clientMutationId}}"`, wantRuleGraphqlReview),
		b("an expansion in single quotes of a -c script (event)", `E=APPROVE; bash -c "gh api repos/o/r/pulls/1/reviews -f event='$E'"`, wantRuleGhApiEvent),
		review("an expansion in single quotes of a -c script (flag)", `X=--approve; bash -c "gh pr review 1 '$X'"`),
		review("pr review, a flag in the PR slot", `F=--approve; gh pr review $F 12`),
		review("pr review, only an expansion", `gh pr review "$F"`),
		review("pr review, an unquoted variable that splits", `X='1 -a'; gh pr review $X`),
		b("a backtick program, short flag next to the quote", "`echo gh pr review 1 -a`", wantRuleUnparsed),
		b("a pipe into bash, short flag before a backslash", `printf 'gh pr review 1 -a\n' | bash`, wantRuleUnparsed),
		b("api submit under a leading expansion", `gh api "$BASE/pulls/1/reviews/$ID/events" -f event=COMMENT`, wantRuleGhApiSubmit),
		b("api with only an expansion for the endpoint", `gh api "$U" -f event=APPROVE`, wantRuleGhApiEvent),
		b("api reviews, a field key from an expansion", `gh api repos/o/r/pulls/1/reviews -f "$K=$V"`, wantRuleGhApiEvent),
		b("api --input - with an expansion in a here-string", `gh api repos/o/r/pulls/1/reviews --input - <<< "{\"event\":\"$E\"}"`, wantRuleGhApiEvent),
		review("env --chdir= takes no next word", `env --chdir= gh pr review 1 -a`),
		review("zsh repeat", `repeat 1 gh pr review 1 -a`),
		review("a quoted heredoc keeps its backslashes", "bash <<'EOF'\necho \\'; gh pr review 1 -a; echo \\'\nEOF"),

		// Content set elsewhere on the line: where the guard has no text for what it
		// judges (a program, script, file or query that is only an expansion, or a
		// shell reading a file or pipe), the whole line is read for long spellings only.
		b("a program set in a variable", `CMD='gh pr review 1 --approve'; $CMD`, wantRuleUnparsed),
		b("eval of a variable", `C='gh pr review 1 --approve'; eval "$C"`, wantRuleUnparsed),
		b("bash -c of a variable", `C='gh pr review 1 --approve'; bash -c "$C"`, wantRuleUnparsed),
		b("a variable piped into bash", `C='gh pr review 1 --approve'; echo "$C" | bash`, wantRuleUnparsed),
		b("a heredoc of a variable fed to bash", "C='gh pr review 1 --approve'\nbash <<EOF\n$C\nEOF", wantRuleUnparsed),
		b("a script written to a file, then run", "cat > /tmp/r.sh <<'EOF'\ngh pr review 1 --approve\nEOF\nbash /tmp/r.sh", "shell_script"),
		b("a graphql query set in a variable", "QUERY='mutation { submitPullRequestReview(input: {pullRequestReviewId: \"PRR_x\", event: APPROVE}) { clientMutationId } }'\ngh api graphql -f query=\"$QUERY\"", wantRuleGraphqlReview),
		b("pr create with review and -race, then a script (refused since #120 D8)", "gh pr create --title t --body \"$(cat <<'EOF'\nReady for review. go test -race ./...\nEOF\n)\"; bash scripts/check.sh", "shell_script"),
		b("a harmless query in a variable, after a body that mentions review (refused since #120: the guard cannot see a variable)", "Q='query{viewer{login}}'; gh pr create --title t --body \"$(cat <<'EOF'\nReady for review.\n- go test -race ./...\nEOF\n)\"; gh api graphql -f query=\"$Q\"", "graphql_merge"),
		b("pr review --comment then a script (refused since #120 D8)", `gh pr review $PR --comment -b "$(cat body.md)"; bash scripts/lint.sh`, "shell_script"),
		b("a path with an expansion after a body that asks for review and approval", `gh pr create --title t --body 'Please review and approve when ready'; $HOME/bin/lint`, wantAllowed),
		b("a script name with an expansion after the same body", `gh pr create --title t --body 'Please review and approve when ready'; ./scripts/$TASK.sh`, wantAllowed),
		b("a graphql query that is an expansion between newlines", "QUERY='\nmutation { submitPullRequestReview(input: {pullRequestReviewId: \"PRR_x\", event: APPROVE}) { clientMutationId } }'\ngh api graphql -f query=\"\n$QUERY\n\"", wantRuleGraphqlReview),
		b("pr view of APPROVED reviews", `gh pr view $PR --json reviews --jq '.reviews[] | select(.state=="APPROVED")'`, wantAllowed),
		// Known gap: the whole line has no short flags, or every line with review and -r would be refused.
		b("a program set in a variable, short flag (known gap)", `CMD='gh pr review 1 -a'; $CMD`, wantAllowed),

		// MCP tools.
		mcp("mcp create review APPROVE", "mcp__github__create_pull_request_review", map[string]any{"event": "APPROVE"}, wantRuleMcpReview),
		mcp("mcp submit pending review APPROVE", "mcp__github__submit_pending_pull_request_review", map[string]any{"event": "APPROVE"}, wantRuleMcpReview),
		mcp("mcp request_changes in lowercase [D]", "mcp__github__create_pull_request_review", map[string]any{"event": "request_changes"}, wantRuleMcpReview),

		// Fail closed.
		b("unparseable with review and approve", `echo "unterminated review approve`, wantRuleUnparsed),
		b("unparseable with review and request-changes", `echo 'unterminated review request-changes`, wantRuleUnparsed),
		b("unparseable with a trailing backslash [D]", `gh pr review 12 --approve \`, wantRuleUnparsed),
		{name: "Bash with no command [D]", tool: "Bash", input: map[string]any{}, want: wantRuleUnparsed},

		// Allowed on an autopilot ship: the issue's three (AC2).
		b("AC2 pr review --comment", `gh pr review 12 --comment -b hi`, wantAllowed),
		b("AC2 api reviews with no event", `gh api repos/o/r/pulls/12/reviews -f body=x`, wantAllowed),
		b("AC2 pr view", `gh pr view 12`, wantAllowed),

		// Allowed: edges of the rules.
		b("api reviews, nothing else", `gh api `+reviews, wantAllowed),
		apiFile("api --input file with no event", `gh api `+reviews+` --input body.json`, map[string]string{"body.json": `{"body":"x"}`}, wantAllowed),
		b("api review comments", `gh api repos/o/r/pulls/12/comments -f body=x`, wantAllowed),
		b("api -X GET with no event", `gh api -X GET `+reviews, wantAllowed),
		b("api event COMMENT on reviews", `gh api `+reviews+` -f event=COMMENT`, wantAllowed),
		b("short -c comment", `gh pr review 12 -c -b hi`, wantAllowed),
		b("-ba is a body of a", `gh pr review 12 -ba`, wantAllowed),
		b("-b -a has a body of -a", `gh pr review 12 -b -a`, wantAllowed),
		b("--body -a has a body of -a", `gh pr review 12 --body -a`, wantAllowed),
		b("a heredoc body is not a command [D]", "cat <<EOF\ngh pr review 12 --approve\nEOF", wantAllowed),
		b("git commit mentioning approve", `git commit -m "approve the plan"`, wantAllowed),
		b("unparseable without review", `echo "unterminated approve`, wantAllowed),
		b("ls", `ls -la`, wantAllowed),
		b("pr review comment with $PR and a body from $(…)", `gh pr review $PR --comment -b "$(cat body.md)"`, wantAllowed),
		b("api reviews with $PR and no event", `gh api repos/o/r/pulls/$PR/reviews -f body=x`, wantAllowed),
		b("api comments with $PR", `gh api repos/o/r/pulls/$PR/comments -f body="$B"`, wantAllowed),
		b("bash -o pipefail -c with a comment", `bash -o pipefail -c 'gh pr review 1 --comment -b hi'`, wantAllowed),
		b("a comment body that says approve, in a heredoc inside $(…)", "gh pr review 12 --comment -b \"$(cat <<'EOF'\nI can't approve this yet\nEOF\n)\"", wantAllowed),
		b("a loop of comments", `for n in 1 2; do gh pr review $n --comment -b hi; done`, wantAllowed),
		b("redirects around a comment", `gh pr review 12 --comment -b hi 2>&1 >/dev/null`, wantAllowed),
		b("api review comments under a fragment", `gh api 'repos/o/r/pulls/12/comments#x' -f body=x`, wantAllowed),
		b("graphql query that is not a review", `gh api graphql -f query='query { viewer { login } }'`, wantAllowed),
		mcp("mcp review COMMENT", "mcp__github__create_pull_request_review", map[string]any{"event": "COMMENT"}, wantAllowed),
		mcp("mcp review with no event", "mcp__github__create_pull_request_review", map[string]any{"body": "x"}, wantAllowed),
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

// AC1: every refused command is allowed when ADMIRAL_AUTOPILOT is not 1, so a ship
// launched by hand is unaffected. Only the exact value 1 turns the guard on.
func TestGuardIsOffOutsideAutopilot(t *testing.T) {
	for _, c := range guardTestCases() {
		if !strings.HasPrefix(c.name, "AC1 ") {
			continue
		}
		for _, v := range []string{"", "0", "true"} {
			t.Run(c.name+" with ADMIRAL_AUTOPILOT="+v, func(t *testing.T) {
				err, out, errb := runGuardTest(t, c.payload(t), v)
				if err != nil || out != "" || errb != "" {
					t.Errorf("Guard = %v, stdout %q, stderr %q, want nil and no output", err, out, errb)
				}
			})
		}
	}
	t.Run("a payload that is not JSON", func(t *testing.T) {
		if err, out, errb := runGuardTest(t, `{"tool_name":`, ""); err != nil || out != "" || errb != "" {
			t.Errorf("Guard = %v, stdout %q, stderr %q, want nil and no output", err, out, errb)
		}
	})
	t.Run("only the exact value 1 turns it on", func(t *testing.T) {
		deny := guardTestCase{tool: "Bash", input: bashInput("gh pr review 12 --approve")}
		if err, _, _ := runGuardTest(t, deny.payload(t), "11"); err != nil {
			t.Errorf("Guard with ADMIRAL_AUTOPILOT=11 = %v, want nil", err)
		}
	})
}

// A payload that is not a JSON object fails closed on an autopilot ship [D].
func TestGuardRefusesAPayloadItCannotRead(t *testing.T) {
	for _, p := range []string{``, `not json`, `{"tool_name":`, `null`, `["Bash"]`, `"gh pr review 12 --approve"`, `{"tool_name":"Bash","tool_input":{"command":7}}`, `{"tool_name":"Bash"}`} {
		err, _, _ := runGuardTest(t, p, "1")
		var denied *GuardDenied
		if !errors.As(err, &denied) || denied.Rule != wantRuleUnparsed {
			t.Errorf("Guard(%q) = %v, want a *GuardDenied for rule %s", p, err, wantRuleUnparsed)
		}
	}
}

// The refusal carries the reason the agent reads, exactly, on stderr and in
// the error; an allowed call writes nothing.
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

// A line of nested substitutions with a submit after it is decided in well under
// a second: the text rule runs once on the line, not once per level (round 3
// found 88 s at this size, past Claude Code's hook timeout).
func TestGuardDecidesNestedSubstitutionsFast(t *testing.T) {
	const n = 40000
	cmd := strings.Repeat("$(", n) + "true" + strings.Repeat(")", n) + "; gh api repos/o/r/pulls/1/reviews/2/events --input ev.json"
	deny := guardTestCase{tool: "Bash", input: bashInput(cmd)}
	start := time.Now()
	err, _, _ := runGuardTest(t, deny.payload(t), "1")
	took := time.Since(start)
	var denied *GuardDenied
	if !errors.As(err, &denied) || denied.Rule != wantRuleGhApiSubmit {
		t.Errorf("Guard = %v, want a *GuardDenied for rule %s", err, wantRuleGhApiSubmit)
	}
	if took > time.Second {
		t.Errorf("Guard took %v on %d nested substitutions, want well under a second", took, n)
	}
}
