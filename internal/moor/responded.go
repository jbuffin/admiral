package moor

import "strings"

// Responded says the PR's author answered the viewer's review: the head moved past the viewer's latest review
// (GitHub doesn't say who pushed, so any new head counts), or the author commented in a review thread the viewer
// commented in, after the viewer's last comment there. The poll's review and thread limits can hide the viewer's
// side of a long or old thread, which then reads as not responded.
func Responded(st Status) bool {
	if st.Viewer == "" || st.Author == "" {
		return false
	}
	var latest *Review
	for i := range st.Reviews {
		r := &st.Reviews[i]
		if !isViewer(r.Author, st.Viewer) || r.State == "PENDING" || r.State == "DISMISSED" {
			continue
		}
		if latest == nil || r.SubmittedAt.After(latest.SubmittedAt) {
			latest = r
		}
	}
	if latest != nil && latest.Commit != "" && st.Head != "" && latest.Commit != st.Head {
		return true
	}
	mine := map[string]Comment{}
	for _, c := range st.ThreadComments {
		if c.Thread == "" || !isViewer(c.Author, st.Viewer) {
			continue
		}
		if last, ok := mine[c.Thread]; !ok || c.CreatedAt.After(last.CreatedAt) {
			mine[c.Thread] = c
		}
	}
	for _, c := range st.ThreadComments {
		if last, ok := mine[c.Thread]; ok && strings.EqualFold(c.Author.Login, st.Author) && c.CreatedAt.After(last.CreatedAt) {
			return true
		}
	}
	return false
}
