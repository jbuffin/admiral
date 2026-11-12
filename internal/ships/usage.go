package ships

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/transcriptfs"
	"github.com/jbuffin/admiral/internal/usage"
)

// usageReadLimit bounds the goroutine's wait on UsageRead; the transcript parse itself reads a file to its end.
const usageReadLimit = 10 * time.Second

// paneReadLimit bounds the tmux reads queueUsage makes inline, before a kill can take the panes away.
const paneReadLimit = 3 * time.Second

// ExitUsage is what a ship's transcripts gave at its exit. A nil Own, or a boat with nil Figures, means none were found.
type ExitUsage struct {
	Own  *usage.Figures
	Crew []usage.Boat
}

// paneFacts are the pane strings a ship's figures need. A kill removes them within milliseconds, so they are
// read before the goroutine starts; only the files are opened later.
type paneFacts struct {
	transcript, startCmd, rollouts string
	crewRollouts                   map[string]string // by crew pane id, Codex boats only
}

// queueUsage writes the `usage` record beside `ended`, once per exit: retryEnded and recordKilled queue
// `ended` again for the same exit. The panes are read here, before any kill; the transcripts are read on a
// goroutine of their own, so a slow or huge one never holds `ended` or the pass back. A failed write clears
// the guard so retryEnded queues it again.
func (s *Service) queueUsage(sess sessionhost.Session, end *endState) {
	s.mu.Lock()
	again := end.usaged
	end.usaged = true
	end.usageFailed = false
	s.mu.Unlock()
	if again {
		return
	}
	var facts *paneFacts
	if s.UsageRead == nil {
		facts = s.readPanes(sess)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), usageReadLimit)
		defer cancel()
		var ex ExitUsage
		if s.UsageRead != nil {
			ex = s.UsageRead(ctx, sess)
		} else {
			ex = s.transcriptUsage(sess, facts)
		}
		if ex.Own == nil && s.LastLive != nil {
			if f, ok := s.LastLive(sess.Name); ok {
				ex.Own = &f
			}
		}
		round, _ := strconv.Atoi(sess.Options["round"])
		s.record(outcomes.Record{Ship: sess.Name, Event: "usage",
			Fields: usage.ExitFields(sess.Options["fleet"], sess.Options["harness"], round, ex.Own, ex.Crew)}, func(err error) {
			if err != nil {
				s.mu.Lock()
				end.usaged, end.usageFailed = false, true
				s.mu.Unlock()
			}
		})
	}()
}

// readPanes is nil when there is no tmux to ask. A failed read leaves its strings empty, which gives null figures.
func (s *Service) readPanes(sess sessionhost.Session) *paneFacts {
	if s.Launcher == nil || s.Launcher.Tmux == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), paneReadLimit)
	defer cancel()
	srv := sessionhost.NewServer(s.Launcher.Tmux, s.Launcher.Socket)
	f := &paneFacts{crewRollouts: map[string]string{}}
	if sess.PaneID != "" && HasConversation(sess.Options["harness"]) {
		f.transcript, f.startCmd, f.rollouts, _ = srv.ReadPaneFacts(ctx, sess.PaneID)
	}
	for _, c := range sess.Crew {
		if c.Harness == "codex" && c.PaneID != "" {
			if raw, err := srv.ReadPaneOption(ctx, c.PaneID, "rollouts"); err == nil {
				f.crewRollouts[c.PaneID] = raw
			}
		}
	}
	return f
}

// transcriptUsage reads the figures of the ship's own transcript and of each crew boat's, live or closed.
// Admiral crew boats are separate harness processes; in-process subagents are already inside their ship's figures.
func (s *Service) transcriptUsage(sess sessionhost.Session, f *paneFacts) ExitUsage {
	var ex ExitUsage
	fleet, harness := sess.Options["fleet"], sess.Options["harness"]
	if f == nil {
		f = &paneFacts{}
	}
	if cfg := s.dockConfig(fleet, harness); cfg != "" {
		ex.Own = ownFigures(harness, cfg, f)
	}
	for _, c := range sess.Crew {
		b := usage.Boat{Name: c.Name, Harness: c.Harness}
		if dir := s.dockConfig(fleet, c.Harness); dir != "" {
			paths := sessionhost.ParseRollouts(f.crewRollouts[c.PaneID])
			if sessionhost.ValidCrewTranscriptPath(c.Transcript) && !slices.Contains(paths, c.Transcript) {
				paths = append(paths, c.Transcript)
			}
			b.Figures = sumPaths(c.Harness, dir, paths)
		}
		ex.Crew = append(ex.Crew, b)
	}
	type key struct{ name, harness string }
	var order []key
	closed := map[key][]string{}
	for _, cb := range sessionhost.ParseClosed(sess.Options[sessionhost.OptClosedCrew]) {
		k := key{cb.Name, cb.Harness}
		if _, seen := closed[k]; !seen {
			order = append(order, k)
		}
		closed[k] = append(closed[k], cb.Path)
	}
	for _, k := range order {
		b := usage.Boat{Name: k.name, Harness: k.harness}
		if dir := s.dockConfig(fleet, k.harness); dir != "" {
			b.Figures = sumPaths(k.harness, dir, closed[k])
		}
		ex.Crew = append(ex.Crew, b)
	}
	return ex
}

// sumPaths parses each distinct transcript and adds the figures: a Codex process that ran /new or resumed has one
// rollout per thread, and each keeps its own totals.
func sumPaths(harness, dir string, paths []string) *usage.Figures {
	var parts []*usage.Figures
	seen := map[string]bool{}
	for _, p := range paths {
		if !seen[p] && len(seen) < sessionhost.MaxRollouts+1 {
			seen[p] = true
			parts = append(parts, figuresOf(harness, dir, p, ""))
		}
	}
	return usage.SumFigures(parts)
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
// the launch's --session-id, Codex through every rollout its hook recorded and the one the pane names now.
func ownFigures(harness, dir string, f *paneFacts) *usage.Figures {
	if !HasConversation(harness) {
		return nil
	}
	parts := strings.SplitN(f.transcript, " ", 3)
	if harness == "codex" {
		paths := sessionhost.ParseRollouts(f.rollouts)
		if len(parts) == 3 && sessionhost.ValidTranscriptPath(parts[2]) && !slices.Contains(paths, parts[2]) {
			paths = append(paths, parts[2])
		}
		return sumPaths(harness, dir, paths)
	}
	path, id, ok := transcriptfs.BindFor(harness, f.transcript, f.startCmd)
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
