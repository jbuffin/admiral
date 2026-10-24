package autopilot

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// Counting runs from the outcome log (#117, ADR 0017). The interface these tests
// assume:
//
//	type Panel struct{ Source, Panel string }
//	type Count struct {
//	    Runs  int       // counted runs since the panel's last retro_queued
//	    Since time.Time // that retro_queued's ts; with none, the first counted run's launched ts
//	}
//	func CountRuns(log []outcomes.Line) map[Panel]Count
//
// A counted run is an `ended` joined to the latest earlier `launched` of the
// same ship, at most once per `launched`, where the `launched` is an autopilot
// one for the panel. "Earlier" is position in the log, never ts. Count may
// hold more fields; a panel with no counted run may be absent or zero.

// logb builds a log, one minute apart, in the order the calls are made.
type logb struct {
	t   *testing.T
	n   int
	log []outcomes.Line
}

var logStart = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func (b *logb) add(event, ship string, fields map[string]any) time.Time {
	b.n++
	ts := logStart.Add(time.Duration(b.n) * time.Minute)
	b.log = append(b.log, outcomes.Line{Event: event, Ship: ship, TS: ts, Fields: fields})
	return ts
}

func (b *logb) launched(ship, source, panel, order string) time.Time {
	return b.add("launched", ship, map[string]any{"source": source, "panel": panel, "order_id": order,
		"title": "Title " + order, "autopilot": true})
}

func (b *logb) byHand(ship, source, panel, order string) {
	b.add("launched", ship, map[string]any{"source": source, "panel": panel, "order_id": order, "autopilot": false})
}

func (b *logb) ended(ship string) time.Time {
	return b.add("ended", ship, map[string]any{"exit": 0, "status": "sailed"})
}

func (b *logb) retro(source, panel, resumeSource, order string) time.Time {
	return b.add("retro_queued", "", map[string]any{"source": source, "panel": panel, "resume_source": resumeSource,
		"resume_panel": "q", "order_id": order, "text": "/retro " + source + "/" + panel + " --since x"})
}

func TestCountRuns(t *testing.T) {
	sp := Panel{"s", "p"}
	cases := []struct {
		name  string
		build func(b *logb) (since time.Time)
		want  map[Panel]int
	}{
		{"every ended of an autopilot launch counts, whatever the exit", func(b *logb) time.Time {
			first := b.launched("a", "s", "p", "o1")
			b.ended("a")
			b.launched("b", "s", "p", "o2")
			b.add("ended", "b", map[string]any{"exit": 3, "status": "failed"})
			return first
		}, map[Panel]int{sp: 2}},
		{"Since is the launched ts of the first counted run, not of a ship that never ended", func(b *logb) time.Time {
			b.launched("a", "s", "p", "o1") // still running, or lost its ended
			second := b.launched("b", "s", "p", "o2")
			b.ended("b")
			return second
		}, map[Panel]int{sp: 1}},
		{"a hand launch, an unknown ship and another panel count toward nothing here", func(b *logb) time.Time {
			b.byHand("h", "s", "p", "o0")
			b.ended("h")
			b.ended("ghost") // no launched record: lost, or from before outcomes
			first := b.launched("a", "s", "p", "o1")
			b.ended("a")
			b.launched("c", "s", "other", "o3")
			b.ended("c")
			return first
		}, map[Panel]int{sp: 1, {"s", "other"}: 1}},
		{"a duplicate ended counts once", func(b *logb) time.Time {
			first := b.launched("a", "s", "p", "o1")
			b.ended("a")
			b.ended("a")
			return first
		}, map[Panel]int{sp: 1}},
		{"a reused ship name joins its latest earlier launched", func(b *logb) time.Time {
			first := b.launched("x", "s", "p", "o1")
			b.ended("x")
			b.launched("x", "t", "q", "o2")
			b.ended("x")
			b.launched("x", "s", "p", "o3")
			b.ended("x")
			return first
		}, map[Panel]int{sp: 2, {"t", "q"}: 1}},
		{"position beats ts: an ended before its launched counts nothing, and one after counts whatever its ts", func(b *logb) time.Time {
			b.ended("early") // the ship's name is launched only later, and never ends
			b.launched("early", "s", "p", "o1")
			late := b.launched("late", "s", "p", "o2")
			b.ended("late")
			b.log[len(b.log)-1].TS = late.Add(-time.Hour) // stamped before its launched
			return late
		}, map[Panel]int{sp: 1}},
		{"only runs after the panel's last retro_queued count, and Since is that retro's ts", func(b *logb) time.Time {
			b.launched("a", "s", "p", "o1")
			b.ended("a")
			b.launched("b", "s", "p", "o2") // launched before the retro, ended after it
			b.launched("c", "s", "p", "o3")
			retro := b.retro("s", "p", "r", "ro1")
			b.ended("b")
			b.ended("c")
			b.launched("d", "s", "p", "o4")
			b.ended("d")
			b.retro("s", "other", "r", "ro2") // another panel's retro resets nothing here
			return retro
		}, map[Panel]int{sp: 3}},
		{"a retro ship counts toward no panel: matched by order id on the resume source", func(b *logb) time.Time {
			b.launched("a", "s", "p", "o1")
			b.ended("a")
			b.retro("s", "p", "r", "ro1") // the order is added to r/q
			b.launched("retro-ship", "r", "q", "ro1")
			b.ended("retro-ship")
			b.launched("n", "r", "q", "ro9") // a real order of the resume panel
			b.ended("n")
			b.launched("m", "other", "q", "ro1") // same id, another source: a real run
			b.ended("m")
			return time.Time{}
		}, map[Panel]int{sp: 0, {"r", "q"}: 1, {"other", "q"}: 1}},
		{"an uncertain retro is matched by its text against the launched title", func(b *logb) time.Time {
			b.add("retro_queued", "", map[string]any{"source": "s", "panel": "p", "resume_source": "r", "resume_panel": "q",
				"text": "/retro s/p --since 2026-10-20T09:01:00Z", "uncertain": true})
			b.add("launched", "retro-ship", map[string]any{"source": "r", "panel": "q", "order_id": "whatever",
				"title": "/retro s/p --since 2026-10-20T09:01:00Z", "autopilot": true})
			b.ended("retro-ship")
			b.launched("n", "r", "q", "ro9")
			b.ended("n")
			return time.Time{}
		}, map[Panel]int{sp: 0, {"r", "q"}: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &logb{t: t}
			since := c.build(b)
			got := CountRuns(b.log)
			for panel, n := range c.want {
				if got[panel].Runs != n {
					t.Errorf("%v counted %d runs, want %d (all: %v)", panel, got[panel].Runs, n, got)
				}
			}
			for panel, n := range got {
				if _, ok := c.want[panel]; !ok && n.Runs != 0 {
					t.Errorf("%v counted %d runs, want none", panel, n.Runs)
				}
			}
			if !since.IsZero() && !got[sp].Since.Equal(since) {
				t.Errorf("Since = %v, want %v", got[sp].Since, since)
			}
		})
	}
}
