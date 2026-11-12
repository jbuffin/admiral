package ships

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/usage"
)

type usageRig struct {
	s   *Service
	dir string
}

func newUsageRig(t *testing.T) *usageRig {
	t.Helper()
	home := t.TempDir()
	r := &usageRig{dir: filepath.Join(home, "outcomes")}
	w := outcomes.New(r.dir, t.Logf)
	t.Cleanup(w.Close)
	r.s = &Service{
		Host:     sessionhost.New("tmux", filepath.Join(home, "tmux.sock")),
		Outcomes: w,
		Launcher: &launcher.Launcher{Home: home, Config: func() *config.Config { return &config.Config{} }},
	}
	return r
}

func (r *usageRig) lines(t *testing.T) []map[string]any {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(r.dir, "*.jsonl"))
	var out []map[string]any
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, l := range strings.Split(string(b), "\n") {
			if l == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Fatal(err)
			}
			out = append(out, m)
		}
	}
	return out
}

func count(recs []map[string]any, event string) int {
	n := 0
	for _, m := range recs {
		if m["event"] == event {
			n++
		}
	}
	return n
}

func (r *usageRig) waitCount(t *testing.T, event string, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		recs := r.lines(t)
		if count(recs, event) >= n {
			return recs
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 5 s the log has %d %s records, want %d: %v", count(recs, event), event, n, recs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (r *usageRig) usageRecord(t *testing.T, recs []map[string]any) map[string]any {
	t.Helper()
	for _, m := range recs {
		if m["event"] == "usage" {
			return m
		}
	}
	t.Fatal("no usage record")
	return nil
}

func exitedShip(name string, opts map[string]string) sessionhost.Session {
	o := map[string]string{"name": name, "fleet": "work", "harness": "claude", "launched": "2026-11-11T10:00:00Z"}
	for k, v := range opts {
		o[k] = v
	}
	return sessionhost.Session{Name: name, Options: o, PaneDead: true, ExitStatus: 0, DeadTime: time.Date(2026, 11, 11, 10, 5, 0, 0, time.UTC)}
}

func ending1(sess sessionhost.Session) ending {
	return ending{sess: sess, end: &endState{}}
}

func figs() *usage.Figures {
	return &usage.Figures{Tokens: usage.Tokens{Input: 100, Output: 40, Reasoning: 10, CacheRead: 300, CacheWrite: 40},
		Money: &usage.Money{Amount: 0.25, Basis: usage.BasisEstimate}}
}

func TestExitWritesOneUsageRecordBesideEnded(t *testing.T) {
	r := newUsageRig(t)
	r.s.UsageRead = func(context.Context, sessionhost.Session) ExitUsage { return ExitUsage{Own: figs()} }
	r.s.queueEnded(ending1(exitedShip("a-1", nil)))
	recs := r.waitCount(t, "usage", 1)
	r.waitCount(t, "ended", 1)

	u := r.usageRecord(t, recs)
	if u["ship"] != "a-1" || u["fleet"] != "work" || u["harness"] != "claude" {
		t.Errorf("usage = %v, want ship a-1, fleet work, harness claude", u)
	}
	want := map[string]any{"input": 100.0, "output": 40.0, "reasoning": 10.0, "cache_read": 300.0, "cache_write": 40.0}
	if got, _ := u["tokens"].(map[string]any); len(got) != len(want) || got["input"] != want["input"] || got["output"] != want["output"] ||
		got["reasoning"] != want["reasoning"] || got["cache_read"] != want["cache_read"] || got["cache_write"] != want["cache_write"] {
		t.Errorf("tokens = %v, want %v", u["tokens"], want)
	}
	if m, _ := u["money"].(map[string]any); m["amount"] != 0.25 || m["basis"] != "estimate" || m["incomplete"] != false {
		t.Errorf("money = %v, want 0.25 estimate, not incomplete", u["money"])
	}
	if _, has := u["round"]; has {
		t.Errorf("usage = %v, a ship that was never woken has no round", u)
	}
}

func TestMooredRoundCarriesItsRoundNumber(t *testing.T) {
	r := newUsageRig(t)
	r.s.UsageRead = func(context.Context, sessionhost.Session) ExitUsage { return ExitUsage{Own: figs()} }
	r.s.queueEnded(ending1(exitedShip("a-1", map[string]string{"round": "2"})))
	if got := r.usageRecord(t, r.waitCount(t, "usage", 1))["round"]; got != 2.0 {
		t.Errorf("round = %v, want 2", got)
	}
}

// A retry of `ended` (a failed write, or a kill that queues it again) is the same exit.
func TestRetryOfEndedWritesNoSecondUsage(t *testing.T) {
	r := newUsageRig(t)
	reads := make(chan struct{}, 8)
	r.s.UsageRead = func(context.Context, sessionhost.Session) ExitUsage {
		reads <- struct{}{}
		return ExitUsage{Own: figs()}
	}
	e := ending1(exitedShip("a-1", nil))
	r.s.queueEnded(e)
	r.s.queueEnded(e)
	r.waitCount(t, "ended", 2)
	r.waitCount(t, "usage", 1)
	time.Sleep(300 * time.Millisecond)
	if n := count(r.lines(t), "usage"); n != 1 {
		t.Errorf("%d usage records after a retried ended, want 1", n)
	}
}

// Another exit of the same name (a new round, a relaunch) is a new exit with its own record.
func TestNextExitOfTheSameNameWritesItsOwnUsage(t *testing.T) {
	r := newUsageRig(t)
	r.s.UsageRead = func(context.Context, sessionhost.Session) ExitUsage { return ExitUsage{Own: figs()} }
	r.s.queueEnded(ending1(exitedShip("a-1", map[string]string{"round": "1"})))
	r.s.queueEnded(ending1(exitedShip("a-1", map[string]string{"round": "2", "launched": "2026-11-11T11:00:00Z"})))
	r.waitCount(t, "usage", 2)
}

// A transcript read that hangs must not hold `ended` back, and ended is not lost when it ends.
func TestSlowTranscriptReadDoesNotDelayEnded(t *testing.T) {
	r := newUsageRig(t)
	release := make(chan struct{})
	r.s.UsageRead = func(context.Context, sessionhost.Session) ExitUsage { <-release; return ExitUsage{Own: figs()} }
	done := make(chan struct{})
	go func() { r.s.queueEnded(ending1(exitedShip("a-1", nil))); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("queueEnded waited on the transcript read")
	}
	r.waitCount(t, "ended", 1)
	if n := count(r.lines(t), "usage"); n != 0 {
		t.Errorf("usage written before its read returned: %d", n)
	}
	close(release)
	r.waitCount(t, "usage", 1)
}

func TestNoFiguresWritesNullsSoTheExitIsCounted(t *testing.T) {
	r := newUsageRig(t)
	r.s.UsageRead = func(context.Context, sessionhost.Session) ExitUsage { return ExitUsage{} }
	r.s.queueEnded(ending1(exitedShip("a-1", nil)))
	u := r.usageRecord(t, r.waitCount(t, "usage", 1))
	if v, has := u["tokens"]; !has || v != nil {
		t.Errorf("tokens = %v, want null", v)
	}
	if v, has := u["money"]; !has || v != nil {
		t.Errorf("money = %v, want null", v)
	}
	if u["fleet"] != "work" || u["harness"] != "claude" {
		t.Errorf("usage = %v, want fleet and harness even without figures", u)
	}
}

func TestNoFiguresAsksTheLastLiveFigures(t *testing.T) {
	r := newUsageRig(t)
	r.s.UsageRead = func(context.Context, sessionhost.Session) ExitUsage { return ExitUsage{} }
	var asked string
	r.s.LastLive = func(session string) (usage.Figures, bool) {
		asked = session
		return usage.Figures{Tokens: usage.Tokens{Input: 7}, Money: &usage.Money{Amount: 0.5, Basis: usage.BasisEstimate}}, true
	}
	r.s.queueEnded(ending1(exitedShip("a-1", nil)))
	u := r.usageRecord(t, r.waitCount(t, "usage", 1))
	if asked != "a-1" {
		t.Errorf("LastLive asked for %q, want a-1", asked)
	}
	if tk, _ := u["tokens"].(map[string]any); tk["input"] != 7.0 {
		t.Errorf("tokens = %v, want the live figures", u["tokens"])
	}
	if m, _ := u["money"].(map[string]any); m["amount"] != 0.5 {
		t.Errorf("money = %v, want the live figures", u["money"])
	}
}

func TestLastLiveIsNotAskedWhenTheTranscriptHasFigures(t *testing.T) {
	r := newUsageRig(t)
	r.s.UsageRead = func(context.Context, sessionhost.Session) ExitUsage { return ExitUsage{Own: figs()} }
	r.s.LastLive = func(string) (usage.Figures, bool) {
		t.Error("LastLive asked although the transcript had figures")
		return usage.Figures{}, false
	}
	r.s.queueEnded(ending1(exitedShip("a-1", nil)))
	r.waitCount(t, "usage", 1)
}

func TestLastLiveWithNothingStillWritesNulls(t *testing.T) {
	r := newUsageRig(t)
	r.s.UsageRead = func(context.Context, sessionhost.Session) ExitUsage { return ExitUsage{} }
	r.s.LastLive = func(string) (usage.Figures, bool) { return usage.Figures{}, false }
	r.s.queueEnded(ending1(exitedShip("a-1", nil)))
	if u := r.usageRecord(t, r.waitCount(t, "usage", 1)); u["tokens"] != nil || u["money"] != nil {
		t.Errorf("usage = %v, want null tokens and money", u)
	}
}

func TestCrewBoatsAreAddedAndListed(t *testing.T) {
	r := newUsageRig(t)
	boat := usage.Figures{Tokens: usage.Tokens{Input: 20}, Money: &usage.Money{Amount: 0.25, Basis: usage.BasisEstimate}}
	r.s.UsageRead = func(context.Context, sessionhost.Session) ExitUsage {
		return ExitUsage{Own: figs(), Crew: []usage.Boat{{Name: "scout", Harness: "claude", Figures: &boat}, {Name: "late", Harness: "claude"}}}
	}
	r.s.queueEnded(ending1(exitedShip("a-1", nil)))
	u := r.usageRecord(t, r.waitCount(t, "usage", 1))
	if tk, _ := u["tokens"].(map[string]any); tk["input"] != 120.0 {
		t.Errorf("tokens = %v, want the crew's input added (120)", u["tokens"])
	}
	if m, _ := u["money"].(map[string]any); m["amount"] != 0.5 {
		t.Errorf("money = %v, want the crew's 0.25 added (0.5)", u["money"])
	}
	crew, _ := u["crew"].([]any)
	if len(crew) != 2 {
		t.Fatalf("crew = %v, want scout and late", u["crew"])
	}
	late := crew[1].(map[string]any)
	if late["name"] != "late" || late["tokens"] != nil || late["money"] != nil {
		t.Errorf("late = %v, want listed with null figures", late)
	}
}
