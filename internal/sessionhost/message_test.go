package sessionhost

import "testing"

func TestCleanMessage(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"CRLF and lone CR become LF":      {"a\r\nb\rc", "a\nb\nc"},
		"C0 controls go, tab and LF stay": {"a\x07b\x1b[31m\tc\nd\x00", "ab[31m\tc\nd"},
		"C1 controls and DEL go":          {"a\x7fb\u0085c\u009fd", "abcd"},
		"the edges are trimmed":           {" \n hi \t\n", "hi"},
		"controls only is empty":          {"\x07\x1b\x00", ""},
	} {
		if got := CleanMessage(c.in); got != c.want {
			t.Errorf("%s: CleanMessage(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}
