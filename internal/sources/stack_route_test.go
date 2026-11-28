package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/github"
)

const testGuardHeader = "X-Test-Merge-Token"

// prDetail seeds GitHub's answer to the PR tab's detail query for one pull request, in no stack.
func (g *fakeGH) prDetail(url string, n int, head, base, author string) {
	g.t.Helper()
	b := fmt.Sprintf(`{"number":%d,"url":%q,"title":"PR %d","state":"OPEN","isDraft":false,"headRefName":%q,"baseRefName":%q,"author":{"login":%q},"baseRepository":{"defaultBranchRef":{"name":"main"}},"stack":null}`,
		n, url, n, head, base, author)
	if err := os.MkdirAll(filepath.Join(g.dir, "pr_detail"), 0o755); err != nil {
		g.t.Fatal(err)
	}
	g.write("pr_detail/"+strings.NewReplacer("/", "_", ":", "_").Replace(url), b+"\n")
}

// ghArgv are the argument lists of every gh call so far.
func (g *fakeGH) ghArgv() [][]string {
	g.t.Helper()
	files, _ := filepath.Glob(filepath.Join(g.dir, "calls", "call.*", "argv"))
	var out [][]string
	for _, f := range files {
		b, _ := os.ReadFile(f)
		out = append(out, strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00"))
	}
	return out
}

func (g *fakeGH) stackLinks() [][]string {
	var out [][]string
	for _, a := range g.ghArgv() {
		if len(a) >= 2 && a[0] == "stack" && a[1] == "link" {
			out = append(out, a)
		}
	}
	return out
}

func (g *fakeGH) detailReads() int {
	n := 0
	for _, a := range g.ghArgv() {
		if strings.Contains(strings.Join(a, " "), "pullRequest(number:") {
			n++
		}
	}
	return n
}

// stackFixture runs a GitHub source showing PRs 41 to 43; guarded says whether the route has a token check.
func stackFixture(t *testing.T, guarded bool, authors ...string) (*serviceFixture, *fakeGH) {
	t.Helper()
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	heads := []string{"b41", "b42", "b43"}
	var nodes []obj
	for i, a := range authors {
		base := "main"
		if i > 0 {
			base = heads[i-1]
		}
		n := 41 + i
		nodes = append(nodes, chainNode(n, heads[i], base, a, nil))
		g.prDetail(widgets+itoa(n), n, heads[i], base, a)
	}
	writeSearch(t, g, "bugs", "label:bug", nodes...)
	g.prDetail("https://github.com/acme/gadgets/pull/42", 42, "b42", "main", "fake-viewer")
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	if guarded {
		f.Svc.MergeGuard = func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(testGuardHeader) != "ok" {
					http.Error(w, `{"error":"needs the merge token"}`, http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
			})
		}
	}
	f.startSources()
	f.waitView("gha", "the bugs", func(s SourceView) bool { return len(s.orders("bugs")) == len(authors) })
	return f, g
}

