package ships

import (
	"context"
	"net/http"

	"github.com/jbuffin/admiral/internal/launcher"
)

// ClaimKey normalises a GitHub issue URL for the claim; ok is false for any other URL.
func ClaimKey(url string) (key string, ok bool) { return "", false }

// ClaimsOf maps each claimed issue (by ClaimKey) to the name of a listed ship holding it, of any status.
func ClaimsOf(list []Ship) map[string]string { return map[string]string{} }

// Claims is ClaimsOf over the listed ships, the pending launches and the launches in flight.
func (s *Service) Claims() map[string]string { return map[string]string{} }

// lockIssue takes the in-flight claim on an issue URL for the named launch; when another holds it, ok is false and holder names it.
func (s *Service) lockIssue(url, holder string) (heldBy string, ok bool) { return "", true }

func (s *Service) unlockIssue(url string) {}

// Launch is what POST /api/ships does after decoding: clone, admit, LaunchOrder, hold the reservation.
func (s *Service) Launch(ctx context.Context, req launcher.Request) (Ship, *launcher.Error) {
	return Ship{}, &launcher.Error{Status: http.StatusNotImplemented, Msg: "not implemented"}
}
