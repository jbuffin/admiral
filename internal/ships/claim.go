package ships

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/config"
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
	for k, sh := range claimingShips(list) {
		out[k] = cmpName(sh.Name, sh.Session) // a pending launch has a session but no name yet
	}
	return out
}

func claimingShips(list []Ship) map[string]Ship {
	out := map[string]Ship{}
	for _, sh := range list {
		for _, u := range sh.IssueURLs() {
			if k, ok := ClaimKey(u); ok {
				out[k] = sh
			}
		}
	}
	return out
}

// holderOf names a holding ship for a refusal; to AdmiralBot, a ship of another fleet is only that fleet.
func holderOf(req launcher.Request, sh Ship) string {
	if req.ByFleet != "" && sh.Fleet != req.ByFleet {
		return "a ship in fleet " + cmpName(sh.FleetName, sh.Fleet)
	}
	return cmpName(sh.Name, sh.Session)
}

// CheckIssueURLs refuses a URL that is not an https GitHub issue on one of hosts, and returns the claim-normalised forms.
func CheckIssueURLs(urls, hosts []string) ([]string, *launcher.Error) {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		k, ok := ClaimKey(u)
		if ok {
			host := strings.TrimPrefix(k, "https://")
			host = host[:strings.IndexByte(host, '/')]
			ok = strings.HasPrefix(strings.ToLower(strings.TrimSpace(u)), "https://") && slices.Contains(hosts, host)
		}
		if !ok {
			return nil, &launcher.Error{Status: http.StatusBadRequest, Msg: "issues must be https GitHub issue URLs on a host the fleet uses, not " + strconv.Quote(u)}
		}
		out = append(out, k)
	}
	return out, nil
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
	if f, ok := actor.FleetOf(ctx); ok {
		req.ByFleet = f
		// The server keys an actor's freeform launch on the chat prompt in force; a client-sent id is dropped.
		req.ChatMessage = ""
		if req.Source == "" && req.OrderID == "" && len(req.Issues) == 0 && s.ChatPrompt != nil {
			req.ChatMessage = s.ChatPrompt(f)
		}
	}
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
		// A commitment retries after a wait and the clone path is fixed, so it must not clone before it knows there is room.
		if req.CommitmentKey != "" {
			s.admitMu.Lock()
			release, lerr := s.admit(req)
			s.admitMu.Unlock()
			if lerr != nil {
				return Ship{}, lerr
			}
			release()
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
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	// the actor guard reads this same first value, so anything after it is refused rather than ignored
	if dec.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: data after the request")
		return
	}
	// a campaign's skill, text and refusals are set by its own route, not the client's
	if req.Source == config.ReservedSourceKey {
		writeError(w, http.StatusBadRequest, "launch a campaign's child through its campaign")
		return
	}
	// a source's order has its own issue; only a launch with no source names issues, and the campaign route sets a slice's itself
	if req.Source != "" && len(req.Issues) > 0 {
		writeError(w, http.StatusBadRequest, "issues go only on a launch with no source")
		return
	}
	req.Via = viaOf(r)
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

func (s *Service) setUnlistedIssues(key OrderKey, url string, issues []string, chat, routine, commitment, fleet string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.listing[key]; ok {
		u.url, u.issues, u.chat, u.routine, u.commitment, u.fleet = url, issues, chat, routine, commitment, fleet
		s.listing[key] = u
	}
}

func cmpName(name, session string) string {
	if name != "" {
		return name
	}
	return session
}

// IssueURLs is every issue the ship holds: Issues for a slice, else its one URL.
func (sh Ship) IssueURLs() []string {
	if len(sh.Issues) > 0 {
		return sh.Issues
	}
	if sh.URL != "" {
		return []string{sh.URL}
	}
	return nil
}

func splitIssues(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// distinctIssues drops a repeated issue, whatever its spelling, keeping the first.
func distinctIssues(urls []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range urls {
		k := claimKeyOf(u)
		if k == "" {
			k = u
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, u)
		}
	}
	return out
}

// lockIssues takes every issue's in-flight claim or none, and ok is false with the holder when one is taken.
func (s *Service) lockIssues(urls []string, holder string) (heldBy string, ok bool) {
	for i, u := range urls {
		if h, ok := s.lockIssue(u, holder); !ok {
			s.unlockIssues(urls[:i])
			return h, false
		}
	}
	return "", true
}

func (s *Service) unlockIssues(urls []string) {
	for _, u := range urls {
		s.unlockIssue(u)
	}
}
