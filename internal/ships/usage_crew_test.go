package ships

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

const claudeLine = `{"type":"cost-state","totalCostUSD":0.5,"modelUsage":{"m":{"inputTokens":20,"outputTokens":2}}}`

func codexLine(input int64) string {
	return `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` +
		strconv.FormatInt(input, 10) + `,"cached_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":0}}}}`
}

func writeTranscript(t *testing.T, path string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var s string
	for _, l := range lines {
		s += l + "\n"
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The crew boats' transcripts are read from the fleet's own config directories, one rollout per Codex boat.
func TestTranscriptUsageReadsEachCrewBoatsTranscript(t *testing.T) {
	root := t.TempDir()
	claudeDir, codexDir := filepath.Join(root, "claude"), filepath.Join(root, "codex")
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Harnesses: []config.Harness{
		{Name: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": claudeDir}},
		{Name: "codex", Env: map[string]string{"CODEX_HOME": codexDir}}}}}}
	s := &Service{Launcher: &launcher.Launcher{Config: func() *config.Config { return cfg }}}

	scout := writeTranscript(t, filepath.Join(claudeDir, "projects", "p", "a.jsonl"), claudeLine)
	c1 := writeTranscript(t, filepath.Join(codexDir, "sessions", "2026", "11", "11", "rollout-1.jsonl"), codexLine(100), codexLine(300))
	c2 := writeTranscript(t, filepath.Join(codexDir, "sessions", "2026", "11", "11", "rollout-2.jsonl"), codexLine(7))
	outside := writeTranscript(t, filepath.Join(root, "elsewhere", "x.jsonl"), claudeLine)

	sess := sessionhost.Session{Name: "a-1", Options: map[string]string{"fleet": "work", "harness": "claude"}, Crew: []sessionhost.Crew{
		{Name: "scout", Harness: "claude", Transcript: scout},
		{Name: "codey-1", Harness: "codex", Transcript: c1},
		{Name: "codey-2", Harness: "codex", Transcript: c2},
		{Name: "new", Harness: "claude"},
		{Name: "rogue", Harness: "claude", Transcript: outside},
	}}
	got := s.transcriptUsage(sess, nil)

	if got.Own != nil {
		t.Errorf("own = %+v, want none: the pane has no transcript option here", got.Own)
	}
	if len(got.Crew) != 5 {
		t.Fatalf("crew = %+v, want the five boats listed in order", got.Crew)
	}
	in := func(i int) (int64, bool) {
		if got.Crew[i].Figures == nil {
			return 0, false
		}
		return got.Crew[i].Figures.Tokens.Input, true
	}
	if v, ok := in(0); !ok || v != 20 || got.Crew[0].Figures.Money == nil || got.Crew[0].Figures.Money.Amount != 0.5 {
		t.Errorf("scout = %+v, want 20 input and $0.5", got.Crew[0].Figures)
	}
	if v, ok := in(1); !ok || v != 300 {
		t.Errorf("codey-1 = %+v, want its rollout's last total (300)", got.Crew[1].Figures)
	}
	if v, ok := in(2); !ok || v != 7 {
		t.Errorf("codey-2 = %+v, want its own rollout's total (7), not summed into the first", got.Crew[2].Figures)
	}
	for _, i := range []int{3, 4} {
		if got.Crew[i].Figures != nil {
			t.Errorf("boat %s = %+v, want no figures (no transcript, or one outside the config dir)", got.Crew[i].Name, got.Crew[i].Figures)
		}
	}
}

func codexService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	codexDir := filepath.Join(root, "codex")
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Harnesses: []config.Harness{
		{Name: "codex", Env: map[string]string{"CODEX_HOME": codexDir}}}}}}
	return &Service{Launcher: &launcher.Launcher{Config: func() *config.Config { return cfg }}}, filepath.Join(codexDir, "sessions", "2026", "11", "11")
}

// A Codex ship or boat that ran /new keeps the tokens of the thread it left: every recorded rollout is summed.
func TestTranscriptUsageSumsEveryRolloutAPaneRecorded(t *testing.T) {
	s, dir := codexService(t)
	r1 := writeTranscript(t, filepath.Join(dir, "rollout-1.jsonl"), codexLine(100), codexLine(300))
	r2 := writeTranscript(t, filepath.Join(dir, "rollout-2.jsonl"), codexLine(7))
	b1 := writeTranscript(t, filepath.Join(dir, "rollout-b1.jsonl"), codexLine(50))
	b2 := writeTranscript(t, filepath.Join(dir, "rollout-b2.jsonl"), codexLine(5))

	sess := sessionhost.Session{Name: "a-1", PaneID: "%1", Options: map[string]string{"fleet": "work", "harness": "codex"},
		Crew: []sessionhost.Crew{{Name: "codey", Harness: "codex", PaneID: "%2", Transcript: b2}}}
	got := s.transcriptUsage(sess, &paneFacts{
		transcript:   "1 startup " + r2,
		rollouts:     r1 + " " + r2,
		crewRollouts: map[string]string{"%2": b1 + " " + b2},
	})
	if got.Own == nil || got.Own.Tokens.Input != 307 {
		t.Errorf("own = %+v, want both rollouts summed (300 + 7), the current one once", got.Own)
	}
	if len(got.Crew) != 1 || got.Crew[0].Figures == nil || got.Crew[0].Figures.Tokens.Input != 55 {
		t.Errorf("crew = %+v, want the boat's two rollouts summed (50 + 5)", got.Crew)
	}
}

// A boat closed before its ship exits is still counted, from the paths the close kept on the session.
func TestTranscriptUsageCountsClosedBoats(t *testing.T) {
	s, dir := codexService(t)
	a := writeTranscript(t, filepath.Join(dir, "rollout-a.jsonl"), codexLine(40))
	b := writeTranscript(t, filepath.Join(dir, "rollout-b.jsonl"), codexLine(2))
	closed := sessionhost.AppendClosed("", []sessionhost.ClosedBoat{{Name: "gone", Harness: "codex", Path: a}, {Name: "gone", Harness: "codex", Path: b}})
	sess := sessionhost.Session{Name: "a-1", Options: map[string]string{"fleet": "work", "harness": "codex", sessionhost.OptClosedCrew: closed + " junk:codex:/etc/passwd"}}
	got := s.transcriptUsage(sess, nil)
	if len(got.Crew) != 1 || got.Crew[0].Name != "gone" || got.Crew[0].Figures == nil || got.Crew[0].Figures.Tokens.Input != 42 {
		t.Errorf("crew = %+v, want one closed boat with 42 input; a token with an invalid path is ignored", got.Crew)
	}
}
