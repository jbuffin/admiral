package uselive

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/usage"
)

var epoch = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

type rig struct {
	now   time.Time
	store *usage.Store
}

func newRig() *rig {
	r := &rig{now: epoch.Add(time.Minute)}
	r.store = usage.NewStore(func() time.Time { return r.now })
	return r
}

func (r *rig) clock() time.Time { return r.now }

func loginOf(ship string) string {
	if ship == "" {
		return ""
	}
	return "/home/u/.claude"
}

func keptLine(session, time string, cost string, windows string) string {
	rl := ""
	if windows != "" {
		rl = `,"rate_limits":{` + windows + `}`
	}
	return `{"time":"` + time + `","session_id":"` + session + `","cost":{"total_cost_usd":` + cost + `},"context_window":{"total_input_tokens":100,"total_output_tokens":10}` + rl + `}`
}

const five = `"five_hour":{"used_percentage":23.5,"resets_at":1900000000}`

func claudeFeed(r *rig, panes func() ([]Pane, error)) *Claude {
	return &Claude{Store: r.store, Login: loginOf, Now: r.clock, Panes: func(context.Context) ([]Pane, error) { return panes() }}
}

func TestClaudePollFillsShipFiguresAndTheLoginsQuota(t *testing.T) {
	r := newRig()
	f := claudeFeed(r, func() ([]Pane, error) {
		return []Pane{{Ship: "a-1", Option: keptLine("s1", "2026-11-12T10:00:00Z", "1.5", five)}}, nil
	})
	f.Poll(context.Background())
	fig, age, ok := r.store.Figures("a-1")
	if !ok || fig.Money.Amount != 1.5 || fig.Tokens.Input != 100 || age != time.Minute {
		t.Errorf("figures=%+v age=%v ok=%v, want $1.5, 100 in, a minute old (the line's own time)", fig, age, ok)
	}
	q, qage, ok := r.store.Quota("/home/u/.claude")
	if !ok || q.Primary == nil || q.Primary.UsedPercent != 23.5 || qage != time.Minute {
		t.Errorf("quota=%+v age=%v ok=%v, want the five-hour window under the login", q, qage, ok)
	}
}

func TestClaudePollTakesTheChatPaneToo(t *testing.T) {
	r := newRig()
	f := claudeFeed(r, func() ([]Pane, error) {
		return []Pane{{Ship: sessionhost.ChatName, Option: keptLine("c1", "2026-11-12T10:00:00Z", "0.7", "")}}, nil
	})
	f.Poll(context.Background())
	if fig, _, ok := r.store.Figures(sessionhost.ChatName); !ok || fig.Money.Amount != 0.7 {
		t.Errorf("chat figures = %+v ok=%v", fig, ok)
	}
}

// A new session's first status line has no rate_limits yet; it must not wipe what the login last showed.
func TestClaudePollWithoutRateLimitsKeepsTheLoginsQuota(t *testing.T) {
	r := newRig()
	opt := keptLine("s1", "2026-11-12T10:00:00Z", "1.5", five)
	f := claudeFeed(r, func() ([]Pane, error) { return []Pane{{Ship: "a-1", Option: opt}}, nil })
	f.Poll(context.Background())
	opt = keptLine("s2", "2026-11-12T10:00:30Z", "0", "")
	f.Poll(context.Background())
	if q, _, ok := r.store.Quota("/home/u/.claude"); !ok || q.Primary == nil || q.Primary.UsedPercent != 23.5 {
		t.Errorf("quota = %+v ok=%v, want the earlier reading kept", q, ok)
	}
	if fig, _, _ := r.store.Figures("a-1"); fig.Money.Amount != 0 {
		t.Errorf("figures = %+v, want the new session's", fig)
	}
	if last, ok := r.store.LastLive("a-1"); !ok || last.Money.Amount != 1.5 {
		t.Errorf("last live = %+v ok=%v, want the first session's cost", last, ok)
	}
}

func TestClaudePollIgnoresAPaneWithNoOrGarbageOption(t *testing.T) {
	r := newRig()
	f := claudeFeed(r, func() ([]Pane, error) {
		return []Pane{{Ship: "a-1", Option: ""}, {Ship: "a-2", Option: "not json"}}, nil
	})
	f.Poll(context.Background())
	for _, ship := range []string{"a-1", "a-2"} {
		if _, _, ok := r.store.Figures(ship); ok {
			t.Errorf("%s has figures from nothing", ship)
		}
	}
}

