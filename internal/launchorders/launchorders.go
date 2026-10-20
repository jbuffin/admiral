// Package launchorders serves what the launch orders dialog needs from the
// server (stories 70–85): the directories a ship can start in with the
// pre-selection, the skills a dock can invoke, and the favorite skills. None
// of the GET handlers changes anything.
package launchorders

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/repofinder"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/skillindex"
	"github.com/jbuffin/admiral/internal/state"
)

// Service reads the config, the ships and the remembered directories.
type Service struct {
	Config *config.Manager
	State  *state.Store
	Ships  func() []ships.Ship
	Orders ships.Orders
}

// Routes registers the launch orders API.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/launch-orders/dirs", s.serveDirs)
	mux.HandleFunc("GET /api/launch-orders/skills", s.serveSkills)
	mux.HandleFunc("PUT /api/favorites", s.serveFavorites)
}

// Connected sends nothing: the dialog asks when it opens.
func (s *Service) Connected(*server.Client) {}

type dirEntry struct {
	repofinder.Entry
	ShipsHere int `json:"ships_here"`
}

type dirsRequest struct {
	Fleet   string `json:"fleet"`
	Harness string `json:"harness"`
	Source  string `json:"source"`
	OrderID string `json:"order_id"`
	// Repo and Text are read only when there is no source: with one, the
	// server looks both up in the order.
	Repo string `json:"repo"`
	Text string `json:"text"`
}

// serveDirs answers with the list and the pre-selection. It is a POST only
// because the order's text can be long.
func (s *Service) serveDirs(w http.ResponseWriter, r *http.Request) {
	var req dirsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	if req.Source != "" {
		var lerr *launcher.Error
		if req.Text, _, req.Repo, _, lerr = s.Orders.Order(req.Source, req.OrderID); lerr != nil {
			writeError(w, lerr.Status, lerr.Msg)
			return
		}
	}
	cfg := s.Config.Current()
	fleet, ok := cfg.Fleet(req.Fleet)
	if !ok {
		writeError(w, http.StatusNotFound, "fleet "+req.Fleet+" not found")
		return
	}
	if _, ok := fleet.Harness(req.Harness); !ok {
		writeError(w, http.StatusBadRequest, "fleet "+fleet.Name+" has no "+req.Harness+" dock")
		return
	}
	if !fleet.CanLaunch() {
		writeError(w, http.StatusConflict, "This fleet has no search roots")
		return
	}
	entries := repofinder.List(fleet.SearchRoots)
	live := s.Ships()
	// The checkouts most recently used first; the search roots stay last.
	checkouts := slices.IndexFunc(entries, func(e repofinder.Entry) bool { return e.Kind != repofinder.KindCheckout })
	if checkouts < 0 {
		checkouts = len(entries)
	}
	slices.SortStableFunc(entries[:checkouts], func(a, b repofinder.Entry) int {
		return lastUsed(s.State, b.Path, live).Compare(lastUsed(s.State, a.Path, live))
	})
	selected, reason := s.preselect(&entries, fleet, req, live)

	out := make([]dirEntry, len(entries))
	for i, e := range entries {
		out[i] = dirEntry{Entry: e}
		for _, sh := range live {
			if sh.Dir == e.Path && (sh.Status == ships.StatusUnderway || sh.Status == ships.StatusNeedsYou) {
				out[i].ShipsHere++
			}
		}
	}
	var sel, why *string
	if selected != "" {
		sel, why = &selected, &reason
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "selected": sel, "reason": why})
}

// preselect picks the directory (ADR 0009) and floats several matching
// checkouts to the top of entries, the most recently used first.
func (s *Service) preselect(entries *[]repofinder.Entry, fleet config.Fleet, req dirsRequest, live []ships.Ship) (string, string) {
	matching := func(id string) []int {
		var at []int
		for i, e := range *entries {
			if slices.Contains(e.Remotes, id) {
				at = append(at, i)
			}
		}
		return at
	}
	var at []int
	reason := ""
	if id, ok := repofinder.Identity(req.Repo); ok {
		if at = matching(id); len(at) > 0 {
			reason = "repo"
		}
	}
	if reason == "" {
		for _, id := range repofinder.URLs(req.Text) {
			if at = matching(id); len(at) > 0 {
				reason = "text"
				break
			}
		}
	}
	if reason == "" {
		last := s.State.LastDir(fleet.Key, req.Harness)
		if last == "" {
			return "", ""
		}
		for _, e := range *entries {
			if e.Path == last {
				return last, "last_used"
			}
		}
		return "", ""
	}
	if len(at) == 1 {
		return (*entries)[at[0]].Path, reason
	}
	rank := s.recency(fleet, req.Harness, live)
	slices.SortStableFunc(at, func(a, b int) int { return rank((*entries)[a].Path).compare(rank((*entries)[b].Path)) })
	best := (*entries)[at[0]].Path
	var top, rest []repofinder.Entry
	floated := map[int]bool{}
	for _, i := range at {
		if (*entries)[i].Kind == repofinder.KindCheckout {
			top = append(top, (*entries)[i])
			floated[i] = true
		}
	}
	for i, e := range *entries {
		if !floated[i] {
			rest = append(rest, e)
		}
	}
	*entries = append(top, rest...)
	return best, reason
}

