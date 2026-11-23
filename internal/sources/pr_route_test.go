package sources

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// reviewedDetail is GitHub's answer for one PR with a decision, reviews, a requested reviewer and a conflict.
const reviewedDetail = `{"number":41,"url":"https://github.com/acme/widgets/pull/41","title":"PR 41","state":"OPEN","isDraft":true,"headRefName":"b41","baseRefName":"main","reviewDecision":"CHANGES_REQUESTED","mergeable":"CONFLICTING","author":{"login":"fake-viewer"},"reviews":{"nodes":[{"author":{"__typename":"User","login":"alice-approver"},"state":"APPROVED"},{"author":{"__typename":"User","login":"bob-blocker"},"state":"CHANGES_REQUESTED"}]},"reviewRequests":{"nodes":[{"requestedReviewer":{"__typename":"User","login":"dave-waiting"}}]},"baseRepository":{"defaultBranchRef":{"name":"main"}},"stack":null}`

func (f *serviceFixture) prDetailRead(key, prURL string) (int, string) {
	f.t.Helper()
	return f.do(http.MethodGet, "/"+key+"/pr?url="+url.QueryEscape(prURL), nil)
}

func TestPRRouteAnswersThePRsReviewsAndRequestedReviewers(t *testing.T) {
	f, g := stackFixture(t, true, "fake-viewer")
	g.write("pr_detail/https___github.com_acme_widgets_pull_41", reviewedDetail+"\n")
	code, body := f.prDetailRead("gha", widgets+"41")
	wantCode(t, "GET pr", http.StatusOK, code, body)
	var got struct {
		Review    string `json:"review"`
		Mergeable string `json:"mergeable"`
		Draft     bool   `json:"draft"`
		Reviews   []struct {
			Author struct{ Login string } `json:"author"`
			State  string                 `json:"state"`
		} `json:"reviews"`
		Requested []struct{ Login string } `json:"requested"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("answer is not JSON: %s", body)
	}
	if got.Review != "CHANGES_REQUESTED" || got.Mergeable != "CONFLICTING" || !got.Draft {
		t.Errorf("decision, mergeable, draft = %q, %q, %v, want CHANGES_REQUESTED, CONFLICTING, true", got.Review, got.Mergeable, got.Draft)
	}
	if len(got.Reviews) != 2 || got.Reviews[0].Author.Login != "alice-approver" || got.Reviews[0].State != "APPROVED" || got.Reviews[1].Author.Login != "bob-blocker" || got.Reviews[1].State != "CHANGES_REQUESTED" {
		t.Errorf("reviews = %+v, want alice-approver APPROVED then bob-blocker CHANGES_REQUESTED", got.Reviews)
	}
	if len(got.Requested) != 1 || got.Requested[0].Login != "dave-waiting" {
		t.Errorf("requested = %+v, want dave-waiting", got.Requested)
	}
}

func TestPRRouteRefusesAPRNotOnTheSourcesHostBeforeReadingGitHub(t *testing.T) {
	f, g := stackFixture(t, true, "fake-viewer")
	code, body := f.prDetailRead("gha", "https://ghe.example.com/acme/widgets/pull/41")
	wantCode(t, "GET pr off the host", http.StatusUnprocessableEntity, code, body)
	if n := g.detailReads(); n != 0 {
		t.Errorf("gh read %d pull requests for a URL off the host", n)
	}
}

func TestPRRouteOfASourceThatIsNotThereIsNotFound(t *testing.T) {
	f, _ := stackFixture(t, true, "fake-viewer")
	code, body := f.prDetailRead("nope", widgets+"41")
	wantCode(t, "GET pr on an unknown source", http.StatusNotFound, code, body)
	if !strings.Contains(body, "source nope not found") {
		t.Errorf("answer = %s, want the unknown-source message", body)
	}
}

func TestPRRouteOfASourceThatIsReadOnlyIsAConflict(t *testing.T) {
	f, _ := stackFixture(t, true, "fake-viewer")
	rn := f.Svc.Sup.runner("gha")
	rn.mu.Lock()
	rn.readOnly = true
	rn.mu.Unlock()
	code, body := f.prDetailRead("gha", widgets+"41")
	wantCode(t, "GET pr on a read-only source", http.StatusConflict, code, body)
}

func TestPRRouteAnswersBadGatewayWhenGitHubHasNoSuchPR(t *testing.T) {
	f, _ := stackFixture(t, true, "fake-viewer")
	code, body := f.prDetailRead("gha", widgets+"99")
	wantCode(t, "GET pr GitHub does not have", http.StatusBadGateway, code, body)
}
