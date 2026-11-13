// Package moor decides whether a ship that exited cleanly moors, and keeps the
// record of one that did.
package moor

import (
	"encoding/json"
	"regexp"
	"strings"
)

// maxReport is the largest report the ships package reads; a parser handed more reads none.
const maxReport = 1 << 20

// Report is what Admiral takes from a ship's report file.
type Report struct {
	PRs    []string
	Resume string
	// Result is the report's own verdict: done, partial or failed. Version 1 has none, and so does anything else.
	Result string
}

// A PR URL on any host, since the client's host decides which counts.
var prURL = regexp.MustCompile(`^https://([^/\s]+)/([^/\s]+)/([^/\s]+)/pull/([0-9]+)$`)

// A resume text is the wake's prompt, so it is only a skill's resume invocation and nothing a ship could append to.
var resumeText = regexp.MustCompile(`^[/$][A-Za-z0-9][A-Za-z0-9._-]*(:[A-Za-z0-9][A-Za-z0-9._-]*)? resume [A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidResume says text has the shape of the report schema's resume: `/skill resume <run>` or `$skill resume <run>`.
func ValidResume(text string) bool {
	return resumeText.MatchString(text) && !strings.Contains(text, "..")
}

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
		Result  any             `json:"result"`
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
	if !ValidResume(head.Resume) {
		head.Resume = ""
	}
	var result string
	if head.Version == 2 {
		if r, ok := head.Result.(string); ok && (r == "done" || r == "partial" || r == "failed") {
			result = r
		}
	}
	return Report{PRs: urls, Resume: head.Resume, Result: result}
}
