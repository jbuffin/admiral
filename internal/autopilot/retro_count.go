package autopilot

import (
	"slices"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
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
	// Retros is every retro_queued of the panel, oldest first. The count restarts
	// at the last; a retro is pending while any of them has an order or a ship.
	Retros []Retro
}

// Retro is a retro_queued record: the order added to the resume panel.
type Retro struct {
	ResumeSource, ResumePanel string
	OrderID                   string // empty when the add was uncertain
	Text                      string
}

// matches says an order, or a ship's order, is this retro's: the order id, or
// for an uncertain record, which has none, the exact title. A record with
// neither an order id nor a text matches nothing.
func (r Retro) matches(orderID, title string) bool {
	if r.OrderID != "" {
		return orderID == r.OrderID
	}
	return r.Text != "" && launcher.CleanTitle(title) == launcher.CleanTitle(r.Text)
}

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
// The log is the whole state: a restart counts the same as a live pass. It
// trusts every retro_queued it is given, so the caller passes only the records
// for the configured resume target (ForTarget).
func CountRuns(log []outcomes.Line) map[Panel]Count {
	// The retro ships are told by the panel they launch in, so a record names the
	// orders of its resume panel and no other.
	retroShips := map[Panel][]Retro{}
	for _, l := range log {
		if l.Event == "retro_queued" {
			r := retroOf(l)
			to := Panel{r.ResumeSource, r.ResumePanel}
			retroShips[to] = append(retroShips[to], r)
		}
	}
	counts := map[Panel]Count{}
	open := map[string]*launch{}
	for _, l := range log {
		switch l.Event {
		case "launched":
			source, panel := str(l.Fields, "source"), str(l.Fields, "panel")
			auto, _ := l.Fields["autopilot"].(bool)
			orderID, title := str(l.Fields, "order_id"), str(l.Fields, "title")
			open[l.Ship] = &launch{panel: Panel{source, panel}, auto: auto, ts: l.TS,
				retro: slices.ContainsFunc(retroShips[Panel{source, panel}], func(r Retro) bool { return r.matches(orderID, title) })}
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
			p := Panel{str(l.Fields, "source"), str(l.Fields, "panel")}
			counts[p] = Count{Since: l.TS, Retro: &r, Retros: append(counts[p].Retros, r)}
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

// ForTarget is the log without the retro_queued records that name another
// resume target than the configured one. Admiral writes a record only for the
// target it added the order to, so a record for any other is a ship's forgery or
// a retro queued before the engineer changed autopilot.resume_source; it counts
// for nothing, and changing the target drops the hold on the old one's retros.
func ForTarget(log []outcomes.Line, target config.ResumeSource) []outcomes.Line {
	out := make([]outcomes.Line, 0, len(log))
	for _, l := range log {
		if l.Event == "retro_queued" && (str(l.Fields, "resume_source") != target.Source || str(l.Fields, "resume_panel") != target.Panel) {
			continue
		}
		out = append(out, l)
	}
	return out
}
