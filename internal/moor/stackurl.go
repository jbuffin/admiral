package moor

import (
	"regexp"
	"strconv"
)

// A stack URL on any host, since the client's host decides which counts. Strict, because its parts build a gh api path.
var stackURL = regexp.MustCompile(`^https://([^/\s?#%]+)/([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)/stack/([1-9][0-9]*)$`)

// ParseStack reads https://<host>/<owner>/<repo>/stack/<N> strictly; ok is false for anything else.
func ParseStack(url string) (host, owner, repo string, number int, ok bool) {
	m := stackURL.FindStringSubmatch(url)
	if m == nil || m[3] == "." || m[3] == ".." {
		return "", "", "", 0, false
	}
	n, err := strconv.ParseInt(m[4], 10, 32)
	if err != nil {
		return "", "", "", 0, false
	}
	return m[1], m[2], m[3], int(n), true
}
