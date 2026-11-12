package ships

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/transcriptfs"
	"github.com/jbuffin/admiral/internal/usage"
)

// usageReadLimit bounds the tmux calls and file reads behind one exit's figures.
const usageReadLimit = 10 * time.Second

// ExitUsage is what a ship's transcripts gave at its exit. A nil Own, or a boat with nil Figures, means none were found.
type ExitUsage struct {
	Own  *usage.Figures
	Crew []usage.Boat
}

// queueUsage writes the `usage` record beside `ended`, once per exit: retryEnded and recordKilled queue
// `ended` again for the same exit. The transcripts are read on a goroutine of their own, so a slow or
// huge one never holds `ended` or the pass back.
func (s *Service) queueUsage(sess sessionhost.Session, end *endState) {
	s.mu.Lock()
	again := end.usaged
	end.usaged = true
	s.mu.Unlock()
	if again {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), usageReadLimit)
		defer cancel()
		var ex ExitUsage
		if s.UsageRead != nil {
			ex = s.UsageRead(ctx, sess)
		}
		if ex.Own == nil && s.LastLive != nil {
			if f, ok := s.LastLive(sess.Name); ok {
				ex.Own = &f
			}
		}
		round, _ := strconv.Atoi(sess.Options["round"])
		s.record(outcomes.Record{Ship: sess.Name, Event: "usage",
			Fields: usage.ExitFields(sess.Options["fleet"], sess.Options["harness"], round, ex.Own, ex.Crew)}, nil)
	}()
}

// TranscriptUsage reads the figures of the ship's own transcript and of each crew boat's. Admiral crew
// boats are separate harness processes; in-process subagents are already inside their ship's figures.
func (s *Service) TranscriptUsage(ctx context.Context, sess sessionhost.Session) ExitUsage {
	var ex ExitUsage
	harness := sess.Options["harness"]
	if cfg := s.dockConfig(sess.Options["fleet"], harness); cfg != "" && s.Launcher != nil && sess.PaneID != "" {
		ex.Own = s.ownFigures(ctx, sess, harness, cfg)
	}
	for _, c := range sess.Crew {
		b := usage.Boat{Name: c.Name, Harness: c.Harness}
		if dir := s.dockConfig(sess.Options["fleet"], c.Harness); dir != "" && sessionhost.ValidCrewTranscriptPath(c.Transcript) {
			b.Figures = figuresOf(c.Harness, dir, c.Transcript, "")
		}
		ex.Crew = append(ex.Crew, b)
	}
	return ex
}

func (s *Service) dockConfig(fleet, harness string) string {
	if s.Launcher == nil || s.Launcher.Config == nil {
		return ""
	}
	f, ok := s.Launcher.Config().Fleet(fleet)
	if !ok {
		return ""
	}
	var env map[string]string
	if d, has := f.Harness(harness); has {
		env = d.Env
	}
	return config.IdentityDir(harness, env)
}

// ownFigures finds the ship's transcript the way the conversation view does: Claude and Copilot only through
// the launch's --session-id, Codex through whatever rollout its hook recorded.
func (s *Service) ownFigures(ctx context.Context, sess sessionhost.Session, harness, dir string) *usage.Figures {
	if !HasConversation(harness) || s.Launcher.Tmux == "" {
		return nil
	}
	srv := sessionhost.NewServer(s.Launcher.Tmux, s.Launcher.Socket)
	raw, err := srv.ReadPaneOption(ctx, sess.PaneID, "transcript")
	if err != nil {
		return nil
	}
	parts := strings.SplitN(raw, " ", 3)
	if harness == "codex" {
		if len(parts) != 3 || !sessionhost.ValidTranscriptPath(parts[2]) {
			return nil
		}
		return figuresOf(harness, dir, parts[2], "")
	}
	startCmd, err := srv.ReadPaneStartCommand(ctx, sess.PaneID)
	if err != nil {
		return nil
	}
	path, id, ok := transcriptfs.BindFor(harness, raw, startCmd)
	if !ok {
		return nil
	}
	if harness == "copilot" {
		id = ""
	}
	return figuresOf(harness, dir, path, id)
}

// figuresOf is nil when the file is missing, refused or holds no figures. A non-empty id lets Claude's file be found
// under another project directory.
func figuresOf(harness, dir, path, id string) *usage.Figures {
	f, _, err := transcriptfs.OpenFor(harness, dir, path)
	if errors.Is(err, fs.ErrNotExist) && id != "" {
		var p string
		if p, err = transcriptfs.Find(dir, id); err == nil {
			f, _, err = transcriptfs.OpenFor(harness, dir, p)
		}
	}
	if err != nil {
		return nil
	}
	defer f.Close()
	var parse func(io.Reader) (usage.Figures, error)
	switch harness {
	case "codex":
		parse = usage.ParseCodex
	case "copilot":
		parse = usage.ParseCopilot
	default:
		parse = usage.ParseClaude
	}
	fig, err := parse(f)
	if err != nil {
		return nil
	}
	return &fig
}
