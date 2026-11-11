package ships

import (
	"context"
	"net/http"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
)

// LaunchProposal starts the feedback round a wake proposal asks for.
func (s *Service) LaunchProposal(ctx context.Context, p inbox.WakeProposal) (Ship, *launcher.Error) {
	return Ship{}, &launcher.Error{Status: http.StatusNotImplemented, Msg: "not built"}
}
