package ships

import (
	"context"
	"net/http"

	"github.com/jbuffin/admiral/internal/launcher"
)

// Wake relaunches a moored ship by hand, with the engineer's optional text after its resume text.
func (s *Service) Wake(ctx context.Context, session, text string) (Ship, *launcher.Error) {
	return Ship{}, &launcher.Error{Status: http.StatusNotImplemented, Msg: "not implemented"}
}

// FeedbackRound relaunches a moored ship, unattended, for the feedback on one of its PRs.
func (s *Service) FeedbackRound(ctx context.Context, session, pr, feedback string) (Ship, *launcher.Error) {
	return Ship{}, &launcher.Error{Status: http.StatusNotImplemented, Msg: "not implemented"}
}

func (s *Service) serveWake(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}
