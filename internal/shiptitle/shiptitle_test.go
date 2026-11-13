package shiptitle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/shipconv"
	"github.com/jbuffin/admiral/internal/ships"
)

type world struct {
	mu       sync.Mutex
	ships    []ships.Ship
	seeds    map[string]shipconv.Seed
	written  map[string]map[string]string
	items    map[string]string // GitHub item titles by link
	reply    map[string]any
	judgeErr error
	asked    []string
	release  chan struct{} // when set, the titler blocks until it closes
}

func newWorld(sh ...ships.Ship) *world {
	return &world{ships: sh, seeds: map[string]shipconv.Seed{}, written: map[string]map[string]string{}, items: map[string]string{}}
}

func (w *world) service() *Service {
	return &Service{
		List: func() []ships.Ship { return w.ships },
		Seed: func(session string) (shipconv.Seed, bool) {
			w.mu.Lock()
			defer w.mu.Unlock()
			s, ok := w.seeds[session]
			return s, ok
		},
		Set: func(_ context.Context, session string, opts map[string]string) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.written[session] = opts
			return nil
		},
		ItemTitle: func(_ context.Context, _, link string) (string, bool) {
			t, ok := w.items[link]
			return t, ok
		},
		Judge: func(_ context.Context, _ string, call judge.Call) (judge.Reply, error) {
			w.mu.Lock()
			w.asked = append(w.asked, call.Prompt)
			release := w.release
			w.mu.Unlock()
			if release != nil {
				<-release
			}
			if w.judgeErr != nil {
				return judge.Reply{}, w.judgeErr
			}
			return judge.Reply{Fields: w.reply}, nil
		},
		Wait: 50 * time.Millisecond,
	}
}

func (w *world) run(t *testing.T, s *Service) {
	t.Helper()
	var wg sync.WaitGroup
	s.Poll(context.Background(), &wg)
	wg.Wait()
}

func ship(session, harness string) ships.Ship {
	return ships.Ship{Session: session, Name: session, Fleet: "work", Harness: harness, Status: ships.StatusUnderway, Launched: "t0"}
}

func TestAGitHubLinkInThePromptGivesItsTitle(t *testing.T) {
	w := newWorld(ship("s1", "claude"))
	w.seeds["s1"] = shipconv.Seed{FirstPrompt: "Review https://github.com/o/r/pull/7 and leave comments.", Title: "Claude's name"}
	w.items["https://github.com/o/r/pull/7"] = "Fix the thing"
	w.run(t, w.service())
	want := map[string]string{"title": "Fix the thing", "link": "https://github.com/o/r/pull/7", "titled": "1"}
	if got := w.written["s1"]; !reflect.DeepEqual(got, want) {
		t.Errorf("written %v, want %v", got, want)
	}
	if len(w.asked) != 0 {
		t.Errorf("the titler ran %d times for one GitHub link", len(w.asked))
	}
}

func TestAClaudeShipWithoutAGitHubItemTakesClaudesTitle(t *testing.T) {
	w := newWorld(ship("s1", "claude"))
	w.seeds["s1"] = shipconv.Seed{FirstPrompt: "Read https://docs.example.com/guide and tidy the setup page", Title: "Tidy the setup page"}
	w.run(t, w.service())
	want := map[string]string{"title": "Tidy the setup page", "link": "https://docs.example.com/guide", "titled": "1"}
	if got := w.written["s1"]; !reflect.DeepEqual(got, want) {
		t.Errorf("written %v, want %v", got, want)
	}
}

func TestOtherHarnessesAskTheTitler(t *testing.T) {
	w := newWorld(ship("s1", "codex"))
	w.seeds["s1"] = shipconv.Seed{FirstPrompt: "the login test flakes on CI, find out why and fix it"}
	w.reply = map[string]any{"title": "Fix flaky login test", "link": float64(0)}
	w.run(t, w.service())
	want := map[string]string{"title": "Fix flaky login test", "titled": "1"}
	if got := w.written["s1"]; !reflect.DeepEqual(got, want) {
		t.Errorf("written %v, want %v", got, want)
	}
}

