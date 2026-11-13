package commitments

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/jbuffin/admiral/internal/ledger"
	"github.com/jbuffin/admiral/internal/outcomes"
)

type verdictKind int

const (
	verdictWait verdictKind = iota
	verdictFire
	verdictDrop
)

type verdict struct {
	what    verdictKind
	why     string
	recheck time.Time // when a pending settle ends, so the runner looks again then
}

// rec is one outcome-log line of a ship.
type rec struct {
	at       time.Time
	event    string
	launched string
	reason   string
	clean    bool // an `ended` with exit code 0
	killed   bool // an `ended` by signal KILL: a stop or a dismissal writes it, then its own record
}

// shipLog is the outcome log's records by ship, oldest first.
type shipLog struct{ byShip map[string][]rec }

// readLog reads the log from the month of the earliest launch stamp a ship trigger names.
func (r *Runner) readLog(list []ledger.Commitment) *shipLog {
	var since time.Time
	for _, c := range list {
		if c.Trigger.Kind != ledger.TriggerShipSailed && c.Trigger.Kind != ledger.TriggerShipAnchored {
			continue
		}
		if t, err := time.Parse(time.RFC3339, c.Trigger.Launched); err == nil && (since.IsZero() || t.Before(since)) {
			since = t
		}
	}
	log := &shipLog{byShip: map[string][]rec{}}
	files, err := outcomes.MonthFiles(filepath.Join(r.Home, "outcomes"))
	if err != nil || since.IsZero() {
		return log
	}
	for _, f := range files {
		if end := f.End(); !end.IsZero() && !end.After(since) {
			continue
		}
		log.readFile(f.Path)
	}
	return log
}

func (l *shipLog) readFile(path string) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	br := bufio.NewReaderSize(fh, 64<<10)
	for {
		line, err := outcomes.ReadLine(br)
		if len(line) > 0 && line[0] == '{' {
			var raw struct {
				TS       time.Time `json:"ts"`
				Ship     string    `json:"ship"`
				Event    string    `json:"event"`
				Launched string    `json:"launched"`
				Reason   string    `json:"reason"`
				ExitCode *int      `json:"exit_code"`
				Signal   *string   `json:"signal"`
			}
			if json.Unmarshal(line, &raw) == nil && raw.Ship != "" {
				e := rec{at: raw.TS, event: raw.Event, launched: raw.Launched, reason: raw.Reason}
				if raw.Event == "ended" {
					e.clean = raw.Signal == nil && raw.ExitCode != nil && *raw.ExitCode == 0
					e.killed = raw.Signal != nil && *raw.Signal == "KILL"
				}
				l.byShip[raw.Ship] = append(l.byShip[raw.Ship], e)
			}
		}
		if err != nil {
			return
		}
	}
}

// segment is the records of the ship launched at stamp: after its `launched` and before the next launch of the name.
func (l *shipLog) segment(ship, stamp string) []rec {
	recs := l.byShip[ship]
	start := -1
	for i, e := range recs {
		if e.event == "launched" && stamp != "" && e.launched == stamp {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	out := recs[start+1:]
	for i, e := range out {
		if e.event == "launched" {
			return out[:i]
		}
	}
	return out
}

// Where a ship stands, from its records in order.
type stance int

const (
	running stance = iota
	endedClean
	anchored
	killed // ended by KILL, until its `stopped` or `dismissed` says why
	moored
	sailed
	stopped
	dismissed
	unfollowed
)

// The ship trigger table, the single source of these rules (every row has a test):
//
//	stance            ship_sailed                      ship_anchored
//	clean end, no moor within SettleGrace  fires               drops: the ship sailed off
//	unmoored merged, closed, prs_done      fires               drops: the ship sailed off
//	ended with an error (or KILL unexplained)  waits: it can be woken   fires
//	moored, woken (running)                waits               waits
//	stopped                                drops               drops (a stop is not anchoring)
//	dismissed (also unmoored as dismissed) drops               drops
//	unmoored as unfollowed                 drops               drops
func (l *shipLog) verdict(c ledger.Commitment, now time.Time) verdict {
	recs := l.segment(c.Trigger.Subject, c.Trigger.Launched)
	cur, at := running, time.Time{}
	for i := 0; ; i++ {
		var next *rec
		if i < len(recs) {
			next = &recs[i]
		}
		// A stance that needs the next record, or the time, to settle.
		switch cur {
		case endedClean:
			if next != nil && next.event == "moored" {
				break
			}
			if next == nil || next.at.Sub(at) >= SettleGrace {
				if now.Sub(at) < SettleGrace {
					return verdict{recheck: at.Add(SettleGrace)}
				}
				cur = sailed
			}
		case killed:
			if next != nil && (next.event == "stopped" || next.event == "dismissed") {
				break
			}
			if next == nil && now.Sub(at) < SettleGrace {
				return verdict{recheck: at.Add(SettleGrace)}
			}
			cur = anchored
		}
		if v, done := decide(c, cur); done {
			return v
		}
		if next == nil {
			return verdict{}
		}
		cur, at = apply(cur, *next), next.at
	}
}

func apply(cur stance, e rec) stance {
	switch e.event {
	case "ended":
		switch {
		case e.clean:
			return endedClean
		case e.killed:
			return killed
		}
		return anchored
	case "moored":
		return moored
	case "woken":
		return running
	case "stopped":
		return stopped
	case "dismissed":
		return dismissed
	case "unmoored":
		switch e.reason {
		case "merged", "closed", "prs_done":
			return sailed
		case "dismissed":
			return dismissed
		case "unfollowed":
			return unfollowed
		}
	}
	return cur
}

func decide(c ledger.Commitment, cur stance) (verdict, bool) {
	drop := func(why string) (verdict, bool) { return verdict{what: verdictDrop, why: why}, true }
	switch cur {
	case stopped:
		return drop("the ship was stopped by the engineer")
	case dismissed:
		return drop("the ship was dismissed")
	case unfollowed:
		return drop("the ship was unfollowed")
	}
	if c.Trigger.Kind == ledger.TriggerShipSailed {
		if cur == sailed {
			return verdict{what: verdictFire}, true
		}
		return verdict{}, false
	}
	switch cur {
	case anchored:
		return verdict{what: verdictFire}, true
	case sailed:
		return drop("the ship sailed off, so it will not anchor")
	}
	return verdict{}, false
}
