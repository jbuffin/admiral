package crewapi

import "net/http"

// publishRoutes registers the publish and Unpublish routes.
func (s *Service) publishRoutes(mux *http.ServeMux) {
	for _, pattern := range []string{
		"GET /api/fleets/{key}/crew/sharing",
		"POST /api/fleets/{key}/crew/sharing/repo",
		"POST /api/fleets/{key}/crew/members/{name}/share/preview",
		"POST /api/fleets/{key}/crew/members/{name}/share",
		"GET /api/fleets/{key}/crew/published/{name}",
		"DELETE /api/fleets/{key}/crew/published/{name}",
	} {
		mux.HandleFunc(pattern, notBuilt)
	}
}

func notBuilt(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "not built yet")
}
