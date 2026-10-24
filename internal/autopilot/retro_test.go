package autopilot

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/sources"
)

// Retro orders (#117): every pass counts the autopilot runs in the outcome log
// and adds a /retro order to the resume panel. The interface these tests
// assume on Engine, beside the existing seams:
//
//	Record   func(outcomes.Record, func(error)) // was func(outcomes.Record); the func is Writer.Enqueue's onDone, nil for a record nobody waits for
//	Log      func() []outcomes.Line              // the outcome log, outcomes.Read of the home's outcomes directory
//	AddOrder func(ctx context.Context, source, panel, text string) (id string, err error)
//
// An AddOrder error may implement `AddKind() inbox.AddKind` (as
// sources.RequestError does); one that doesn't is final. The resume panel comes
// from Config().ResumeSource. config.PanelAutopilot.RetroEvery is the N: 0
// (the zero value) means off, so every other rig in this package stays retro-free.

type addCall struct{ source, panel, text string }

type heldWrite struct {
	rec  outcomes.Record
	done func(error)
}

type addFail struct {
	msg  string
	kind inbox.AddKind
}

func (e addFail) Error() string          { return e.msg }
func (e addFail) AddKind() inbox.AddKind { return e.kind }
func retryable(msg string) error         { return addFail{msg, inbox.AddRetryable} }
func finalRefusal(msg string) error      { return addFail{msg, inbox.AddFinal} }
func uncertainAdd(msg string) error      { return addFail{msg, inbox.AddUncertain} }
func (r *rig) stamp() time.Time {
	r.minutes++
	return r.now.Add(time.Duration(r.minutes) * time.Minute)
}
func isRetro(rec outcomes.Record) bool           { return rec.Event == "retro_queued" }
func (r *rig) retrosRecorded() []outcomes.Record { return r.recordsOf("retro_queued") }

// addOrder is a plugin that adds the order to its listing, like freeform.
func (r *rig) addOrder(_ context.Context, source, panelID, text string) (string, error) {
	r.adds = append(r.adds, addCall{source, panelID, text})
	if r.addErr != nil {
		if err := r.addErr(len(r.adds)); err != nil {
			return "", err
		}
	}
	id := fmt.Sprintf("ro%d", len(r.adds))
	for i := range r.src {
		for j := range r.src[i].Panels {
			if r.src[i].Key == source && r.src[i].Panels[j].ID == panelID {
				r.src[i].Panels[j].Orders = append(r.src[i].Panels[j].Orders, sources.Order{ID: id, Title: text})
			}
		}
	}
	return id, nil
}

// commit finishes a write: the line reaches the log only when err is nil, the
// way Writer.Enqueue's onDone gets the error of a failed write.
func (r *rig) commit(w heldWrite, err error) {
	if err == nil {
		fields := map[string]any{}
		for k, v := range w.rec.Fields {
			fields[k] = v
		}
		r.lines = append(r.lines, outcomes.Line{Event: w.rec.Event, Ship: w.rec.Ship, TS: r.stamp(), Fields: fields})
	}
	if w.done != nil {
		w.done(err)
	}
}

// confirm finishes the oldest held write.
func (r *rig) confirm(err error) {
	r.t.Helper()
	if len(r.held) == 0 {
		r.t.Fatal("no write is held")
	}
	w := r.held[0]
	r.held = r.held[1:]
	r.commit(w, err)
}

// run puts one finished autopilot run of s/p into the log and returns the ts
// of its launched record.
func (r *rig) run(ship string) time.Time { return r.runOf("s", "p", ship) }

func (r *rig) runOf(source, panelID, ship string) time.Time {
	ts := r.stamp()
	r.lines = append(r.lines, outcomes.Line{Event: "launched", Ship: ship, TS: ts, Fields: map[string]any{
		"source": source, "panel": panelID, "order_id": "o-" + ship, "title": "Title " + ship, "autopilot": true}})
	r.lines = append(r.lines, outcomes.Line{Event: "ended", Ship: ship, TS: r.stamp(), Fields: map[string]any{"exit": 0, "status": "sailed"}})
	return ts
}

func rfc(ts time.Time) string { return ts.UTC().Format(time.RFC3339) }

