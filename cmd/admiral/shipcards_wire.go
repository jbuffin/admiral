package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jbuffin/admiral/internal/chat"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/ships"
)

const (
	// shipCardLookback bounds the log a pass reads; a voyage's own state decides what is still pending.
	shipCardLookback = 14 * 24 * time.Hour
	shipCardTick     = 5 * time.Second
)

// shipCardRunner feeds the ship snapshot and the outcome log to the chat's ship card detector.
type shipCardRunner struct {
	cards   *chat.ShipCards
	list    func() []ships.Ship
	dir     string
	ready   <-chan struct{}
	now     func() time.Time
	changed chan struct{}
	cached  map[string]cachedRecords
	logf    func(format string, args ...any)
	lastErr string
}

type cachedRecords struct {
	size  int64
	mtime time.Time
	recs  []chat.ShipRecord
}

func newShipCardRunner(home string, post func(fleet string, c chat.ShipCard) error, list func() []ships.Ship, ready <-chan struct{}) *shipCardRunner {
	return &shipCardRunner{cards: chat.NewShipCards(home, post), list: list, dir: filepath.Join(home, "outcomes"), ready: ready, now: time.Now,
		changed: make(chan struct{}, 1), cached: map[string]cachedRecords{},
		logf: func(format string, args ...any) { fmt.Fprintf(os.Stderr, "admiral: "+format+"\n", args...) }}
}

// Changed asks for a pass soon; it never blocks.
func (r *shipCardRunner) Changed() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// Run passes on every change and every few seconds, so a sail-off settles when its grace ends. It waits for the host's
// first listing: a pass over an empty list would seed nothing and then post a card for every ship already moored.
func (r *shipCardRunner) Run(ctx context.Context) {
	if r.ready != nil {
		select {
		case <-r.ready:
		case <-ctx.Done():
			return
		}
	}
	tick := time.NewTicker(shipCardTick)
	defer tick.Stop()
	for {
		r.pass()
		select {
		case <-ctx.Done():
			return
		case <-r.changed:
		case <-tick.C:
		}
	}
}

func (r *shipCardRunner) pass() {
	now := r.now()
	err := r.cards.Observe(r.list(), r.records(now), now)
	switch {
	case err == nil:
		r.lastErr = ""
	case err.Error() != r.lastErr:
		r.lastErr = err.Error()
		r.logf("ship cards: %v", err)
	}
}

// records is the log's records of the lookback.
func (r *shipCardRunner) records(now time.Time) []chat.ShipRecord {
	files, err := outcomes.MonthFiles(r.dir)
	if err != nil {
		return nil
	}
	since := now.Add(-shipCardLookback)
	var out []chat.ShipRecord
	kept := map[string]cachedRecords{}
	for _, f := range files {
		if end := f.End(); !end.IsZero() && !end.After(since) {
			continue
		}
		info, err := os.Stat(f.Path)
		if err != nil {
			continue
		}
		c, ok := r.cached[f.Path]
		if !ok || c.size != info.Size() || !c.mtime.Equal(info.ModTime()) {
			data, err := os.ReadFile(f.Path)
			if err != nil {
				continue
			}
			c = cachedRecords{size: info.Size(), mtime: info.ModTime(), recs: parseShipRecords(data)}
		}
		kept[f.Path] = c
		for _, rec := range c.recs {
			if !rec.At.Before(since) {
				out = append(out, rec)
			}
		}
	}
	r.cached = kept
	return out
}

func parseShipRecords(data []byte) []chat.ShipRecord {
	var out []chat.ShipRecord
	br := bufio.NewReaderSize(bytes.NewReader(data), 64<<10)
	for {
		line, err := outcomes.ReadLine(br)
		if len(line) > 0 && line[0] == '{' {
			var raw struct {
				TS       time.Time       `json:"ts"`
				Ship     string          `json:"ship"`
				Event    string          `json:"event"`
				Reason   string          `json:"reason"`
				ExitCode *int            `json:"exit_code"`
				Signal   *string         `json:"signal"`
				Returned bool            `json:"returned"`
				Scout    bool            `json:"scout"`
				Report   json.RawMessage `json:"report"`
				Result   string          `json:"result"`
			}
			if json.Unmarshal(line, &raw) == nil && raw.Ship != "" {
				out = append(out, chat.ShipRecord{At: raw.TS, Ship: raw.Ship, Event: raw.Event, Reason: raw.Reason,
					Clean: raw.Event == "ended" && raw.Signal == nil && raw.ExitCode != nil && *raw.ExitCode == 0, Returned: raw.Returned, Scout: raw.Scout, Result: raw.Result, ReportResult: moor.ParseReport(raw.Report).Result})
			}
		}
		if err != nil {
			return out
		}
	}
}

// shipReportHook queues a card's report turn with the result its ended record carried, and the live report's when it did not.
func shipReportHook(enqueue func(fleet string, c chat.ShipCard, reportResult string) error, live func(session string) string) func(string, chat.ShipCard) error {
	return func(fleet string, c chat.ShipCard) error {
		result := c.ReportResult
		if result == "" {
			result = live(c.Ship)
		}
		return enqueue(fleet, c, result)
	}
}
