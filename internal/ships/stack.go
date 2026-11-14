package ships

import "net/http"

func (s *Service) serveStackShape(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "stack shape is not built yet")
}

func (s *Service) serveStackLink(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "stack link is not built yet")
}
