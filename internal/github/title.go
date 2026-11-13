package github

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var itemURL = regexp.MustCompile(`^https://([^/\s]+)/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/(pull|issues)/([0-9]+)(?:[/?#][^\s]*)?$`)

// ItemURL is the canonical URL of the pull request or issue u points into, on host, such as
// a PR's files tab; ok is false for any other URL.
func ItemURL(u, host string) (canonical string, ok bool) {
	m := itemURL.FindStringSubmatch(strings.TrimSpace(u))
	if m == nil || !strings.EqualFold(m[1], host) {
		return "", false
	}
	return "https://" + strings.ToLower(m[1]) + "/" + m[2] + "/" + m[3] + "/" + m[4] + "/" + m[5], true
}

// ItemTitle is the title of the pull request or issue at a canonical ItemURL. found is false
// when GitHub has no such item, or the token cannot see it.
func (c *Client) ItemTitle(ctx context.Context, url string) (title string, found bool, err error) {
	if _, ok := ItemURL(url, c.host); !ok {
		return "", false, fmt.Errorf("%s is not a pull request or issue on %s", url, c.host)
	}
	q := fmt.Sprintf("query { r: resource(url: %q) { ... on Issue { title } ... on PullRequest { title } } }", url)
	data, notFound, gone, err := c.batchQuery(ctx, "title", q, map[string]string{"r": url})
	if err != nil || len(notFound) > 0 || len(gone) > 0 {
		return "", false, err
	}
	var r *struct{ Title string }
	if json.Unmarshal(data["r"], &r) != nil || r == nil || strings.TrimSpace(r.Title) == "" {
		return "", false, nil
	}
	return strings.TrimSpace(r.Title), true, nil
}