func (f *serviceFixture) stack(key, token string, prs ...string) (int, string) {
	f.t.Helper()
	b, _ := json.Marshal(obj{"prs": prs})
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/sources/"+key+"/stack", bytes.NewReader(b))
	if token != "" {
		req.Header.Set(testGuardHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func wantCode(t *testing.T, what string, want, got int, body string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d: %s", what, got, want, body)
	}
}

func TestStackRouteIsRefusedWithoutTheMergeGuardsTokenAndRunsNoGh(t *testing.T) {
	for name, c := range map[string]struct {
		guarded bool
		token   string
	}{
		"no guard is wired":             {false, "ok"},
		"the guard finds no token":      {true, ""},
		"the guard finds a wrong token": {true, "nope"},
	} {
		t.Run(name, func(t *testing.T) {
			f, g := stackFixture(t, c.guarded, "fake-viewer", "fake-viewer")
			code, body := f.stack("gha", c.token, widgets+"41", widgets+"42")
			wantCode(t, "POST stack", http.StatusForbidden, code, body)
			if links := g.stackLinks(); len(links) != 0 || g.detailReads() != 0 {
				t.Errorf("a refused request ran gh: links %v, detail reads %d", links, g.detailReads())
			}
		})
	}
}

func TestStackRouteOfASourceThatIsNotThereIsNotFound(t *testing.T) {
	f, g := stackFixture(t, true, "fake-viewer", "fake-viewer")
	code, body := f.stack("nope", "ok", widgets+"41", widgets+"42")
	wantCode(t, "POST stack on an unknown source", http.StatusNotFound, code, body)
	if links := g.stackLinks(); len(links) != 0 {
		t.Errorf("gh stack link ran: %v", links)
	}
}

// closePR makes GitHub answer that the pull request is merged.
func closePR(url string) func(g *fakeGH) {
	return func(g *fakeGH) {
		name := "pr_detail/" + strings.NewReplacer("/", "_", ":", "_").Replace(url)
		b, err := os.ReadFile(filepath.Join(g.dir, name))
		if err != nil {
			g.t.Fatal(err)
		}
		g.write(name, strings.Replace(string(b), `"state":"OPEN"`, `"state":"MERGED"`, 1))
	}
}

func TestStackRouteRefusesWhatIsNotTheViewersChainBeforeAnyLink(t *testing.T) {
	for name, c := range map[string]struct {
		authors []string
		prs     []string
		reshape func(g *fakeGH)
	}{
		"a PR by someone else":    {[]string{"fake-viewer", "someone-else"}, []string{widgets + "41", widgets + "42"}, nil},
		"PRs that do not chain":   {[]string{"fake-viewer", "fake-viewer"}, []string{widgets + "41", widgets + "42"}, func(g *fakeGH) { g.prDetail(widgets+"42", 42, "b42", "main", "fake-viewer") }},
		"a closed PR":             {[]string{"fake-viewer", "fake-viewer"}, []string{widgets + "41", widgets + "42"}, closePR(widgets + "42")},
		"one PR alone":            {[]string{"fake-viewer", "fake-viewer"}, []string{widgets + "41"}, nil},
		"PRs in two repositories": {[]string{"fake-viewer", "fake-viewer"}, []string{widgets + "41", "https://github.com/acme/gadgets/pull/42"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			f, g := stackFixture(t, true, c.authors...)
			if c.reshape != nil {
				c.reshape(g)
			}
			code, body := f.stack("gha", "ok", c.prs...)
			wantCode(t, "POST stack", http.StatusConflict, code, body)
			if links := g.stackLinks(); len(links) != 0 {
				t.Errorf("a refused request ran gh stack link: %v", links)
			}
		})
	}
}

func TestStackRouteRefusesAPRNotOnTheSourcesHostBeforeReadingGitHub(t *testing.T) {
	f, g := stackFixture(t, true, "fake-viewer", "fake-viewer")
	code, body := f.stack("gha", "ok", widgets+"41", "https://ghe.example.com/acme/widgets/pull/42")
	wantCode(t, "POST stack with a PR on another host", http.StatusUnprocessableEntity, code, body)
	if g.detailReads() != 0 || len(g.stackLinks()) != 0 {
		t.Errorf("gh ran for a PR off the host: detail reads %d, links %v", g.detailReads(), g.stackLinks())
	}
}

func TestStackRouteLinksTheViewersChainBottomToTopOnTheBottomPRsBase(t *testing.T) {
	f, g := stackFixture(t, true, "fake-viewer", "fake-viewer", "fake-viewer")
	g.write("stack_link.number", "9\n")
	code, body := f.stack("gha", "ok", widgets+"43", widgets+"41", widgets+"42")
	wantCode(t, "POST stack", http.StatusOK, code, body)
	if want := []string{"stack", "link", "--base", "main", "41", "42", "43"}; len(g.stackLinks()) != 1 || !slices.Equal(g.stackLinks()[0], want) {
		t.Errorf("gh stack link calls = %v, want one: %v", g.stackLinks(), want)
	}
	var got struct {
		Shape string   `json:"shape"`
		PRs   []string `json:"prs"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Shape != "stacked" || !slices.Equal(got.PRs, []string{widgets + "41", widgets + "42", widgets + "43"}) {
		t.Errorf("answer = %s, want the stacked shape over 41, 42, 43", body)
	}
}

func TestStackRouteLinksOneChainAtATimePerSource(t *testing.T) {
	f, g := stackFixture(t, true, "fake-viewer", "fake-viewer")
	g.write("stack_link.sleep", "0.3\n")
	first := make(chan int, 1)
	go func() {
		code, _ := f.stack("gha", "ok", widgets+"41", widgets+"42")
		first <- code
	}()
	// The second POST goes only once the first's gh stack link is running, so the two always overlap.
	deadline := time.Now().Add(10 * time.Second)
	for len(g.stackLinks()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first POST never ran gh stack link")
		}
		time.Sleep(20 * time.Millisecond)
	}
	code, body := f.stack("gha", "ok", widgets+"41", widgets+"42")
	wantCode(t, "a second POST while a link is under way", http.StatusConflict, code, body)
	wantCode(t, "the first POST", http.StatusOK, <-first, "")
	if n := len(g.stackLinks()); n != 1 {
		t.Errorf("gh stack link ran %d times, want once", n)
	}
}

func TestStackRouteAnswersOKWhenTheLinkTakesLongerThanTheReadAllowance(t *testing.T) {
	old := readTimeout
	readTimeout = 300 * time.Millisecond
	t.Cleanup(func() { readTimeout = old })
	f, g := stackFixture(t, true, "fake-viewer", "fake-viewer")
	g.write("stack_link.sleep", "0.5\n")
	code, body := f.stack("gha", "ok", widgets+"41", widgets+"42")
	wantCode(t, "POST stack when the link outlasts the read allowance", http.StatusOK, code, body)
}

func TestStackRouteAnswersBadGatewayWhenGitHubRefusesTheLink(t *testing.T) {
	f, g := stackFixture(t, true, "fake-viewer", "fake-viewer")
	g.write("stack_link.fail", "GitHub refused to link the pull requests\n")
	code, body := f.stack("gha", "ok", widgets+"41", widgets+"42")
	wantCode(t, "POST stack when gh stack link fails", http.StatusBadGateway, code, body)
	if !strings.Contains(body, "GitHub refused to link") {
		t.Errorf("answer = %s, want GitHub's message", body)
	}
}

func TestStackRouteOfASourceThatIsReadOnlyIsAConflictAndRunsNoGh(t *testing.T) {
	f, g := stackFixture(t, true, "fake-viewer", "fake-viewer")
	rn := f.Svc.Sup.runner("gha")
	rn.mu.Lock()
	rn.readOnly = true
	rn.mu.Unlock()
	code, body := f.stack("gha", "ok", widgets+"41", widgets+"42")
	wantCode(t, "POST stack on a read-only source", http.StatusConflict, code, body)
	if links := g.stackLinks(); len(links) != 0 {
		t.Errorf("gh stack link ran: %v", links)
	}
}

func TestStackRouteAnswersGatewayTimeoutWhenGhRunsPastItsLimit(t *testing.T) {
	old := github.MergeTimeout
	github.MergeTimeout = 100 * time.Millisecond
	oldGrace := github.KillGrace
	github.KillGrace = 20 * time.Millisecond
	t.Cleanup(func() { github.MergeTimeout, github.KillGrace = old, oldGrace })
	f, g := stackFixture(t, true, "fake-viewer", "fake-viewer")
	g.write("stack_link.sleep", "1\n")
	code, body := f.stack("gha", "ok", widgets+"41", widgets+"42")
	wantCode(t, "POST stack when gh runs past its limit", http.StatusGatewayTimeout, code, body)
}
