package routines

import "net/http"

// serveMute adds a skip-filter entry scoped to one routine: by sender or by dedupe_key.
func (s *Service) serveMute(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}
