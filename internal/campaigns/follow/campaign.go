package follow

import (
	"context"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/launcher"
)

// Follow follows the campaign a reference names, or returns the one already followed; created says which.
func (s *Service) Follow(ctx context.Context, reference string) (Followed, bool, *launcher.Error) {
	return Followed{}, false, nil
}

// Snapshot is the campaign as a commitment pass reads it: the latest good tree, built the way the page's is.
func (s *Service) Snapshot(id string) campaigns.Read { return campaigns.Read{} }