func TestTheTitlerPicksTheMainLinkOfSeveral(t *testing.T) {
	w := newWorld(ship("s1", "codex"))
	w.seeds["s1"] = shipconv.Seed{FirstPrompt: "Following https://docs.example.com/style, address the review on https://github.com/o/r/pull/9"}
	w.items["https://github.com/o/r/pull/9"] = "Add dark mode"
	w.reply = map[string]any{"title": "Address dark mode review", "link": float64(2)}
	w.run(t, w.service())
	want := map[string]string{"title": "Add dark mode", "link": "https://github.com/o/r/pull/9", "titled": "1"}
	if got := w.written["s1"]; !reflect.DeepEqual(got, want) {
		t.Errorf("written %v, want %v", got, want)
	}
	if len(w.asked) != 1 || !strings.Contains(w.asked[0], "2. https://github.com/o/r/pull/9") {
		t.Errorf("titler prompts %q, want one listing the links by number", w.asked)
	}
}

func TestATitlerLinkOutsideTheListIsIgnored(t *testing.T) {
	w := newWorld(ship("s1", "codex"))
	w.seeds["s1"] = shipconv.Seed{FirstPrompt: "compare https://a.example.com/x with https://b.example.com/y"}
	w.reply = map[string]any{"title": "Compare two pages", "link": float64(5)}
	w.run(t, w.service())
	want := map[string]string{"title": "Compare two pages", "titled": "1"}
	if got := w.written["s1"]; !reflect.DeepEqual(got, want) {
		t.Errorf("written %v, want %v", got, want)
	}
}

func TestAFailedTitlerLeavesTheFirstLineWithoutItsLinks(t *testing.T) {
	w := newWorld(ship("s1", "codex"))
	w.seeds["s1"] = shipconv.Seed{FirstPrompt: "\n  Look at https://a.example.com/x for me\nmore detail"}
	w.judgeErr = errors.New("no judge harness")
	w.run(t, w.service())
	want := map[string]string{"title": "Look at for me", "link": "https://a.example.com/x", "titled": "1"}
	if got := w.written["s1"]; !reflect.DeepEqual(got, want) {
		t.Errorf("written %v, want %v", got, want)
	}
}

func TestAnIssueLaunchKeepsItsURLAndTakesTheIssueTitle(t *testing.T) {
	sh := ship("s1", "codex")
	sh.URL = "https://github.com/o/r/issues/3"
	w := newWorld(sh)
	w.seeds["s1"] = shipconv.Seed{FirstPrompt: "/tdd see https://example.com/notes"}
	w.items["https://github.com/o/r/issues/3"] = "Crash on start"
	w.run(t, w.service())
	want := map[string]string{"title": "Crash on start", "titled": "1"}
	if got := w.written["s1"]; !reflect.DeepEqual(got, want) {
		t.Errorf("written %v, want %v", got, want)
	}
}

func TestSettledAndOrderShipsAndBlankShipsAreLeftAlone(t *testing.T) {
	titled, order, blank := ship("titled", "claude"), ship("order", "claude"), ship("blank", "claude")
	titled.Titled, order.Source = true, "github"
	w := newWorld(titled, order, blank)
	for _, s := range []string{"titled", "order"} {
		w.seeds[s] = shipconv.Seed{FirstPrompt: "do the thing", Title: "Do the thing"}
	}
	w.seeds["blank"] = shipconv.Seed{}
	w.run(t, w.service())
	if len(w.written) != 0 {
		t.Errorf("written %v, want nothing", w.written)
	}
}

func TestTitlingShowsWhileTheTitleIsWorkedOut(t *testing.T) {
	w := newWorld(ship("s1", "codex"))
	w.seeds["s1"] = shipconv.Seed{FirstPrompt: "tidy up"}
	w.reply = map[string]any{"title": "Tidy up", "link": float64(0)}
	w.release = make(chan struct{})
	s := w.service()
	if s.Titling("s1") {
		t.Fatal("titling before the first poll")
	}
	var wg sync.WaitGroup
	s.Poll(context.Background(), &wg)
	if !s.Titling("s1") {
		t.Error("not titling while the titler runs")
	}
	close(w.release)
	wg.Wait()
	if s.Titling("s1") {
		t.Error("still titling once the title is written")
	}
	s.Poll(context.Background(), &wg)
	wg.Wait()
	if len(w.asked) != 1 {
		t.Errorf("the titler ran %d times; a ship is titled once", len(w.asked))
	}
}

func TestLinks(t *testing.T) {
	got := Links("See (https://a.example.com/x), then https://b.example.com/y. Again https://a.example.com/x and http://localhost:8080/app")
	want := []string{"https://a.example.com/x", "https://b.example.com/y", "http://localhost:8080/app"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Links = %q, want %q", got, want)
	}
}