// newRetroRig is a rig whose panel s/p retros every n runs, with the resume
// panel rs/orders listed.
func newRetroRig(t *testing.T, n int) *rig {
	t.Helper()
	r := newRig(t)
	e := entry(5)
	e.RetroEvery = n
	r.cfg.SourceList[0].Autopilot["p"] = e
	r.cfg.ResumeSource = &config.ResumeSource{Source: "rs", Panel: "orders"}
	r.src = append(r.src, Source{Key: "rs", Listed: true, Panels: []sources.Panel{panel("orders")}})
	r.engine = r.newEngine()
	return r
}

func (r *rig) logged(substr string) int {
	n := 0
	for _, l := range r.logs {
		if strings.Contains(l, substr) {
			n++
		}
	}
	return n
}

func (r *rig) addTexts() []string {
	out := []string{}
	for _, a := range r.adds {
		if a.source != "rs" || a.panel != "orders" {
			r.t.Errorf("AddOrder(%q, %q), want the resume panel rs/orders", a.source, a.panel)
		}
		out = append(out, a.text)
	}
	return out
}

func wantTexts(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: orders added %q, want %q", what, got, want)
	}
}

// AC1 and the record: N counted runs add exactly one order, with the previous
// retro's ts as --since (the first counted run's launched ts for the first
// retro), and write a retro_queued record that carries what the order is.
func TestRetroAddsOneOrderForEveryNRuns(t *testing.T) {
	r := newRetroRig(t, 2)
	first := r.run("a")
	r.pass()
	wantTexts(t, "one run of two", r.addTexts())

	r.run("b")
	r.pass()
	text := "/retro s/p --since " + rfc(first)
	wantTexts(t, "two runs", r.addTexts(), text)
	recs := r.retrosRecorded()
	if len(recs) != 1 {
		t.Fatalf("%d retro_queued records, want 1", len(recs))
	}
	want := map[string]any{"source": "s", "panel": "p", "since": rfc(first), "text": text,
		"resume_source": "rs", "resume_panel": "orders", "order_id": "ro1"}
	if recs[0].Ship != "" || !reflect.DeepEqual(recs[0].Fields, want) {
		t.Errorf("retro_queued = ship %q %v, want a ship-less record %v", recs[0].Ship, recs[0].Fields, want)
	}

	// The order leaves the listing (its ship launched and went); the count starts
	// over, so a third run adds nothing and a fourth adds the next retro.
	r.src[1].Panels[0].Orders = nil
	r.run("c")
	r.pass()
	wantTexts(t, "one run after the retro", r.addTexts(), text)
	r.run("d")
	r.pass()
	wantTexts(t, "two runs after the retro", r.addTexts(), text, "/retro s/p --since "+rfc(r.lines[slices.IndexFunc(r.lines, func(l outcomes.Line) bool { return l.Event == "retro_queued" })].TS))
}

// The retro step is not part of the launch loop: a pass that returns early with
// the global cap full still adds the retro.
func TestRetroIsAddedWhenThePassReturnsEarly(t *testing.T) {
	r := newRetroRig(t, 1)
	r.cfg.AutopilotMaxShips = 1
	r.src[0].Panels = []sources.Panel{panel("p", "queued")}
	r.dirs["queued"] = "/w/queued"
	r.setShips(ship(ships.StatusUnderway, "elsewhere", "x", "y"))
	first := r.run("a")
	r.pass()
	if len(r.launches) != 0 {
		t.Fatalf("launched %v with the global cap full", r.launches)
	}
	wantTexts(t, "a full cap", r.addTexts(), "/retro s/p --since "+rfc(first))
}

