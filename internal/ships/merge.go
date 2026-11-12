package ships

import "net/http"

func (s *Service) serveMerge(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "merge is not implemented")
}
