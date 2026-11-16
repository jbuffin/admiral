package inbox

import "net/http"

func (s *Service) serveMission(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}
