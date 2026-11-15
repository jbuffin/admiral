package github

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// restGH is a gh that records each api call's argv, stdin and token variables and answers from files.
const restGHScript = `#!/bin/sh
d="$FAKEGH_REST"
mkdir -p "$d/calls"
if [ "$1" = auth ]; then printf '%s\n' "$*" > "$d/auth.args"; echo fake-token-work-2; exit 0; fi
n=$(ls "$d/calls" | grep -c argv)
printf '%s\0' "$@" > "$d/calls/$n.argv"
printf '%s|%s\n' "$GH_TOKEN" "$GH_ENTERPRISE_TOKEN" > "$d/calls/$n.token"
case "$*" in *"--input -"*) cat > "$d/calls/$n.stdin" ;; esac
if [ -e "$d/fail" ]; then cat "$d/fail" >&2; exit 1; fi
cat "$d/answer"
`

type restGH struct {
	dir string
	env []string
	gh  string
}

func newRestGH(t *testing.T, answer string) *restGH {
	t.Helper()
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	for p, body := range map[string]string{gh: restGHScript, filepath.Join(dir, "answer"): answer} {
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &restGH{dir: dir, gh: gh, env: append(os.Environ(), "FAKEGH_REST="+dir)}
}

func (g *restGH) client(host, account string) *Client {
	c := NewClient(host, account, nil, g.env)
	c.Gh = g.gh
	return c
}

// only is the one api call's args, stdin and token variables.
func (g *restGH) only(t *testing.T) (args []string, stdin, token string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(g.dir, "calls", "*.argv"))
	if len(files) != 1 {
		t.Fatalf("want one gh api call, got %d", len(files))
	}
	raw, _ := os.ReadFile(files[0])
	in, _ := os.ReadFile(strings.TrimSuffix(files[0], ".argv") + ".stdin")
	tok, _ := os.ReadFile(strings.TrimSuffix(files[0], ".argv") + ".token")
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"), string(in), strings.TrimSpace(string(tok))
}

func TestIssueRecordReadsTheRESTIdAndTheParentOfAnIssue(t *testing.T) {
	g := newRestGH(t, `{"id":4000000031,"number":31,"title":"x","parent_issue_url":"https://api.github.com/repos/acme/widgets/issues/5"}`)
	got, err := g.client("ghe.example.com", "work").IssueRecord(context.Background(), "acme", "widgets", 31)
	if err != nil || got.RESTID != 4000000031 || got.ParentURL != "https://api.github.com/repos/acme/widgets/issues/5" {
		t.Fatalf("IssueRecord = %+v, %v; want REST id 4000000031 (not the number) and the parent url", got, err)
	}
	args, stdin, token := g.only(t)
	if want := "api --method GET --hostname ghe.example.com repos/acme/widgets/issues/31"; strings.Join(args, " ") != want || stdin != "" {
		t.Errorf("gh ran %q with stdin %q; want %q and no stdin", strings.Join(args, " "), stdin, want)
	}
	if token != "|fake-token-work-2" {
		t.Errorf("token variables = %q; the fleet account's token goes in GH_ENTERPRISE_TOKEN on a GHE Server host", token)
	}
	if auth, _ := os.ReadFile(filepath.Join(g.dir, "auth.args")); strings.TrimSpace(string(auth)) != "auth token -h ghe.example.com -u work" {
		t.Errorf("token came from %q; want the client's own account", auth)
	}
}

func TestIssueRecordLeavesTheParentEmptyWhenThereIsNone(t *testing.T) {
	g := newRestGH(t, `{"id":4000000031,"number":31,"parent_issue_url":null}`)
	got, err := g.client("github.com", "").IssueRecord(context.Background(), "acme", "widgets", 31)
	if err != nil || got.RESTID != 4000000031 || got.ParentURL != "" {
		t.Errorf("IssueRecord = %+v, %v; want no parent", got, err)
	}
	if _, _, token := g.only(t); token != "fake-token-work-2|" {
		t.Errorf("token variables = %q; github.com reads GH_TOKEN", token)
	}
}

func TestCreateIssueSendsTitleAndBodyOnStdinAndReturnsNumberAndURL(t *testing.T) {
	g := newRestGH(t, `{"id":4000000099,"number":99,"html_url":"https://github.com/acme/widgets/issues/99"}`)
	title, body := `Orders "quoted" -f x=y`, "line one\nline two"
	got, err := g.client("github.com", "").CreateIssue(context.Background(), "acme", "widgets", title, body)
	if err != nil || got.Number != 99 || got.URL != "https://github.com/acme/widgets/issues/99" {
		t.Fatalf("CreateIssue = %+v, %v; want 99 and its html_url", got, err)
	}
	args, stdin, _ := g.only(t)
	if want := "api --method POST --hostname github.com --input - repos/acme/widgets/issues"; strings.Join(args, " ") != want {
		t.Errorf("gh ran %q; want %q", strings.Join(args, " "), want)
	}
	if want := `{"body":"line one\nline two","title":"Orders \"quoted\" -f x=y"}`; !sameJSON(stdin, want) {
		t.Errorf("stdin = %q; want a JSON object with the title and body", stdin)
	}
	for _, a := range args {
		if a == "-f" || a == "-F" || strings.Contains(a, "quoted") {
			t.Errorf("arg %q carries body text; it belongs on stdin", a)
		}
	}
}

func TestAddSubIssuePostsTheChildsRESTIdToTheParentsSubIssues(t *testing.T) {
	g := newRestGH(t, `{"id":1}`)
	if err := g.client("github.com", "").AddSubIssue(context.Background(), "acme", "widgets", 99, 4000000031); err != nil {
		t.Fatalf("AddSubIssue: %v", err)
	}
	args, stdin, _ := g.only(t)
	if want := "api --method POST --hostname github.com --input - repos/acme/widgets/issues/99/sub_issues"; strings.Join(args, " ") != want {
		t.Errorf("gh ran %q; want %q", strings.Join(args, " "), want)
	}
	if !sameJSON(stdin, `{"sub_issue_id":4000000031}`) {
		t.Errorf("stdin = %q; want {\"sub_issue_id\":4000000031} and never replace_parent", stdin)
	}
}

func TestTheIssueWritesReturnGhsMessageWhenGhFails(t *testing.T) {
	g := newRestGH(t, "")
	if err := os.WriteFile(filepath.Join(g.dir, "fail"), []byte("HTTP 422: Validation Failed (sub-issue already has a parent)"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, ctx := g.client("github.com", ""), context.Background()
	if _, err := c.IssueRecord(ctx, "acme", "widgets", 31); err == nil || !strings.Contains(err.Error(), "HTTP 422") {
		t.Errorf("IssueRecord error = %v; want gh's message", err)
	}
	if _, err := c.CreateIssue(ctx, "acme", "widgets", "t", "b"); err == nil || !strings.Contains(err.Error(), "HTTP 422") {
		t.Errorf("CreateIssue error = %v; want gh's message", err)
	}
	if err := c.AddSubIssue(ctx, "acme", "widgets", 99, 1); err == nil || !strings.Contains(err.Error(), "HTTP 422") {
		t.Errorf("AddSubIssue error = %v; want gh's message", err)
	}
}

func sameJSON(a, b string) bool {
	var x, y any
	return json.Unmarshal([]byte(a), &x) == nil && json.Unmarshal([]byte(b), &y) == nil && reflect.DeepEqual(x, y)
}
