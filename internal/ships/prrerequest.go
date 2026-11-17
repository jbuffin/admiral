package ships

import "net/http"

// serveReRequest is a stub: the route answers 501 until it is built.
func (s *Service) serveReRequest(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}
