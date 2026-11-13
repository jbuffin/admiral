package conversation

import "regexp"

// The PR URL's owner and repository exclude the characters that wrap a URL in prose or a log.
var prURLInText = regexp.MustCompile(`https://[^/\s"'<>()\[\]\\]+/[^/\s"'<>()\[\]\\]+/[^/\s"'<>()\[\]\\]+/pull/[0-9]+`)

// PRCandidates returns the pull request URLs printed in the tool results of items, in order of first appearance.
func PRCandidates(items []Item) []string {
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		if it.Tool == nil {
			continue
		}
		for _, u := range prURLInText.FindAllString(it.Tool.Output, -1) {
			if !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	return out
}
