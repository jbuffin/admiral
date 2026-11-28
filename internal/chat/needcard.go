package chat

import (
	"context"
	"net/http"
)

// InboxChanged tells the need card poster the Inbox's list changed; it never blocks.
func (s *Service) InboxChanged() {}

// InboxSettled records, before the Inbox releases a kept ship's files, that an item was answered or removed through the Inbox.
func (s *Service) InboxSettled(session, id string, removed bool) {}

// RunNeedCards posts a need card for every open question of a ship that has a fleet, until ctx ends.
func (s *Service) RunNeedCards(ctx context.Context) { <-ctx.Done() }

func (s *Service) serveNeedCardAnswer(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "need cards are not built yet")
}
