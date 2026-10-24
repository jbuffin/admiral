package autopilot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/ships"
)

// Retro orders (#117, ADR 0017): once a panel has had retro_every autopilot
// runs since its last retro, an order `/retro <source>/<panel> --since <ts>` is
// added to the panel autopilot.resume_source names, and a retro_queued record
// says so. The runs are counted from the outcome log on every pass, so a restart
// counts what a live pass does. The record is the reset: until it is confirmed
// written, the panel's retro is pending.

// addTimeout bounds one add.
const addTimeout = 10 * time.Second

// retroState is what the retro step keeps in memory: nothing the log cannot say
// again, but for the records in flight and the lines already logged.
type retroState struct {
	mu       sync.Mutex
	writes   map[Panel]*retroWrite // retro_queued records not confirmed written yet
	addErr   map[Panel]string      // the last refusal logged for the panel's add
	logged   map[string]bool       // lost-write messages logged
	badPanel map[Panel]bool        // panel ids refused and logged
	noResume bool                  // the missing resume_source is logged
}

// retroWrite is a retro_queued record whose add succeeded or may have.
type retroWrite struct {
	rec    outcomes.Record
	failed bool // the write failed or was dropped: write it again on the next pass
}

// retroStep makes the retro orders due this pass. It runs after the launches and
// holds no lock across the plugin call; e.pass keeps two passes from adding for
// the same panel at once.
func (e *Engine) retroStep(cfg *config.Config, list []ships.Ship, srcs map[string]Source) {
	if e.Log == nil || e.AddOrder == nil {
		return
	}
	e.rewriteRetros()
	type due struct {
		panel Panel
		every int
	}
	var panels []due
	for _, def := range cfg.SourceList {
		ids := make([]string, 0, len(def.Autopilot))
		for id := range def.Autopilot {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if entry := def.Autopilot[id]; entry.Enabled && entry.RetroEvery > 0 && !e.Paused(def.Key, id) {
				panels = append(panels, due{Panel{def.Key, id}, entry.RetroEvery})
			}
		}
	}
	if len(panels) == 0 {
		return
	}
	counts := CountRuns(e.Log())
	for _, d := range panels {
		c := counts[d.panel]
		if c.Runs < d.every || e.retroPending(d.panel, c, list, srcs) {
			continue
		}
		e.addRetro(d.panel, c, cfg.ResumeSource)
	}
}

// rewriteRetros writes again the records whose write failed.
func (e *Engine) rewriteRetros() {
	s := &e.retros
	s.mu.Lock()
	again := map[Panel]outcomes.Record{}
	for p, w := range s.writes {
		if w.failed {
			w.failed = false
			again[p] = w.rec
		}
	}
	s.mu.Unlock()
	for p, rec := range again {
		e.writeRetro(p, rec)
	}
}

func (e *Engine) writeRetro(p Panel, rec outcomes.Record) {
	e.Record(rec, func(err error) { e.retroWritten(p, err) })
}

// retroWritten is the record's onDone, which may run on the writer's goroutine.
func (e *Engine) retroWritten(p Panel, err error) {
	s := &e.retros
	s.mu.Lock()
	if err == nil {
		delete(s.writes, p)
		s.mu.Unlock()
		return
	}
	w := s.writes[p]
	if w != nil {
		w.failed = true
	}
	key := fmt.Sprintf("%v: %v", p, err)
	first := !s.logged[key]
	if first {
		if s.logged == nil {
			s.logged = map[string]bool{}
		}
		s.logged[key] = true
	}
	s.mu.Unlock()
	if first {
		e.logf("autopilot: the retro_queued record for %s/%s is not written yet, and is written again: %v", p.Source, p.Panel, err)
	}
}

// retroPending says the panel's last retro is not cleared: its record is not
// confirmed, its order is still listed or its source has not listed yet, or a
// ship of it exists in any status. The log carries the last two over a restart.
func (e *Engine) retroPending(p Panel, c Count, list []ships.Ship, srcs map[string]Source) bool {
	e.retros.mu.Lock()
	_, unwritten := e.retros.writes[p]
	e.retros.mu.Unlock()
	if unwritten {
		return true
	}
	r := c.Retro
	if r == nil {
		return false
	}
	src, ok := srcs[r.ResumeSource]
	if !ok || !src.Listed {
		return true
	}
	for _, pn := range src.Panels {
		if pn.ID != r.ResumePanel {
			continue
		}
		for _, o := range pn.Orders {
			if r.OrderID != "" && o.ID == r.OrderID || r.OrderID == "" && launcher.CleanTitle(o.Title) == r.Text {
				return true
			}
		}
	}
	return slices.ContainsFunc(list, func(sh ships.Ship) bool {
		return sh.Source == r.ResumeSource && (r.OrderID != "" && sh.OrderID == r.OrderID || r.OrderID == "" && sh.Title == r.Text)
	})
}

// addRetro adds the panel's retro order and queues its record.
func (e *Engine) addRetro(p Panel, c Count, target *config.ResumeSource) {
	s := &e.retros
	if bad := func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }; strings.IndexFunc(p.Source+p.Panel, bad) >= 0 {
		s.mu.Lock()
		first := !s.badPanel[p]
		if s.badPanel == nil {
			s.badPanel = map[Panel]bool{}
		}
		s.badPanel[p] = true
		s.mu.Unlock()
		if first {
			e.logf("autopilot: no retro order for %s/%q: the panel id has whitespace or control characters", p.Source, p.Panel)
		}
		return
	}
	if target == nil {
		s.mu.Lock()
		first := !s.noResume
		s.noResume = true
		s.mu.Unlock()
		if first {
			e.logf("autopilot: autopilot.resume_source is not set in the config, so retro orders are not added")
		}
		return
	}
	since := c.Since.UTC().Format(time.RFC3339)
	text := "/retro " + p.Source + "/" + p.Panel + " --since " + since
	ctx, cancel := context.WithTimeout(context.Background(), addTimeout)
	id, err := e.AddOrder(ctx, target.Source, target.Panel, text)
	cancel()
	fields := map[string]any{"source": p.Source, "panel": p.Panel, "since": since, "text": text,
		"resume_source": target.Source, "resume_panel": target.Panel}
	if err != nil {
		var fail interface{ AddKind() inbox.AddKind }
		kind := inbox.AddFinal
		if errors.As(err, &fail) {
			kind = fail.AddKind()
		}
		if kind != inbox.AddUncertain {
			// Retryable, or final: the retro is tried again on a later pass, and the
			// refusal is logged once per message. A resume order is dropped on a
			// final refusal, because its text is kept apart from the log; a retro's
			// count stays in the log, so trying again costs nothing.
			s.mu.Lock()
			first := s.addErr[p] != err.Error()
			if s.addErr == nil {
				s.addErr = map[Panel]string{}
			}
			s.addErr[p] = err.Error()
			s.mu.Unlock()
			if first {
				e.logf("autopilot: the retro order for %s/%s is not added yet: %v", p.Source, p.Panel, err)
			}
			return
		}
		e.logf("autopilot: the retro order for %s/%s may not have landed, and is not sent again: %v", p.Source, p.Panel, err)
		fields["uncertain"] = true
	} else {
		fields["order_id"] = id
	}
	rec := outcomes.Record{Event: "retro_queued", Fields: fields}
	s.mu.Lock()
	delete(s.addErr, p)
	if s.writes == nil {
		s.writes = map[Panel]*retroWrite{}
	}
	s.writes[p] = &retroWrite{rec: rec}
	s.mu.Unlock()
	e.writeRetro(p, rec)
}
