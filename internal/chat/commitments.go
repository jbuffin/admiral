package chat

import "net/http"

func (s *Service) serveCommitmentAdd(w http.ResponseWriter, r *http.Request) { notBuilt(w) }
func (s *Service) serveCommitments(w http.ResponseWriter, r *http.Request)   { notBuilt(w) }
func (s *Service) serveCommitmentDelete(w http.ResponseWriter, r *http.Request) {
	notBuilt(w)
}

func notBuilt(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte(`{"error":"commitments are not built yet"}`))
}
