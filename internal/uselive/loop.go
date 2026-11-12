package uselive

import (
	"context"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/transcriptfs"
)

// Interval is the loop's tick: one tmux read and one poll of each feed.
const Interval = 2 * time.Second

// readLimit bounds a tick's tmux read so a hung tmux cannot stall the loop.
const readLimit = 5 * time.Second

// Tmux reads the panes once per tick and hands the same snapshot to the Claude and Copilot feeds.
type Tmux struct {
	Server sessionhost.Server
	// Dir is the identity dir of a ship's harness login, "" when unknown.
	Dir func(ship string) string

	mu    sync.Mutex
	panes []sessionhost.LivePane
	err   error
}

func (t *Tmux) refresh(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, readLimit)
	defer cancel()
	panes, err := t.Server.ReadLivePanes(rctx)
	t.mu.Lock()
	t.panes, t.err = panes, err
	t.mu.Unlock()
}

// ClaudePanes is the Claude feed's Panes.
func (t *Tmux) ClaudePanes(context.Context) ([]Pane, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		return nil, t.err
	}
	var out []Pane
	for _, p := range t.panes {
		if p.Statusline != "" {
			out = append(out, Pane{Ship: p.Session, Option: p.Statusline})
		}
	}
	return out, nil
}

// CopilotFiles is the Copilot feed's Files: each Copilot ship's events file, as its launch named it.
func (t *Tmux) CopilotFiles() []File {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []File
	for _, p := range t.panes {
		if p.Harness != "copilot" {
			continue
		}
		path, _, ok := transcriptfs.BindFor("copilot", p.Transcript, p.StartCmd)
		if dir := t.Dir(p.Session); ok && dir != "" {
			out = append(out, File{Ship: p.Session, Dir: dir, Path: path})
		}
	}
	return out
}

// Run ticks until ctx ends: one tmux read, then each poll in turn on that snapshot.
func (t *Tmux) Run(ctx context.Context, polls ...func(context.Context)) {
	tick := time.NewTicker(Interval)
	defer tick.Stop()
	for {
		t.refresh(ctx)
		for _, poll := range polls {
			poll(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