// A panel that must add nothing adds nothing, and keeps its count in the log
// for later. Warnings are once, not once a pass.
func TestRetroAddsNothingWhenItIsOff(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(r *rig)
		on     func(r *rig) // makes the panel eligible again; nil when it never is
		logged string       // what the single warning mentions
	}{
		{"retro_every 0", func(r *rig) {
			e := r.cfg.SourceList[0].Autopilot["p"]
			e.RetroEvery = 0
			r.cfg.SourceList[0].Autopilot["p"] = e
		},
			func(r *rig) {
				e := r.cfg.SourceList[0].Autopilot["p"]
				e.RetroEvery = 2
				r.cfg.SourceList[0].Autopilot["p"] = e
			}, ""},
		{"autopilot disabled", func(r *rig) {
			e := r.cfg.SourceList[0].Autopilot["p"]
			e.Enabled = false
			r.cfg.SourceList[0].Autopilot["p"] = e
		},
			func(r *rig) {
				e := r.cfg.SourceList[0].Autopilot["p"]
				e.Enabled = true
				r.cfg.SourceList[0].Autopilot["p"] = e
			}, ""},
		{"paused", func(r *rig) { r.paused[[2]string{"s", "p"}] = true },
			func(r *rig) { delete(r.paused, [2]string{"s", "p"}) }, ""},
		{"no resume_source", func(r *rig) { r.cfg.ResumeSource = nil }, nil, "resume_source"},
		{"a panel id with whitespace", func(r *rig) {
			r.cfg.SourceList[0].Autopilot["my panel"] = r.cfg.SourceList[0].Autopilot["p"]
			delete(r.cfg.SourceList[0].Autopilot, "p")
			r.src[0].Panels = []sources.Panel{panel("my panel")}
		}, nil, "my panel"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRetroRig(t, 2)
			c.setup(r)
			panelID := "p"
			if strings.Contains(c.name, "whitespace") {
				panelID = "my panel"
			}
			first := r.runOf("s", panelID, "a")
			r.runOf("s", panelID, "b")
			r.pass()
			r.pass()
			wantTexts(t, c.name, r.addTexts())
			if len(r.retrosRecorded()) != 0 {
				t.Errorf("a retro_queued record was written")
			}
			if c.logged != "" && r.logged(c.logged) != 1 {
				t.Errorf("%d log lines mention %q over two passes, want 1: %q", r.logged(c.logged), c.logged, r.logs)
			}
			if c.on != nil {
				c.on(r)
				r.pass()
				wantTexts(t, c.name+", then on", r.addTexts(), "/retro s/p --since "+rfc(first))
			}
		})
	}
}

// One at a time: while the last retro's order is listed, the source has not
// listed yet, or a ship of it exists in any status, no second retro is added;
// once it clears, a count that reached N adds the next. A restart reads the same
// from the log.
func TestRetroOneAtATime(t *testing.T) {
	cases := []struct {
		name      string
		uncertain bool // the first add may have landed: the record has no order id
		pend      func(r *rig)
		clear     func(r *rig)
	}{
		{"the order is still listed", false, func(r *rig) {},
			func(r *rig) { r.src[1].Panels[0].Orders = nil }},
		{"the source has not listed yet", false,
			func(r *rig) { r.src[1].Panels[0].Orders = nil; r.src[1].Listed = false },
			func(r *rig) { r.src[1].Listed = true }},
		{"a ship has the order, anchored", false,
			func(r *rig) {
				r.src[1].Panels[0].Orders = nil
				r.setShips(ships.Ship{Session: "retro-ship", Source: "rs", OrderID: "ro1", Status: ships.StatusAnchored})
			},
			func(r *rig) { r.setShips() }},
		{"a ship has an uncertain retro's text for its title", true,
			func(r *rig) {
				r.setShips(ships.Ship{Session: "retro-ship", Source: "rs", Title: "/retro s/p --since TS", Status: ships.StatusUnderway})
			},
			func(r *rig) { r.setShips() }},
		{"Admiral restarted with the order listed", false,
			func(r *rig) { r.engine = r.newEngine() },
			func(r *rig) { r.src[1].Panels[0].Orders = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRetroRig(t, 2)
			if c.uncertain {
				r.addErr = func(int) error { return uncertainAdd("sent, no answer") }
			}
			first := r.run("a")
			r.run("b")
			r.pass()
			if len(r.adds) != 1 {
				t.Fatalf("the first retro made %d adds, want 1", len(r.adds))
			}
			r.addErr = nil
			retroTS := r.lines[slices.IndexFunc(r.lines, func(l outcomes.Line) bool { return l.Event == "retro_queued" })].TS
			if c.uncertain {
				// The ship's title is the text of the record.
				text := r.adds[0].text
				c.pend = func(r *rig) {
					r.setShips(ships.Ship{Session: "retro-ship", Source: "rs", Title: text, Status: ships.StatusUnderway})
				}
			}
			r.run("c")
			r.run("d")
			c.pend(r)
			r.pass()
			r.pass()
			if len(r.adds) != 1 {
				t.Fatalf("a second retro was added while the first was pending: %q", r.addTexts())
			}
			c.clear(r)
			r.pass()
			wantTexts(t, "once the first retro cleared", r.addTexts(),
				"/retro s/p --since "+rfc(first), "/retro s/p --since "+rfc(retroTS))
		})
	}
}

