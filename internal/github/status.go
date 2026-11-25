package github

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// statusPattern finds the status in gh's "gh: Not Found (HTTP 404)", the fake's "gh: HTTP 404" and "HTTP 422: Validation Failed".
var statusPattern = regexp.MustCompile(`HTTP (\d{3})\b`)

// statusError is a failed REST call whose stderr named an HTTP status.
type statusError struct {
	msg    string
	status int
}

func (e *statusError) Error() string { return e.msg }

// HTTPStatus is the HTTP status gh reported for a failed REST call, or 0 when err carries none.
func HTTPStatus(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.status
	}
	return 0
}

// RateLimited says whether err is GitHub refusing the call for the account's rate limit.
func RateLimited(err error) bool {
	switch HTTPStatus(err) {
	case 429:
		return true
	case 403:
		return strings.Contains(strings.ToLower(err.Error()), "rate limit")
	}
	return false
}

func statusOf(stderr string) int {
	m := statusPattern.FindStringSubmatch(stderr)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
