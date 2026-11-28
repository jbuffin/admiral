package sessionhost

import (
	"strings"
	"testing"
)

func TestOnlyAnIdNewStopIDCouldMakeIsAStopID(t *testing.T) {
	t.Parallel()
	if id := NewStopID(); !ValidStopID(id) {
		t.Errorf("NewStopID() = %q, which ValidStopID refuses", id)
	}
	for name, s := range map[string]string{"empty": "", "short": "abc123", "upper case": "ABCDEF0123456789", "not hex": "zzzzzzzzzzzzzzzz",
		"a line break": "0123456789abcde\n", "too long": "0123456789abcdef0", "a space": "0123456789abcde "} {
		if ValidStopID(s) {
			t.Errorf("ValidStopID(%s %q) = true, want false", name, s)
		}
	}
}

func TestAQuotedTmuxArgumentHoldsNoLineBreak(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"a\nb", "a\rb", "a\r\nset-option x y"} {
		if got := tmuxQuote(s); strings.ContainsAny(got, "\r\n") {
			t.Errorf("tmuxQuote(%q) = %q, want no raw line break that could end the command line", s, got)
		}
	}
}
