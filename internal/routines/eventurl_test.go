package routines_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

const prURL = "https://github.com/acme/api/pull/7"

func TestAnEventRoutineSavesWithNoText(t *testing.T) {
	r := newRig(t)
	r.catalog()
	in := validOn()
	in.Do.Launch.Text = "  \n"
	got := r.create("work", in)
	if got.Do.Launch == nil || got.On == nil {
		t.Fatalf("saved routine = %+v, want an event routine with its launch", got)
	}
}

func TestAScheduleRoutineWithNoTextIsRefusedNamingText(t *testing.T) {
	r := newRig(t)
	in := launchRoutine()
	in.Do.Launch.Text = " "
	_, err := r.store.Create("work", in)
	if err == nil || !strings.HasPrefix(err.Error(), "invalid routine: Text") {
		t.Fatalf("Create = %v, want an invalid routine error that starts with Text", err)
	}
}

func TestEachMissingLaunchFieldIsRefusedByItsOwnNameInFormOrder(t *testing.T) {
	cases := []struct {
		name  string
		on    bool
		mod   func(*routines.Launch)
		field string
	}{
		{"no dock, checkout or text", false, func(l *routines.Launch) { l.Harness, l.Dir, l.Text = "", "", "" }, "Dock"},
		{"no checkout or text", false, func(l *routines.Launch) { l.Dir, l.Text = "", "" }, "Checkout"},
		{"no checkout", false, func(l *routines.Launch) { l.Dir = "  " }, "Checkout"},
		{"no text", false, func(l *routines.Launch) { l.Text = "" }, "Text"},
		{"an event routine with no dock", true, func(l *routines.Launch) { l.Harness, l.Text = "", "" }, "Dock"},
		{"an event routine with no checkout", true, func(l *routines.Launch) { l.Dir, l.Text = "", "" }, "Checkout"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.catalog()
			in := launchRoutine()
			if c.on {
				in = validOn()
			}
			c.mod(in.Do.Launch)
			_, err := r.store.Create("work", in)
			if err == nil || !strings.HasPrefix(err.Error(), "invalid routine: "+c.field) {
				t.Fatalf("Create = %v, want an invalid routine error that starts with %q", err, c.field)
			}
			for _, other := range []string{"Dock", "Checkout", "Text"} {
				if other != c.field && strings.Contains(err.Error(), other) {
					t.Errorf("error %q names %s too, want only %s", err, other, c.field)
				}
			}
		})
	}
}

func TestAnEventLaunchHandsTheShipsSeamTheEventsPRorIssueURLAndTheTextUnchanged(t *testing.T) {
	for _, ref := range []string{prURL, "https://github.com/acme/api/issues/12"} {
		t.Run(ref, func(t *testing.T) {
			h := newHarness(t)
			h.withEvents(standing.PresetLimits(standing.Home))
			h.onRoutine(routines.Act, nil)
			ev := evFrom("Eng")
			ev.Ref = ref
			h.sched.HandleEvent(ev)
			h.wantLaunches(1)
			req := h.ships.requests()[0]
			if req.EventURL != ref || req.Template != "sweep the board" {
				t.Errorf("request event URL %q, template %q, want %q and the routine's Text", req.EventURL, req.Template, ref)
			}
		})
	}
}

func TestAnEventLaunchOfAnEventRoutineWithNoTextStartsFromTheURL(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	h.onRoutine(routines.Act, func(in *routines.Routine) { in.Do.Launch.Text = "" })
	h.sched.HandleEvent(evFrom("Eng"))
	h.wantLaunches(1)
	if req := h.ships.requests()[0]; req.EventURL != prURL || strings.TrimSpace(req.Template) != "" {
		t.Errorf("request event URL %q, template %q, want the PR and no template", req.EventURL, req.Template)
	}
}

func TestARefThatIsNotAGitHubPRorIssueGivesTheLaunchNoEventURL(t *testing.T) {
	refs := []string{
		"ENG-9",
		"https://tracker.example.com/acme/api/pull/7",
		"https://github.com.evil.example/acme/api/pull/7",
		"https://evil.example/?u=https://github.com/acme/api/pull/7",
		"https://github.com/acme/api/discussions/3",
		"https://github.com/acme/api/pull/seven",
	}
	for _, ref := range refs {
		t.Run(ref, func(t *testing.T) {
			h := newHarness(t)
			h.withEvents(standing.PresetLimits(standing.Home))
			h.onRoutine(routines.Act, nil)
			ev := evFrom("Eng")
			ev.Ref = ref
			h.sched.HandleEvent(ev)
			h.wantLaunches(1)
			req := h.ships.requests()[0]
			if req.EventURL != "" || req.Template != "sweep the board" {
				t.Errorf("request event URL %q, template %q, want no URL and the routine's Text alone", req.EventURL, req.Template)
			}
		})
	}
}

