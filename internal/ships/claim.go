package ships

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/launcher"
)

// ClaimKey normalises a GitHub issue URL for the claim; ok is false for any other URL, so a plugin
// source's non-issue link never holds a claim.
func ClaimKey(url string) (key string, ok bool) {
	u := strings.TrimSpace(url)
	if i := strings.IndexByte(u, '#'); i >= 0 {
		u = u[:i]
	}
	r, err := campaigns.ParseIssueRef(strings.TrimRight(u, "/"))
	if err != nil || r.Host == "" {
		return "", false
	}
	return strings.ToLower(fmt.Sprintf("https://%s/%s/%s/issues/%d", r.Host, r.Owner, r.Repo, r.Number)), true
}

// ClaimsOf maps each claimed issue (by ClaimKey) to the name of a listed ship holding it, of any status.
func ClaimsOf(list []Ship) map[string]string {
	out := map[string]string{}
	for _, sh := range list {
		if k, ok := ClaimKey(sh.URL); ok {
			out[k] = cmpName(sh.Name, sh.Session) // a pending launch has a session but no name yet
		}
	}
	return out
}

// Claims is ClaimsOf over the listed ships, the pending launches and the launches in flight.
func (s *Service) Claims() map[string]string {
	out := ClaimsOf(append(s.List(), s.Pending()...))
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, holder := range s.issueLocks {
		if _, held := out[k]; !held {
			out[k] = holder
		}
	}
	return out
}

// lockIssue takes the in-flight claim on an issue URL for the named launch; when another holds it, ok is false and holder names it.
// A URL that is not an issue is never locked.
func (s *Service) lockIssue(url, holder string) (heldBy string, ok bool) {
	k, isIssue := ClaimKey(url)
	if !isIssue {
		return "", true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, taken := s.issueLocks[k]; taken {
		return h, false
	}
	if s.issueLocks == nil {
		s.issueLocks = map[string]string{}
	}
	s.issueLocks[k] = holder
	return "", true
}

func (s *Service) unlockIssue(url string) {
	if k, ok := ClaimKey(url); ok {
		s.mu.Lock()
		delete(s.issueLocks, k)
		s.mu.Unlock()
	}
}

// Launch is what POST /api/ships does after decoding: clone, admit, LaunchOrder, hold the reservation.
func (s *Service) Launch(ctx context.Context, req launcher.Request) (Ship, *launcher.Error) {
	// These need no disk, so a bad request leaves no clone.
	if lerr := launcher.CheckMember(req.Skill.Kind, req.Member); lerr != nil {
		return Ship{}, lerr
	}
	if lerr := launcher.CheckPins(req.Skill.Kind, req.Crew); lerr != nil {
		return Ship{}, lerr
	}
	if req.Clone {
		if req.Dir != "" {
			return Ship{}, &launcher.Error{Status: http.StatusBadRequest, Msg: "send dir or clone, not both"}
		}
		repo, text := "", req.Text
		if req.Source != "" {
			var lerr *launcher.Error
			if text, _, repo, _, _, lerr = s.Orders.Order(req.Source, req.OrderID); lerr != nil {
				return Ship{}, lerr
			}
		}
		dir, err := s.Launcher.Clone(ctx, req, repo, text)
		if err != nil {
			le, ok := err.(*launcher.Error)
			if !ok {
				le = &launcher.Error{Status: http.StatusInternalServerError, Msg: err.Error()}
			}
			return Ship{}, le
		}
		req.Dir = dir
	}
	if req.Autopilot {
		s.admitMu.Lock()
		defer s.admitMu.Unlock()
	}
	release, lerr := s.admit(req)
	if lerr != nil {
		return Ship{}, lerr
	}
	got, lerr := s.LaunchOrder(req)
	if lerr != nil {
		release()
		return Ship{}, lerr
	}
	s.releaseWhenListed(got.Session, release)
	return got, nil
}

func (s *Service) serveLaunch(w http.ResponseWriter, r *http.Request) {
	var req launcher.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	got, lerr := s.Launch(r.Context(), req)
	if lerr != nil {
		writeError(w, lerr.Status, lerr.Msg)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ship": got})
}

func claimKeyOf(url string) string { k, _ := ClaimKey(url); return k }

func issueHeld(holder string) *launcher.Error {
	return &launcher.Error{Status: http.StatusConflict, Msg: "this issue already has a ship (" + holder + ")"}
}

func (s *Service) setUnlistedURL(key OrderKey, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.listing[key]; ok {
		u.url = url
		s.listing[key] = u
	}
}

func cmpName(name, session string) string {
	if name != "" {
		return name
	}
	return session
}
