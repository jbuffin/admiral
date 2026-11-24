package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const sha40 = "0123456789abcdef0123456789abcdef01234567"

// can answers the REST call METHOD endpoint; keys sort in call order.
func (f *fakeGH) can(t *testing.T, n int, method, endpoint string, status int, body any) {
	t.Helper()
	key := fmt.Sprintf("rest/k%03d", n)
	f.write(t, key+".endpoint", endpoint)
	f.write(t, key+".method", method)
	f.write(t, key+".status", fmt.Sprint(status))
	b, _ := json.Marshal(body)
	f.write(t, key+".json", string(b))
}

// endpoints lists the REST endpoints gh was asked for, in call order.
func (f *fakeGH) endpoints(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, c := range f.calls(t, "api") {
		out = append(out, c.argv[len(c.argv)-1])
	}
	return out
}

func fileAnswer(text string) map[string]any {
	return map[string]any{"sha": strings.Repeat("b", 40), "size": len(text), "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(text))}
}

func signedIn(t *testing.T) (*fakeGH, *Client) {
	t.Helper()
	f := newFake(t)
	f.write(t, "token", "fake-token-work-2\n")
	return f, NewClient("github.com", "", nil, f.env)
}

func TestRepoReadsTheDefaultBranch(t *testing.T) {
	f, c := signedIn(t)
	f.can(t, 1, "GET", "repos/o/r", 200, map[string]any{"full_name": "o/r", "private": false, "permissions": map[string]any{"push": true}, "default_branch": "trunk"})
	info, err := c.Repo(context.Background(), "o", "r")
	if err != nil || info.DefaultBranch != "trunk" {
		t.Errorf("Repo = %+v, %v; want default branch trunk", info, err)
	}
}

func TestResolveCommitEscapesASlashInTheRefAndReturnsTheCommit(t *testing.T) {
	for _, c := range []struct{ ref, endpoint string }{
		{"main", "repos/o/r/commits/main"},
		{"feature/x", "repos/o/r/commits/feature%2Fx"},
		{sha40, "repos/o/r/commits/" + sha40},
	} {
		t.Run(c.ref, func(t *testing.T) {
			f, cl := signedIn(t)
			f.can(t, 1, "GET", c.endpoint, 200, map[string]any{"sha": sha40})
			got, err := cl.ResolveCommit(context.Background(), "o", "r", c.ref)
			if err != nil || got != sha40 {
				t.Errorf("ResolveCommit = %q, %v; want %s", got, err, sha40)
			}
			if eps := f.endpoints(t); len(eps) != 1 || eps[0] != c.endpoint {
				t.Errorf("endpoints %v; want [%s]", eps, c.endpoint)
			}
		})
	}
}

func TestResolveCommitRefusesAnAnswerThatIsNotA40HexCommit(t *testing.T) {
	f, c := signedIn(t)
	f.can(t, 1, "GET", "repos/o/r/commits/main", 200, map[string]any{"sha": "abc123"})
	if got, err := c.ResolveCommit(context.Background(), "o", "r", "main"); err == nil {
		t.Errorf("ResolveCommit = %q, nil; want an error for a short sha", got)
	}
}

func TestResolveCommitKeepsGitHubsStatusOnA404And422(t *testing.T) {
	for _, status := range []int{404, 422} {
		f, c := signedIn(t)
		f.can(t, 1, "GET", "repos/o/r/commits/nope", status, map[string]any{})
		f.write(t, fmt.Sprintf("rest/k001.stderr"), fmt.Sprintf("gh: No commit found (HTTP %d)", status))
		_, err := c.ResolveCommit(context.Background(), "o", "r", "nope")
		if got := HTTPStatus(err); got != status {
			t.Errorf("status %d: HTTPStatus = %d (%v)", status, got, err)
		}
	}
}

func TestReadFileAtAsksForTheCommitAndReturnsTheBytesAndBlob(t *testing.T) {
	f, c := signedIn(t)
	endpoint := "repos/o/r/contents/crew/x.md?ref=" + sha40
	f.can(t, 1, "GET", endpoint, 200, fileAnswer("hello\n"))
	got, err := c.ReadFileAt(context.Background(), "o", "r", "crew/x.md", sha40)
	if err != nil || string(got.Bytes) != "hello\n" || got.Blob != strings.Repeat("b", 40) {
		t.Errorf("ReadFileAt = %+v, %v", got, err)
	}
	if eps := f.endpoints(t); len(eps) != 1 || eps[0] != endpoint {
		t.Errorf("endpoints %v; want [%s]", eps, endpoint)
	}
}

func TestReadFileAtRefusesAFileOver64KiB(t *testing.T) {
	f, c := signedIn(t)
	f.can(t, 1, "GET", "repos/o/r/contents/crew/x.md?ref="+sha40, 200, fileAnswer(strings.Repeat("x", MaxFileBytes+1)))
	if _, err := c.ReadFileAt(context.Background(), "o", "r", "crew/x.md", sha40); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v; want ErrTooLarge", err)
	}
}

func TestReadFileKeepsItsEndpointWithNoRef(t *testing.T) {
	f, c := signedIn(t)
	f.can(t, 1, "GET", "repos/o/r/contents/crew/x.md", 200, fileAnswer("hi"))
	if _, err := c.ReadFile(context.Background(), "o", "r", "crew/x.md"); err != nil {
		t.Fatal(err)
	}
	if eps := f.endpoints(t); len(eps) != 1 || eps[0] != "repos/o/r/contents/crew/x.md" {
		t.Errorf("endpoints %v", eps)
	}
}

func TestTagAtReturnsTheFirstTagOnTheCommitOrNone(t *testing.T) {
	other := strings.Repeat("9", 40)
	tags := []map[string]any{
		{"name": "v0.9", "commit": map[string]any{"sha": other}},
		{"name": "v1.0", "commit": map[string]any{"sha": sha40}},
		{"name": "v1.0-rc", "commit": map[string]any{"sha": sha40}},
	}
	for _, c := range []struct{ name, sha, want string }{
		{"first of two matches", sha40, "v1.0"},
		{"no tag on the commit", strings.Repeat("7", 40), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, cl := signedIn(t)
			f.can(t, 1, "GET", "repos/o/r/tags?per_page=100", 200, tags)
			got, err := cl.TagAt(context.Background(), "o", "r", c.sha)
			if err != nil || got != c.want {
				t.Errorf("TagAt = %q, %v; want %q", got, err, c.want)
			}
			if eps := f.endpoints(t); len(eps) != 1 || eps[0] != "repos/o/r/tags?per_page=100" {
				t.Errorf("endpoints %v; want one first-page call", eps)
			}
		})
	}
}