func TestClaudePollWhenTheReadFailsKeepsWhatItHad(t *testing.T) {
	r := newRig()
	fail := false
	f := claudeFeed(r, func() ([]Pane, error) {
		if fail {
			return nil, errors.New("tmux gone")
		}
		return []Pane{{Ship: "a-1", Option: keptLine("s1", "2026-11-12T10:00:00Z", "1.5", five)}}, nil
	})
	f.Poll(context.Background())
	fail = true
	f.Poll(context.Background())
	if _, _, ok := r.store.Figures("a-1"); !ok {
		t.Error("figures dropped by a failed read")
	}
	if _, _, ok := r.store.Quota("/home/u/.claude"); !ok {
		t.Error("quota dropped by a failed read")
	}
}

func TestClaudePollWithNoKnownLoginKeepsFiguresButNoQuota(t *testing.T) {
	r := newRig()
	f := claudeFeed(r, func() ([]Pane, error) {
		return []Pane{{Ship: "", Option: keptLine("s1", "2026-11-12T10:00:00Z", "1.5", five)}}, nil
	})
	f.Poll(context.Background())
	if _, _, ok := r.store.Quota(""); ok {
		t.Error("a quota stored under no login")
	}
}

// Codex

const tokenFrame = `{"method":"thread/tokenUsage/updated","params":{"threadId":%q,"turnId":"t","tokenUsage":{"total":{"totalTokens":%d,"inputTokens":%d,"cachedInputTokens":0,"outputTokens":%d,"reasoningOutputTokens":0},"modelContextWindow":258400}}}`

func tokenUsageFrame(thread string, in, out int) []byte {
	return []byte(fmt.Sprintf(tokenFrame, thread, in+out, in, out))
}

func codexFeed(r *rig) *Codex {
	return &Codex{Store: r.store, Login: func(ship string) string { return "/home/u/.codex" }, Now: r.clock}
}

func TestCodexTokenUsageFrameFillsTheShipsFigures(t *testing.T) {
	r := newRig()
	codexFeed(r).Frame("c-1", tokenUsageFrame("th1", 1000, 50))
	fig, age, ok := r.store.Figures("c-1")
	if !ok || fig.Tokens.Input != 1000 || fig.Tokens.Output != 50 || age != 0 {
		t.Errorf("figures=%+v age=%v ok=%v", fig, age, ok)
	}
	if fig.Money != nil {
		t.Errorf("money = %+v, want none from Codex", fig.Money)
	}
}

// A thread's totals are cumulative: the next frame of the same thread replaces them.
func TestCodexSameThreadFramesReplaceAndNewThreadsAdd(t *testing.T) {
	r := newRig()
	c := codexFeed(r)
	c.Frame("c-1", tokenUsageFrame("th1", 1000, 50))
	c.Frame("c-1", tokenUsageFrame("th1", 1500, 80))
	if fig, _, _ := r.store.Figures("c-1"); fig.Tokens.Input != 1500 || fig.Tokens.Output != 80 {
		t.Errorf("tokens = %+v, want the thread's latest totals, not a sum of its frames", fig.Tokens)
	}
	c.Frame("c-1", tokenUsageFrame("th2", 200, 20))
	if fig, _, _ := r.store.Figures("c-1"); fig.Tokens.Input != 1700 || fig.Tokens.Output != 100 {
		t.Errorf("tokens = %+v, want both threads summed (/new starts a thread over)", fig.Tokens)
	}
	c.Frame("c-2", tokenUsageFrame("th9", 7, 1))
	if fig, _, _ := r.store.Figures("c-1"); fig.Tokens.Input != 1700 {
		t.Errorf("another ship's frame changed this ship's figures: %+v", fig.Tokens)
	}
}

func TestCodexRateLimitsFrameFillsTheLoginsQuota(t *testing.T) {
	r := newRig()
	frame, err := os.ReadFile("../usage/testdata/codex-ratelimits-unverified.json")
	if err != nil {
		t.Fatal(err)
	}
	codexFeed(r).Frame("c-1", frame)
	q, _, ok := r.store.Quota("/home/u/.codex")
	if !ok || q.Primary == nil || q.Primary.UsedPercent != 42 || q.Secondary == nil || q.Secondary.UsedPercent != 8 {
		t.Errorf("quota = %+v ok=%v", q, ok)
	}
	if _, _, ok := r.store.Figures("c-1"); ok {
		t.Error("a rate-limits frame made ship figures")
	}
}