func TestAnEventLaunchWithNoURLAndNoTextFailsAndStartsNoShip(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.Act, func(in *routines.Routine) { in.Do.Launch.Text = "" })
	ev := evFrom("Eng")
	ev.Ref = "ENG-9"
	h.sched.HandleEvent(ev)
	h.wantLaunches(0)
	rec := h.latest(r)
	reason := strings.ToLower(rec.Reason)
	if rec.Outcome != routines.Failed || !strings.Contains(reason, "no pr or issue link") || !strings.Contains(reason, "no text") {
		t.Errorf("record = %+v, want a failure whose reason says the event had no PR or issue link and the routine has no Text", rec)
	}
}

// proposedOn has an Ask first event routine with text make one open proposal for ref.
func proposedOn(t *testing.T, text, ref string) (*harness, routines.Routine, routines.Trigger) {
	t.Helper()
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.AskFirst, func(in *routines.Routine) { in.Do.Launch.Text = text })
	ev := evFrom("Eng")
	ev.Ref = ref
	h.sched.HandleEvent(ev)
	rec := h.latest(r)
	if rec.Outcome != routines.Proposed {
		t.Fatalf("record = %+v, want a proposal", rec)
	}
	return h, r, rec
}

func TestAProposalLaunchPutsTheEventURLBeforeTheTextAndOnTheRequest(t *testing.T) {
	cases := []struct{ name, text, ref, wantText, wantURL string }{
		{"text and a PR", "sweep the board", prURL, prURL + "\n\nsweep the board", prURL},
		{"no text and a PR", "", prURL, prURL, prURL},
		{"text and a plugin ref", "sweep the board", "ENG-9", "sweep the board", ""},
		{"text and a non-GitHub link", "sweep the board", "https://tracker.example.com/t/9", "sweep the board", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, r, rec := proposedOn(t, c.text, c.ref)
			if _, err := h.sched.LaunchProposed(context.Background(), "work", r.ID, rec.ID); err != nil {
				t.Fatalf("LaunchProposed = %v", err)
			}
			h.wantLaunches(1)
			req := h.ships.requests()[0]
			if req.Text != c.wantText || req.EventURL != c.wantURL || req.EventText != "" {
				t.Errorf("request text %q, event URL %q, event text %q, want text %q, URL %q and no event file", req.Text, req.EventURL, req.EventText, c.wantText, c.wantURL)
			}
		})
	}
}

func TestAProposalLaunchWithNoURLAndNoTextFailsAndStartsNoShip(t *testing.T) {
	h, r, rec := proposedOn(t, "", "ENG-9")
	if _, err := h.sched.LaunchProposed(context.Background(), "work", r.ID, rec.ID); err == nil {
		t.Fatal("LaunchProposed succeeded with no event link and no Text")
	}
	h.wantLaunches(0)
	got := h.latest(r)
	reason := strings.ToLower(got.Reason)
	if got.Outcome != routines.Failed || !strings.Contains(reason, "no pr or issue link") || !strings.Contains(reason, "no text") {
		t.Errorf("record = %+v, want a failure whose reason says the event had no PR or issue link and the routine has no Text", got)
	}
}

func TestAnEventOnAConfiguredEnterpriseHostGetsItsURLAndOtherHostsDoNot(t *testing.T) {
	const ghe = "https://ghe.example.com/acme/api/pull/7"
	for ref, want := range map[string]string{ghe: ghe, "https://other.example.com/acme/api/pull/7": ""} {
		h := newHarness(t)
		h.sched.GitHubHosts = func(string) []string { return []string{"github.com", "ghe.example.com"} }
		h.withEvents(standing.PresetLimits(standing.Home))
		h.onRoutine(routines.Act, nil)
		ev := evFrom("Eng")
		ev.Ref = ref
		h.sched.HandleEvent(ev)
		h.wantLaunches(1)
		if got := h.ships.requests()[0].EventURL; got != want {
			t.Errorf("ref %q: event URL %q, want %q", ref, got, want)
		}
	}
}

func TestAnEventLaunchTitlesTheShipFromTheURLThenTheText(t *testing.T) {
	for _, c := range []struct{ text, want string }{{"", prURL}, {"sweep the board", prURL + "\n\nsweep the board"}} {
		h := newHarness(t)
		h.withEvents(standing.PresetLimits(standing.Home))
		h.onRoutine(routines.Act, func(in *routines.Routine) { in.Do.Launch.Text = c.text })
		h.sched.HandleEvent(evFrom("Eng"))
		h.wantLaunches(1)
		if got := h.ships.requests()[0].Text; got != c.want {
			t.Errorf("text %q: request Text %q, want %q", c.text, got, c.want)
		}
	}
}

func TestAcceptRefusesAProposalOfARoutineThatBecameAnEventRoutine(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r, trig := h.proposed(nil)
	in := validOn()
	in.Do.Launch.Text = ""
	if _, err := h.store.Update("work", r.ID, in); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := h.sched.Accept(context.Background(), "work", r.ID, trig.ID); !errors.Is(err, routines.ErrNotIssued) {
		t.Fatalf("Accept = %v, want ErrNotIssued", err)
	}
	h.wantLaunches(0)
}
