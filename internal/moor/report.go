// Package moor decides whether a ship that exited cleanly moors, and keeps the
// record of one that did.
package moor

import (
	"encoding/json"
	"regexp"
)

// maxReport is the largest report the ships package reads; a parser handed more reads none.
const maxReport = 1 << 20

// Report is what Admiral takes from a ship's report file.
type Report struct {
	PRs    []string
	Resume string
}

// A PR URL on any host, since the client's host decides which counts.
var prURL = regexp.MustCompile(`^https://([^/\s]+)/([^/\s]+)/([^/\s]+)/pull/[0-9]+$`)

// ParseReport reads versions 1 (prs are URL strings) and 2 (prs are objects with a url).
// A report it cannot use gives no PRs and no resume text.
func ParseReport(raw []byte) Report {
	if len(raw) > maxReport {
		return Report{}
	}
	var head struct {
		Version int             `json:"version"`
		PRs     json.RawMessage `json:"prs"`
		Resume  string          `json:"resume"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return Report{}
	}
	var urls []string
	switch head.Version {
	case 1:
		if len(head.PRs) > 0 && json.Unmarshal(head.PRs, &urls) != nil {
			return Report{}
		}
	case 2:
		var objs []struct {
			URL string `json:"url"`
		}
		if len(head.PRs) > 0 && json.Unmarshal(head.PRs, &objs) != nil {
			return Report{}
		}
		for _, o := range objs {
			urls = append(urls, o.URL)
		}
	default:
		return Report{}
	}
	for _, u := range urls {
		if !prURL.MatchString(u) {
			return Report{}
		}
	}
	return Report{PRs: urls, Resume: head.Resume}
}
