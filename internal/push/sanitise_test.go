package push_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/push"
)

const stripped = "\u0000\u0007\u001b\u007f\u0085\u009f  ‎‏‪‫‬‭‮⁦⁧⁨⁩"

func TestPayloadStripsControlAndBidiCharactersFromNameLineAndFleetName(t *testing.T) {
	t.Parallel()
	for _, r := range stripped {
		c := string(r)
		p := push.PayloadOf(push.Event{Kind: push.ShipQuestion, Name: "na" + c + "me", FleetName: "fl" + c + "eet", Text: "li" + c + "ne"})
		if p.Name != "name" || p.FleetName != "fleet" || p.Line != "line" {
			t.Errorf("U+%04X: name %q fleet_name %q line %q, want %q %q %q", r, p.Name, p.FleetName, p.Line, "name", "fleet", "line")
		}
	}
}

func TestPayloadStillCutsTheLineAtANewlineAfterStripping(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"first\r\nsecond":       "first",
		"first\nsecond":         "first",
		"first second":     "firstsecond",
		"‮evil‬ ask?": "evil ask?",
	} {
		if got := push.PayloadOf(push.Event{Kind: push.ShipQuestion, Text: in}).Line; got != want {
			t.Errorf("line of %q = %q, want %q", in, got, want)
		}
	}
}

func TestPayloadKeepsTheLineAtMost120RunesWhenControlsAreStrippedFirst(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("a‮", 200)
	line := push.PayloadOf(push.Event{Kind: push.ShipQuestion, Text: text}).Line
	if n := utf8.RuneCountInString(line); n != 120 || !strings.HasSuffix(line, "…") || strings.Contains(line, "‮") {
		t.Fatalf("line %q (%d runes), want 119 letters and an ellipsis with no bidi mark", line, n)
	}
}