// The record is the reset: until retro_queued is confirmed written, the panel's
// retro is pending. A write that failed or was dropped is logged once and
// written again, and the order is not added again.
func TestRetroWaitsForItsRecordAndWritesItAgain(t *testing.T) {
	r := newRetroRig(t, 2)
	r.hold = true
	r.run("a")
	r.run("b")
	r.pass()
	r.run("c")
	r.run("d")
	r.pass()
	if len(r.adds) != 1 || len(r.held) != 1 {
		t.Fatalf("with the write unconfirmed: %d adds and %d writes, want 1 and 1", len(r.adds), len(r.held))
	}
	first := r.held[0].rec

	r.confirm(errors.New("disk full")) // the write failed
	r.pass()
	r.pass()
	if len(r.adds) != 1 {
		t.Errorf("the add was repeated after a lost record: %q", r.addTexts())
	}
	if len(r.held) != 1 || !isRetro(r.held[0].rec) || !reflect.DeepEqual(r.held[0].rec, first) {
		t.Fatalf("held writes after the failure = %v, want the same retro_queued record written again", r.held)
	}
	if n := r.logged("disk full"); n != 1 {
		t.Errorf("%d log lines for the lost record, want 1: %q", n, r.logs)
	}

	r.confirm(nil)
	r.pass()
	if len(r.adds) != 1 || len(r.held) != 0 {
		t.Errorf("after the record was written: %d adds, %d writes held, want 1 and 0", len(r.adds), len(r.held))
	}
}

// Errors of the add (D-no-resume-source is in the off table above).
func TestRetroAddFailures(t *testing.T) {
	t.Run("retryable is tried on every pass and logged once per message", func(t *testing.T) {
		r := newRetroRig(t, 2)
		r.addErr = func(n int) error {
			if n <= 2 {
				return retryable("source rs is not running yet")
			}
			return nil
		}
		first := r.run("a")
		r.run("b")
		r.pass()
		r.pass()
		if len(r.adds) != 2 || len(r.retrosRecorded()) != 0 {
			t.Fatalf("after two refusals: %d adds, %d records, want 2 and 0", len(r.adds), len(r.retrosRecorded()))
		}
		r.pass()
		if len(r.adds) != 3 || len(r.retrosRecorded()) != 1 {
			t.Errorf("after the third pass: %d adds, %d records, want 3 and 1", len(r.adds), len(r.retrosRecorded()))
		}
		if n := r.logged("source rs is not running yet"); n != 1 {
			t.Errorf("%d log lines for the same refusal, want 1: %q", n, r.logs)
		}
		if got := r.adds[2].text; got != "/retro s/p --since "+rfc(first) {
			t.Errorf("the order added = %q", got)
		}
	})
	t.Run("a final refusal is logged once and tried again on a later pass", func(t *testing.T) {
		r := newRetroRig(t, 2)
		r.addErr = func(int) error { return finalRefusal("the source refused the order") }
		r.run("a")
		r.run("b")
		r.pass()
		r.pass()
		if len(r.adds) < 2 || len(r.retrosRecorded()) != 0 {
			t.Errorf("%d adds and %d records after two refusals, want a retry and no record", len(r.adds), len(r.retrosRecorded()))
		}
		if n := r.logged("the source refused the order"); n != 1 {
			t.Errorf("%d log lines for the same refusal, want 1: %q", n, r.logs)
		}
	})
	t.Run("an uncertain add is recorded without an id and never repeated", func(t *testing.T) {
		r := newRetroRig(t, 2)
		r.addErr = func(int) error { return uncertainAdd("sent, no answer") }
		first := r.run("a")
		r.run("b")
		r.pass()
		recs := r.retrosRecorded()
		if len(recs) != 1 {
			t.Fatalf("%d retro_queued records, want 1", len(recs))
		}
		f := recs[0].Fields
		if _, has := f["order_id"]; has || f["uncertain"] != true || f["text"] != "/retro s/p --since "+rfc(first) {
			t.Errorf("record fields = %v, want uncertain true, no order_id and the text", f)
		}
		r.src[1].Panels[0].Orders = nil
		r.run("c")
		r.run("d")
		r.pass()
		r.pass()
		if len(r.adds) != 2 {
			t.Errorf("%d adds: the uncertain retro was repeated, or the next one is missing; want the first and the next", len(r.adds))
		}
	})
}
