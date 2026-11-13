package config_test

import (
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/standing"
)

func senderSentence(t *testing.T, sentences []string) string {
	t.Helper()
	for _, s := range sentences {
		if strings.HasPrefix(s, "Events from these senders are skipped:") {
			return s
		}
	}
	t.Fatalf("no sender skip sentence in %q", sentences)
	return ""
}

func TestTheFleetViewNamesTheRoutineAScopedSkipBelongsTo(t *testing.T) {
	limits := `{"skip":{"senders":[{"value":"mallory","routine":"r-live"},{"value":"eve","routine":"r-gone"},{"value":"trent"}]}}`
	m, _ := openView(t, limitsFleet("", limits))
	m.RoutineNamer = func(fleet string) standing.RoutineNamer {
		return func(id string) (string, bool) { return "Review watch", fleet == "work" && id == "r-live" }
	}
	f := fleetOf(m.View(), "work")
	texts := []string{}
	for _, s := range f.Sentences {
		texts = append(texts, s.Sentence)
	}
	got := senderSentence(t, texts)
	for _, want := range []string{"mallory (Review watch only)", "eve (a deleted routine only)", "trent"} {
		if !strings.Contains(got, want) {
			t.Errorf("sentence = %q, want it to hold %q", got, want)
		}
	}
	for _, not := range []string{"r-live", "r-gone"} {
		if strings.Contains(got, not) {
			t.Errorf("sentence = %q, want no routine ID %q", got, not)
		}
	}
}
