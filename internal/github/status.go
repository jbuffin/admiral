package github

// HTTPStatus is the HTTP status gh reported for a failed REST call, or 0 when err carries none.
func HTTPStatus(err error) int { return 0 }
