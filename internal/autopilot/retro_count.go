package autopilot

import (
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// Panel names a panel of a source.
type Panel struct{ Source, Panel string }

// Count is what the outcome log says about one panel's retros (#117, ADR 0017).
type Count struct {
	// Runs is the counted runs since the panel's last retro_queued.
	Runs int
	// Since is that retro_queued's ts; with none, the launched ts of the first
	// counted run.
	Since time.Time
	// Retro is the panel's last retro_queued, nil with none.
	Retro *Retro
}

// Retro is a retro_queued record: the order added to the resume panel.
type Retro struct {
	ResumeSource, ResumePanel string
	OrderID                   string // empty when the add was uncertain
	Text                      string
}

// retroKey says which launched record is a retro ship: the resume source and
// the order id, or for an uncertain record, which has none, its text as title.
type retroKey struct{ source, orderID, title string }

// launch is a launched record a later ended can join.
type launch struct {
	panel  Panel
	auto   bool
	retro  bool
	ts     time.Time
	joined bool // an ended has been matched to it
}

// CountRuns counts, per panel, the autopilot runs since its last retro_queued.
// A counted run is an ended that joins to the latest earlier launched of the
// same ship, at most once per launched, where that launched is an autopilot one
// for the panel and not a retro ship. Earlier is position in the log, never ts.
// The log is the whole state: a restart counts the same as a live pass.
func CountRuns(log []outcomes.Line) map[Panel]Count {
	retroShips := map[retroKey]bool{}
	for _, l := range log {
		if l.Event != "retro_queued" {
			continue
		}
		r := retroOf(l)
		if r.OrderID != "" {
			retroShips[retroKey{source: r.ResumeSource, orderID: r.OrderID}] = true
		} else {
			retroShips[retroKey{source: r.ResumeSource, title: r.Text}] = true
		}
	}
	counts := map[Panel]Count{}
	open := map[string]*launch{}
	for _, l := range log {
		switch l.Event {
		case "launched":
			source, panel := str(l.Fields, "source"), str(l.Fields, "panel")
			auto, _ := l.Fields["autopilot"].(bool)
			open[l.Ship] = &launch{panel: Panel{source, panel}, auto: auto, ts: l.TS,
				retro: retroShips[retroKey{source: source, orderID: str(l.Fields, "order_id")}] ||
					retroShips[retroKey{source: source, title: str(l.Fields, "title")}]}
		case "ended":
			ln := open[l.Ship]
			if ln == nil || ln.joined {
				continue
			}
			ln.joined = true
			if !ln.auto || ln.retro {
				continue
			}
			c := counts[ln.panel]
			if c.Runs == 0 && c.Retro == nil {
				c.Since = ln.ts
			}
			c.Runs++
			counts[ln.panel] = c
		case "retro_queued":
			r := retroOf(l)
			counts[Panel{str(l.Fields, "source"), str(l.Fields, "panel")}] = Count{Since: l.TS, Retro: &r}
		}
	}
	return counts
}

func retroOf(l outcomes.Line) Retro {
	return Retro{ResumeSource: str(l.Fields, "resume_source"), ResumePanel: str(l.Fields, "resume_panel"),
		OrderID: str(l.Fields, "order_id"), Text: str(l.Fields, "text")}
}

// str is a string field of a line, "" when it is missing or not a string.
func str(fields map[string]any, key string) string {
	s, _ := fields[key].(string)
	return s
}
