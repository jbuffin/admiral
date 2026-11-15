package ships

import "net/http"

// Compile stub for the red phase: the route answers 501 and nothing advances.
func (s *Service) serveHandoff(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Service) advanceHandoffs() {}
