package shiptitle

import (
	"net/url"
	"regexp"
	"strings"
)

const maxLinks = 10

var linkRE = regexp.MustCompile(`(?i)https?://[^\s<>"'` + "`" + `\]\[(){}]+`)

// Links are the distinct http(s) links in text, in the order they first appear, without the
// punctuation that ends a sentence around them.
func Links(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range linkRE.FindAllString(text, -1) {
		m = strings.TrimRight(m, ".,;:!?*_~")
		if u, err := url.Parse(m); err != nil || u.Host == "" || len(m) > 2048 || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
		if len(out) == maxLinks {
			break
		}
	}
	return out
}