func TestCodexOtherAndBrokenFramesChangeNothing(t *testing.T) {
	r := newRig()
	c := codexFeed(r)
	for _, f := range []string{`{"method":"item/started","params":{}}`, `garbage`, ``} {
		c.Frame("c-1", []byte(f))
	}
	if _, _, ok := r.store.Figures("c-1"); ok {
		t.Error("figures from a frame that holds none")
	}
	if _, _, ok := r.store.Quota("/home/u/.codex"); ok {
		t.Error("quota from a frame that holds none")
	}
}

// Copilot

const sessionUUID = "7a1c0de0-2941-4294-8000-000000000001"

func usageRecordLine(t *testing.T) string {
	t.Helper()
	f, err := os.Open("../usage/testdata/copilot-recorded.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if strings.Contains(sc.Text(), `"session.usage_record"`) {
			return sc.Text() + "\n"
		}
	}
	t.Fatal("no usage_record in the recorded sample")
	return ""
}

func copilotRig(t *testing.T, r *rig) (*Copilot, string, string) {
	t.Helper()
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	path := filepath.Join(dir, "session-state", sessionUUID, "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	c := &Copilot{Store: r.store, Now: r.clock, Files: func() []File { return []File{{Ship: "p-1", Dir: dir, Path: path}} }}
	return c, dir, path
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCopilotPollSumsTheUsageRecordsFromTheFilesStart(t *testing.T) {
	r := newRig()
	c, _, path := copilotRig(t, r)
	rec := usageRecordLine(t)
	appendFile(t, path, rec)
	c.Poll(context.Background())
	fig, _, ok := r.store.Figures("p-1")
	// the recorded line: 35736 in (35732 of it cache writes), 4 out, 17.8756 credits
	if !ok || fig.Tokens.Input != 4 || fig.Tokens.Output != 4 || fig.Tokens.CacheWrite != 35732 || fig.Money == nil || !near(fig.Money.Amount, 17.8756) || fig.Money.Basis != usage.BasisCredits {
		t.Fatalf("figures = %+v ok=%v", fig, ok)
	}
	appendFile(t, path, rec)
	c.Poll(context.Background())
	fig, _, _ = r.store.Figures("p-1")
	if fig.Tokens.CacheWrite != 71464 || !near(fig.Money.Amount, 35.7512) {
		t.Errorf("figures = %+v, want both records summed, not the second alone", fig)
	}
}

func TestCopilotPollWithNoChangeKeepsTheFigures(t *testing.T) {
	r := newRig()
	c, _, path := copilotRig(t, r)
	appendFile(t, path, usageRecordLine(t))
	c.Poll(context.Background())
	c.Poll(context.Background())
	if fig, _, _ := r.store.Figures("p-1"); fig.Tokens.CacheWrite != 35732 {
		t.Errorf("figures = %+v, want one record counted once", fig)
	}
}

func TestCopilotPollOfAMissingOrEmptyFileHasNoFigures(t *testing.T) {
	r := newRig()
	c, _, path := copilotRig(t, r)
	c.Poll(context.Background())
	appendFile(t, path, `{"type":"user.message","data":{}}`+"\n")
	c.Poll(context.Background())
	if _, _, ok := r.store.Figures("p-1"); ok {
		t.Error("figures from a file with no usage_record")
	}
}

// Copilot's account quota is not in the session file, so no login gets a quota from it.
func TestCopilotPollMakesNoQuota(t *testing.T) {
	r := newRig()
	c, dir, path := copilotRig(t, r)
	appendFile(t, path, usageRecordLine(t))
	c.Poll(context.Background())
	if _, _, ok := r.store.Quota(dir); ok {
		t.Error("a quota for the Copilot login")
	}
}

// A moored round's Codex threads start a new sum once the record has taken the last one.
func TestCodexForgetStartsTheNextRoundsSumOver(t *testing.T) {
	r := newRig()
	c := codexFeed(r)
	c.Frame("c-1", tokenUsageFrame("th1", 1000, 50))
	r.store.Forget("c-1")
	c.Forget("c-1")
	if _, ok := r.store.LastLive("c-1"); ok {
		t.Error("round 1's figures survived the forget")
	}
	r.now = r.now.Add(time.Minute)
	c.Frame("c-1", tokenUsageFrame("th2", 200, 20))
	if f, ok := r.store.LastLive("c-1"); !ok || f.Tokens.Input != 200 || f.Tokens.Output != 20 {
		t.Errorf("last live = %+v ok=%v, want round 2's thread alone", f, ok)
	}
}

func TestCodexBoundsThreadsPerShipAndShips(t *testing.T) {
	r := newRig()
	c := codexFeed(r)
	for i := 0; i < maxCodexThreads+1; i++ {
		c.Frame("c-1", tokenUsageFrame(fmt.Sprintf("th%d", i), 1, 0))
	}
	if fig, _, _ := r.store.Figures("c-1"); fig.Tokens.Input != int64(maxCodexThreads) {
		t.Errorf("input = %d, want the first %d threads only", fig.Tokens.Input, maxCodexThreads)
	}
	for i := 0; i < maxCodexShips+1; i++ {
		c.Frame(fmt.Sprintf("ship-%d", i), tokenUsageFrame("th", 1, 0))
	}
	if len(c.threads) > maxCodexShips {
		t.Errorf("%d ships tracked, want at most %d", len(c.threads), maxCodexShips)
	}
	if _, _, ok := r.store.Figures(fmt.Sprintf("ship-%d", maxCodexShips)); !ok {
		t.Error("the newest ship has no figures")
	}
}

// Claude

// A status line's future time must not pin the login's quota against later real readings.
func TestClaudePollClampsAFutureLineTimeToNow(t *testing.T) {
	r := newRig()
	opt := keptLine("s1", "9999-01-01T00:00:00Z", "1", `"five_hour":{"used_percentage":90,"resets_at":1900000000}`)
	f := claudeFeed(r, func() ([]Pane, error) { return []Pane{{Ship: "a-1", Option: opt}}, nil })
	f.Poll(context.Background())
	if _, age, _ := r.store.Quota("/home/u/.claude"); age < 0 {
		t.Errorf("age = %v, want never negative", age)
	}
	r.now = r.now.Add(time.Minute)
	opt = keptLine("s1", "2026-11-12T10:01:30Z", "2", `"five_hour":{"used_percentage":10,"resets_at":1900000000}`)
	f.Poll(context.Background())
	if q, _, _ := r.store.Quota("/home/u/.claude"); q.Primary == nil || q.Primary.UsedPercent != 10 {
		t.Errorf("quota = %+v, want the real reading to replace the one with a future time", q)
	}
}

// Two panes on one login: a dead pane keeps its old line, and being read last or again changes nothing.
func TestClaudePollKeepsTheNewerQuotaWhenAnOlderPaneIsReadLast(t *testing.T) {
	r := newRig()
	newer := keptLine("s2", "2026-11-12T10:00:50Z", "1", `"five_hour":{"used_percentage":60,"resets_at":1900000000}`)
	older := keptLine("s1", "2026-11-12T10:00:10Z", "1", `"five_hour":{"used_percentage":20,"resets_at":1900000000}`)
	f := claudeFeed(r, func() ([]Pane, error) {
		return []Pane{{Ship: "a-1", Option: newer}, {Ship: "a-2", Option: older}}, nil
	})
	f.Poll(context.Background())
	f.Poll(context.Background())
	if q, _, _ := r.store.Quota("/home/u/.claude"); q.Primary == nil || q.Primary.UsedPercent != 60 {
		t.Errorf("quota = %+v, want the newer pane's reading", q)
	}
}

// A line with no time of its own is read at poll time; that is why the fallback is the clock and not zero.
func TestClaudePollLineWithoutATimeCountsAsReadNow(t *testing.T) {
	r := newRig()
	opt := `{"session_id":"s1","cost":{"total_cost_usd":1},"rate_limits":{"five_hour":{"used_percentage":5,"resets_at":1900000000}}}`
	f := claudeFeed(r, func() ([]Pane, error) { return []Pane{{Ship: "a-1", Option: opt}}, nil })
	f.Poll(context.Background())
	if _, age, ok := r.store.Quota("/home/u/.claude"); !ok || age != 0 {
		t.Errorf("age = %v ok=%v, want 0", age, ok)
	}
}

func TestTmuxClaudePanesKeepsClaudeShipsAndTheChatOnly(t *testing.T) {
	line := keptLine("s1", "2026-11-12T10:00:00Z", "1", "")
	tm := &Tmux{Harness: func(ship string) string {
		return map[string]string{"cl-1": "claude", "cx-1": "codex", "cp-1": "copilot"}[ship]
	}}
	tm.panes = []sessionhost.LivePane{
		{Session: "cl-1", Harness: "claude", Statusline: line},
		{Session: "cx-1", Harness: "claude", Statusline: line}, // the pane claims Claude; the record says Codex
		{Session: "cp-1", Statusline: line},
		{Session: "unknown", Harness: "claude", Statusline: line},
		{Session: sessionhost.ChatName, Statusline: line},
		{Session: "cl-1", Harness: "claude"}, // no status line
	}
	got, err := tm.ClaudePanes(context.Background())
	if err != nil || len(got) != 2 || got[0].Ship != "cl-1" || got[1].Ship != sessionhost.ChatName {
		t.Errorf("panes = %+v err=%v, want cl-1 and the chat", got, err)
	}
}

func TestTmuxClaudePanesSkipsAnOversizeStatusLine(t *testing.T) {
	tm := &Tmux{Harness: func(string) string { return "claude" }}
	tm.panes = []sessionhost.LivePane{
		{Session: "big", Statusline: strings.Repeat("x", sessionhost.MaxStatuslineRead+1)},
		{Session: "ok", Statusline: strings.Repeat("x", sessionhost.MaxStatuslineRead)},
	}
	got, _ := tm.ClaudePanes(context.Background())
	if len(got) != 1 || got[0].Ship != "ok" {
		t.Errorf("panes = %+v, want only the one within 4 KB", got)
	}
}

func TestTmuxCopilotFilesKeepsCopilotShipsWithAKnownDirAndBoundTranscript(t *testing.T) {
	path := "/home/u/.copilot/session-state/" + sessionUUID + "/events.jsonl"
	raw := "1800000000000000000 launch " + path
	cmd := "copilot --session-id " + sessionUUID
	dirs := map[string]string{"p-1": "/home/u/.copilot", "p-2": ""}
	tm := &Tmux{Dir: func(ship string) string { return dirs[ship] }}
	tm.panes = []sessionhost.LivePane{
		{Session: "p-1", Harness: "copilot", Transcript: raw, StartCmd: cmd},
		{Session: "p-2", Harness: "copilot", Transcript: raw, StartCmd: cmd},                                      // no dir known
		{Session: "cl-1", Harness: "claude", Transcript: raw, StartCmd: cmd},                                      // not Copilot
		{Session: "p-1", Harness: "copilot", Transcript: "1800000000000000000 launch /etc/passwd", StartCmd: cmd}, // not bound by the launch
	}
	got := tm.CopilotFiles()
	if len(got) != 1 || got[0].Ship != "p-1" || got[0].Dir != "/home/u/.copilot" || got[0].Path != path {
		t.Errorf("files = %+v, want p-1's events file only", got)
	}
}

func TestCopilotPollReadsOnlyWhatWasAppended(t *testing.T) {
	r := newRig()
	c, _, path := copilotRig(t, r)
	rec := usageRecordLine(t)
	half := len(rec) / 2
	appendFile(t, path, rec+rec[:half])
	c.Poll(context.Background())
	if fig, _, _ := r.store.Figures("p-1"); fig.Tokens.CacheWrite != 35732 {
		t.Fatalf("figures = %+v, want the whole line counted and the half line waited for", fig)
	}
	appendFile(t, path, rec[half:])
	c.Poll(context.Background())
	if fig, _, _ := r.store.Figures("p-1"); fig.Tokens.CacheWrite != 71464 {
		t.Errorf("figures = %+v, want the completed line added once", fig)
	}
}

func TestCopilotPollRestartsWhenTheFileIsRewritten(t *testing.T) {
	r := newRig()
	c, _, path := copilotRig(t, r)
	rec := usageRecordLine(t)
	appendFile(t, path, rec+rec)
	c.Poll(context.Background())
	if err := os.WriteFile(path, []byte(rec), 0o600); err != nil { // shorter: truncated in place
		t.Fatal(err)
	}
	c.Poll(context.Background())
	if fig, _, _ := r.store.Figures("p-1"); fig.Tokens.CacheWrite != 35732 {
		t.Errorf("figures = %+v, want the rewritten file's one record", fig)
	}
	other := path + ".new" // another file under the same path, as long as the old one
	if err := os.WriteFile(other, []byte(rec+rec+rec), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(other, path); err != nil {
		t.Fatal(err)
	}
	c.Poll(context.Background())
	if fig, _, _ := r.store.Figures("p-1"); fig.Tokens.CacheWrite != 3*35732 {
		t.Errorf("figures = %+v, want the replacement file summed from its start", fig)
	}
}

func TestCopilotPollSkipsAFileOverTheCap(t *testing.T) {
	r := newRig()
	c, _, path := copilotRig(t, r)
	appendFile(t, path, usageRecordLine(t))
	if err := os.Truncate(path, maxCopilotFile+1); err != nil {
		t.Fatal(err)
	}
	c.Poll(context.Background())
	if _, _, ok := r.store.Figures("p-1"); ok {
		t.Error("figures from a file over the cap")
	}
}
