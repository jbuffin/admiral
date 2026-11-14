package moor

// ParseStack reads https://<host>/<owner>/<repo>/stack/<N> strictly; ok is false for anything else.
func ParseStack(url string) (host, owner, repo string, number int, ok bool) {
	return "", "", "", 0, false
}
