package autopilot

import (
	"slices"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
)

type Panel struct{ Source, Panel string }

// Count is what the outcome log says about one panel's retros.
type Count struct {
	// Runs counts since the panel's last retro_queued.
	Runs int
	// Since is that retro_queued's ts; with none, the first counted run's launch.
	Since time.Time
	Retro *Retro
	// Retros is oldest first; a retro is pending while any of them has an order or a ship.
	Retros []Retro
}

// Retro is a retro_queued record: the order added to the resume panel.
type Retro struct {
	ResumeSource, ResumePanel string
	OrderID                   string // empty when the add was uncertain
	Text                      string
}

// An uncertain record has no order id, so it matches by exact title; one with
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

// CountRuns joins each ended to the ship's latest earlier launched by log
// position, never ts. It trusts every retro_queued, so filter the log with ForTarget first.
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

func str(fields map[string]any, key string) string {
	s, _ := fields[key].(string)
	return s
}

// ForTarget drops retro_queued records for any other resume target: Admiral writes
// one only for its configured target, so another is a forgery or predates a target change.
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
