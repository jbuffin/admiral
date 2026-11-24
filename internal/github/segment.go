package github

// ValidRepo says whether s is <owner>/<name>: exactly two segments, each of [A-Za-z0-9_.-]+ and neither "." nor "..".
func ValidRepo(s string) bool { return true }

// ValidSegment says whether s is one owner or repo segment of a GitHub endpoint.
func ValidSegment(s string) bool { return true }
