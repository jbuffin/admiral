package ships

import (
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
)

func TestAShipTheSchedulerLaunchesForAnEventKeepsTheEventsURL(t *testing.T) {
	r := newWakeRig(t, 0)
	const url = "https://github.com/acme/api/pull/7"
	got := r.mustLaunch(t, func(q *launcher.Request) { q.RoutineKey, q.EventURL = "routine:r1", url })
	if got.URL != url {
		t.Errorf("launched ship url = %q, want the event's %q", got.URL, url)
	}
	waitUntil(t, "the listed ship carries the event URL", func() bool {
		for _, s := range r.s.List() {
			if s.Session == got.Session {
				return s.URL == url
			}
		}
		return false
	})
}

func TestAClientLaunchWithNoSourceStillHasItsTitleAndURLCleared(t *testing.T) {
	r := newWakeRig(t, 0)
	got := r.mustLaunch(t, func(q *launcher.Request) { q.Title, q.URL = "A title", "https://github.com/acme/api/pull/9" })
	if got.Title != "" || got.URL != "" {
		t.Errorf("launched ship title %q, url %q, want both empty: a client sets neither", got.Title, got.URL)
	}
}
