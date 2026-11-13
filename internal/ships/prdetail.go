package ships

import (
	"net/http"
	"time"
)

// detailTimeout bounds one detail read of GitHub.
var detailTimeout = 15 * time.Second

func (s *Service) servePRDetail(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{})
}
