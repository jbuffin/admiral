// Package follow keeps the campaigns an engineer follows: a store under $ADMIRAL_HOME, the reads that
// refresh their trees, the HTTP routes, and the campaign order source that launches a child by hand.
package follow

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
)

// SourceKey is the order source key campaign ships carry.
const SourceKey = "campaign"

// Followed is one followed campaign as stored.
type Followed struct {
	ID         string
	URL        string // the parent's issue URL
	Fleet      string
	WorkRepo   string // host/owner/repo, or empty
	Overrides  map[string]campaigns.Override
	FollowedAt time.Time
	EndedAt    time.Time // zero while the campaign is followed
	Tree       *github.TreeNode
	ReadAt     time.Time
	ReadError  string
}

func (f Followed) Ended() bool { return !f.EndedAt.IsZero() }

// Store is the file of followed campaigns, campaigns.json under the home.
type Store struct{}

var errStub = errors.New("not implemented")

func OpenStore(home string) (*Store, error) { return &Store{}, nil }

// Follow adds f, or returns the campaign already followed for the same issue with created false.
func (s *Store) Follow(f Followed) (got Followed, created bool, err error) {
	return Followed{}, false, errStub
}
func (s *Store) Get(id string) (Followed, bool) { return Followed{}, false }

// List returns every campaign, active and ended, in the order they were followed.
func (s *Store) List() []Followed                                            { return nil }
func (s *Store) End(id string, at time.Time) error                           { return nil }
func (s *Store) SetOverride(id, childURL string, o campaigns.Override) error { return nil }
func (s *Store) SetWorkRepo(id, repo string) error                           { return nil }

// SetRead records a read; a nil tree keeps the last good one.
func (s *Store) SetRead(id string, tree *github.TreeNode, at time.Time, readErr string) error {
	return nil
}

// Service follows campaigns and launches their children.
type Service struct {
	Store *Store
	// Route names the fleet that owns a repo, as github.Router.Route does.
	Route func(host, nameWithOwner string) string
	// Clients gives the fleet's GitHub client for a host, read fresh on each call.
	Clients func(fleet, host string) (*github.Client, bool)
	// Ships lists the ships now, to see which child's ship anchored or left.
	Ships func() []ships.Ship
	// Claims maps a ClaimKey to the ship holding that issue.
	Claims func() map[string]string
	// Launch is ships.Service.Launch.
	Launch func(ctx context.Context, req launcher.Request) (ships.Ship, *launcher.Error)
	// Next answers every order that is not a campaign's.
	Next ships.Orders
	Now  func() time.Time
}

func (s *Service) Routes(mux *http.ServeMux)  {}
func (s *Service) Connected(c *server.Client) {}

// Tick is the periodic pass: it rereads each active campaign last read five minutes ago or more.
func (s *Service) Tick() {}

// ShipsChanged is the ships' OnChange hook; it never blocks.
func (s *Service) ShipsChanged() {}

// Order answers for a campaign child (source SourceKey, id the child's issue URL) and hands the rest to Next.
func (s *Service) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	return "", "", "", "", "", &launcher.Error{Status: http.StatusNotImplemented, Msg: "not implemented"}
}

// Launched triggers a reread of the child's campaign.
func (s *Service) Launched(source, id string) {}
