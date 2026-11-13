package moor

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type fakeGitHub struct {
	host string
	mu   sync.Mutex
	open map[string]bool // PR URL → GitHub confirms it open; a URL left out is one GitHub did not answer for
	fail int             // the first calls that fail
	n    int
}

func (f *fakeGitHub) Host() string { return f.host }

func (f *fakeGitHub) PullRequests(context.Context, []string) (map[string]Status, error) {
	return nil, nil
}

func (f *fakeGitHub) PullRequestDetail(context.Context, string) (Detail, error) { return Detail{}, nil }

func (f *fakeGitHub) DefaultMergeMethod(context.Context, string) (string, error) { return "merge", nil }

func (f *fakeGitHub) MergePR(context.Context, string, string) error { return nil }

func (f *fakeGitHub) StackPRs(context.Context, string, string) ([]StackLayer, error) { return nil, nil }

func (f *fakeGitHub) MergeStack(context.Context, string, string, string) error { return nil }

func (f *fakeGitHub) OpenPRs(_ context.Context, urls []string) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.fail < 0 || f.n <= f.fail {
		return nil, errors.New("gh: HTTP 502")
	}
	out := map[string]bool{}
	for _, u := range urls {
		if v, ok := f.open[u]; ok {
			out[u] = v
		}
	}
	return out, nil
}

func (f *fakeGitHub) calls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.n }

const (
	mine    = "https://github.com/acme/widgets/pull/7"
	foreign = "https://github.com/globex/app/pull/9"
)

// route sends acme's repos to work and everything else to home.
func route(host, nameWithOwner string) string {
	if nameWithOwner == "acme/widgets" {
		return "work"
	}
	return "home"
}

func TestCountedCountsWhatGitHubConfirmsOpenInTheShipsFleet(t *testing.T) {
	const ghe = "https://ghe.example.com/acme/widgets/pull/7"
	for name, c := range map[string]struct {
		gh   *fakeGitHub
		prs  []string
		want []string
	}{
		"open and in the fleet": {&fakeGitHub{host: "github.com", open: map[string]bool{mine: true}}, []string{mine}, []string{mine}},
		"closed or merged":      {&fakeGitHub{host: "github.com", open: map[string]bool{mine: false}}, []string{mine}, nil},
		"GitHub cannot confirm": {&fakeGitHub{host: "github.com", open: map[string]bool{}}, []string{mine}, nil},
		"routes to another fleet": {&fakeGitHub{host: "github.com", open: map[string]bool{foreign: true}},
			[]string{foreign}, nil},
		"host is not the client's": {&fakeGitHub{host: "github.com", open: map[string]bool{ghe: true}}, []string{ghe}, nil},
		"only the open one of two": {&fakeGitHub{host: "github.com", open: map[string]bool{mine: true, foreign: true}},
			[]string{foreign, mine}, []string{mine}},
		"no GitHub source in the fleet": {nil, []string{mine}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			check := Check{Fleet: "work", Route: route}
			if c.gh != nil {
				check.GitHub = c.gh
			}
			got := Counted(context.Background(), check, c.prs)
			if !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
				t.Errorf("counted %v, want %v", got, c.want)
			}
		})
	}
}

func TestCountedRetriesAFailingGhWithinABound(t *testing.T) {
	old := RetryWindow
	t.Cleanup(func() { RetryWindow = old })
	RetryWindow = 2 * time.Second

	flaky := &fakeGitHub{host: "github.com", open: map[string]bool{mine: true}, fail: 2}
	if got := Counted(context.Background(), Check{Fleet: "work", GitHub: flaky, Route: route}, []string{mine}); !reflect.DeepEqual(got, []string{mine}) {
		t.Errorf("after two failures and an answer, counted %v, want the PR", got)
	}

	RetryWindow = 300 * time.Millisecond
	down := &fakeGitHub{host: "github.com", open: map[string]bool{mine: true}, fail: -1}
	start := time.Now()
	got := Counted(context.Background(), Check{Fleet: "work", GitHub: down, Route: route}, []string{mine})
	if len(got) != 0 {
		t.Errorf("with gh failing throughout, counted %v, want none", got)
	}
	if down.calls() < 2 {
		t.Errorf("gh was asked %d time(s), want a retry", down.calls())
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("giving up took %v, want it bounded by the retry window", took)
	}
}
