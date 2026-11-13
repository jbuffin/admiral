package routines_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

func TestStandingOrdersSentencesNameTheRoutineAScopedSkipBelongsTo(t *testing.T) {
	r, rt := muteRig(t)
	skip := `{"senders":[{"value":"mallory","routine":"` + rt.ID + `"},{"value":"eve","routine":"gone-routine"}]}`
	r.setLimits("work", config.FleetFields{Limits: map[string]json.RawMessage{"skip": json.RawMessage(skip)}})

	code, body := r.do("GET", "/api/fleets/work/standing-orders", nil)
	if code != 200 {
		t.Fatalf("GET standing-orders = %d %v", code, body)
	}
	var got string
	for _, s := range body["sentences"].([]any) {
		m := s.(map[string]any)
		if m["field"] == "skip.senders" {
			got = m["sentence"].(string)
		}
	}
	for _, want := range []string{"mallory (" + rt.Name + " only)", "eve (a deleted routine only)"} {
		if !strings.Contains(got, want) {
			t.Errorf("skip.senders sentence = %q, want it to hold %q", got, want)
		}
	}
	for _, not := range []string{rt.ID, "gone-routine"} {
		if strings.Contains(got, not) {
			t.Errorf("skip.senders sentence = %q, want no routine ID %q", got, not)
		}
	}
}