// use says how recently a directory was used; a lower tier is more recent.
type use struct {
	tier int
	at   time.Time
}

func (a use) compare(b use) int {
	switch {
	case a.tier != b.tier:
		return a.tier - b.tier
	case a.at.After(b.at):
		return -1
	case b.at.After(a.at):
		return 1
	}
	return 0
}

// lastUsed is the latest launch into dir: the recorded one, or an existing
// ship's if that is later. It is zero for a directory never launched into.
func lastUsed(st *state.Store, dir string, live []ships.Ship) time.Time {
	latest := st.DirUsed(dir)
	for _, sh := range live {
		if t, err := time.Parse(time.RFC3339, sh.Launched); err == nil && sh.Dir == dir && t.After(latest) {
			latest = t
		}
	}
	return latest
}

// recency ranks a directory: its latest launch, else this dock's last
// directory, else the fleet's other docks' last directories, else not at all.
func (s *Service) recency(fleet config.Fleet, harness string, live []ships.Ship) func(string) use {
	return func(dir string) use {
		latest := lastUsed(s.State, dir, live)
		switch {
		case !latest.IsZero():
			return use{0, latest}
		case s.State.LastDir(fleet.Key, harness) == dir:
			return use{1, time.Time{}}
		}
		for _, h := range fleet.Harnesses {
			if h.Name != harness && s.State.LastDir(fleet.Key, h.Name) == dir {
				return use{2, time.Time{}}
			}
		}
		return use{3, time.Time{}}
	}
}

// serveSkills lists the skills the dock finds in a directory of the fleet's
// list, with the favorites marked, and which skill is Commodore.
func (s *Service) serveSkills(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cfg := s.Config.Current()
	fleet, ok := cfg.Fleet(q.Get("fleet"))
	if !ok {
		writeError(w, http.StatusNotFound, "fleet "+q.Get("fleet")+" not found")
		return
	}
	dock, ok := fleet.Harness(q.Get("harness"))
	if !ok {
		writeError(w, http.StatusBadRequest, "fleet "+fleet.Name+" has no "+q.Get("harness")+" dock")
		return
	}
	if !fleet.CanLaunch() {
		writeError(w, http.StatusConflict, "This fleet has no search roots")
		return
	}
	// Project skill folders are read only inside a listed directory.
	dir, listed := repofinder.Contains(repofinder.List(fleet.SearchRoots), q.Get("dir"))
	if !listed {
		writeError(w, http.StatusBadRequest, "directory "+q.Get("dir")+" is not a checkout or search root of fleet "+fleet.Name)
		return
	}
	names := skillindex.Installed(dock.Name, dock.Environ(), dir)
	type skill struct {
		Name     string `json:"name"`
		Favorite bool   `json:"favorite"`
	}
	list := make([]skill, len(names))
	for i, n := range names {
		list[i] = skill{n, slices.Contains(cfg.FavoriteSkills, n)}
	}
	commodore := map[string]any{"found": false, "name": ""}
	if n, ok := skillindex.FindCommodore(dock.Name, dock.Environ(), dir); ok {
		commodore = map[string]any{"found": true, "name": n}
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": list, "commodore": commodore})
}

// serveFavorites replaces the favorite skills. The five-star limit is checked
// here so the refusal can say what to do.
func (s *Service) serveFavorites(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Skills []string `json:"skills"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	skills := req.Skills
	for i, n := range skills {
		if n == "" || n != strings.TrimSpace(n) || slices.Contains(skills[:i], n) {
			writeError(w, http.StatusBadRequest, "favorite skill names must be non-empty, trimmed and listed once, got "+strconv.Quote(n))
			return
		}
	}
	if len(skills) > config.MaxFavoriteSkills {
		writeError(w, http.StatusBadRequest, "Unstar one first")
		return
	}
	if err := s.Config.SetFavoriteSkills(skills); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"favorite_skills": s.Config.Current().FavoriteSkills})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
