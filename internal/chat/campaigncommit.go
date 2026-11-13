package chat

import "net/http"

// serveCampaignCommitment records the one commitment that works a followed campaign for AdmiralBot.
func (s *Service) serveCampaignCommitment(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}
