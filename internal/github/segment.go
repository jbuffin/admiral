package github

import "regexp"

var segmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ValidRepo says whether s is <owner>/<name>: exactly two segments, each of [A-Za-z0-9_.-]+ and neither "." nor "..".
func ValidRepo(s string) bool {
	for i := range s {
		if s[i] == '/' {
			return ValidSegment(s[:i]) && ValidSegment(s[i+1:])
		}
	}
	return false
}

// ValidSegment says whether s is one owner or repo segment of a GitHub endpoint.
func ValidSegment(s string) bool {
	return s != "." && s != ".." && segmentPattern.MatchString(s)
}
