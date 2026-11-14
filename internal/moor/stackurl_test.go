package moor

import "testing"

func TestParseStackReadsAStackURLStrictly(t *testing.T) {
	host, owner, repo, n, ok := ParseStack("https://github.com/acme/widgets/stack/12")
	if !ok || host != "github.com" || owner != "acme" || repo != "widgets" || n != 12 {
		t.Errorf("ParseStack = %q %q %q %d %v, want github.com acme widgets 12 true", host, owner, repo, n, ok)
	}
	if _, o, r, n, ok := ParseStack("https://ghe.example.com/a-b/my.repo_x-1/stack/3"); !ok || o != "a-b" || r != "my.repo_x-1" || n != 3 {
		t.Errorf("ParseStack of an enterprise URL = %q %q %d %v, want a-b my.repo_x-1 3 true", o, r, n, ok)
	}
	for name, u := range map[string]string{
		"leading zero":       "https://github.com/acme/widgets/stack/012",
		"zero":               "https://github.com/acme/widgets/stack/0",
		"extra path":         "https://github.com/acme/widgets/stack/12/files",
		"trailing slash":     "https://github.com/acme/widgets/stack/12/",
		"dot repo":           "https://github.com/acme/./stack/12",
		"dotdot repo":        "https://github.com/acme/../stack/12",
		"percent-escape":     "https://github.com/acme/wid%67ets/stack/12",
		"escaped dotdot":     "https://github.com/acme/%2e%2e/stack/12",
		"query":              "https://github.com/acme/widgets/stack/12?x=1",
		"fragment":           "https://github.com/acme/widgets/stack/12#top",
		"owner with a dot":   "https://github.com/ac.me/widgets/stack/12",
		"a pull URL":         "https://github.com/acme/widgets/pull/12",
		"http":               "http://github.com/acme/widgets/stack/12",
		"not a number":       "https://github.com/acme/widgets/stack/x",
		"too large a number": "https://github.com/acme/widgets/stack/99999999999999999999",
		"empty":              "",
	} {
		if host, owner, repo, n, ok := ParseStack(u); ok {
			t.Errorf("ParseStack(%s) = %q %q %q %d true, want refused", name, host, owner, repo, n)
		}
	}
}
