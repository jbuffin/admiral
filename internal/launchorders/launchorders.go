// Package launchorders serves the launch orders dialog. None of its GET handlers
// changes anything.
package launchorders

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/commodore"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/repofinder"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/skillindex"
	"github.com/jbuffin/admiral/internal/sources"
	"github.com/jbuffin/admiral/internal/state"
)

type Service struct {
	Home   string // $ADMIRAL_HOME, where the fleets' crew directories live
	Config *config.Manager
	State  *state.Store
	Ships  func() []ships.Ship
	Orders ships.Orders
	// Installer runs Commodore's install and update; nil means the dock's own CLI.
	Installer commodore.Installer

	installing sync.Mutex
	running    map[string]bool // fleet/harness docks with an install or update in flight
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/launch-orders/dirs", s.serveDirs)
	mux.HandleFunc("GET /api/launch-orders/skills", s.serveSkills)
	mux.HandleFunc("GET /api/launch-orders/crew", s.serveCrew)
	mux.HandleFunc("POST /api/launch-orders/commodore", s.serveCommodore)
	mux.HandleFunc("GET /api/ships/{session}/skills", s.serveShipSkills)
	mux.HandleFunc("GET /api/ships/{session}/crew/{name}/skills", s.serveShipSkills)
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

// serveDirs is a POST only because the order's text can be long.
func (s *Service) serveDirs(w http.ResponseWriter, r *http.Request) {
	var req dirsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	if req.Source != "" {
		var lerr *launcher.Error
		if req.Text, _, req.Repo, _, _, lerr = s.Orders.Order(req.Source, req.OrderID); lerr != nil {
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
	entries, live := s.list(fleet)
	selected, reason := s.preselect(&entries, fleet, req, live)
	// A hinted repo with no checkout is cloned: the clone is the preselection.
	var clone map[string]any
	if reason != "repo" && reason != "text" {
		if repo, path, ok := launcher.CloneTarget(cfg, fleet, req.Repo, req.Text); ok {
			clone = map[string]any{"repo": repo, "path": path}
			if _, err := os.Lstat(path); err == nil {
				clone["exists"] = true // the launch would be refused: keep the old preselection
			} else {
				selected, reason = "", "clone"
			}
		}
	}

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
	} else if reason == "clone" {
		why = &reason
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "selected": sel, "reason": why, "clone": clone})
}

func (s *Service) list(fleet config.Fleet) ([]repofinder.Entry, []ships.Ship) {
	entries := repofinder.List(repofinder.FleetRoots(fleet.SearchRoots, fleet.CloneRoot))
	live := s.Ships()
	checkouts := slices.IndexFunc(entries, func(e repofinder.Entry) bool { return e.Kind != repofinder.KindCheckout })
	if checkouts < 0 {
		checkouts = len(entries)
	}
	slices.SortStableFunc(entries[:checkouts], func(a, b repofinder.Entry) int {
		return lastUsed(s.State, b.Path, live).Compare(lastUsed(s.State, a.Path, live))
	})
	return entries, live
}

// Dir never falls back to the dock's last directory (ADR 0009 rules 1 and 2).
func (s *Service) Dir(fleet config.Fleet, harness string, o sources.Order) (string, string) {
	if !fleet.CanLaunch() {
		return "", "fleet " + fleet.Name + " has no search roots"
	}
	text := o.Text
	if text == "" {
		text = o.Title
	}
	entries, live := s.list(fleet)
	dir, reason := s.preselect(&entries, fleet, dirsRequest{Fleet: fleet.Key, Harness: harness, Repo: o.Repo, Text: text}, live)
	if reason != "repo" && reason != "text" || !slices.ContainsFunc(entries, func(e repofinder.Entry) bool {
		return e.Path == dir && e.Kind == repofinder.KindCheckout
	}) {
		return "", "no checkout in fleet " + fleet.Name + " has the order's repository"
	}
	return dir, ""
}

func (s *Service) Checkout(fleet config.Fleet, dir string) bool {
	return slices.ContainsFunc(repofinder.List(repofinder.FleetRoots(fleet.SearchRoots, fleet.CloneRoot)), func(e repofinder.Entry) bool {
		return e.Path == dir && e.Kind == repofinder.KindCheckout
	})
}

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

// A lower tier is more recent.
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

func lastUsed(st *state.Store, dir string, live []ships.Ship) time.Time {
	latest := st.DirUsed(dir)
	for _, sh := range live {
		if t, err := time.Parse(time.RFC3339, sh.Launched); err == nil && sh.Dir == dir && t.After(latest) {
			latest = t
		}
	}
	return latest
}

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
	dir, listed := repofinder.Contains(repofinder.List(repofinder.FleetRoots(fleet.SearchRoots, fleet.CloneRoot)), q.Get("dir"))
	if !listed {
		writeError(w, http.StatusBadRequest, "directory "+q.Get("dir")+" is not a checkout or search root of fleet "+fleet.Name)
		return
	}
	found := skillindex.List(dock.Name, dock.Environ(), dir)
	type skill struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Favorite    bool   `json:"favorite"`
	}
	list := make([]skill, len(found))
	for i, sk := range found {
		list[i] = skill{sk.Name, sk.Description, slices.Contains(cfg.FavoriteSkills, sk.Name)}
	}
	cmd := struct {
		Found bool   `json:"found"`
		Name  string `json:"name"`
		commodore.State
	}{State: commodore.Detect(dock)}
	cmd.Name, cmd.Found = skillindex.FindCommodore(dock.Name, dock.Environ(), dir)
	// FindCommodore answers "" when it finds nothing, so Name needs no reset.
	writeJSON(w, http.StatusOK, map[string]any{"mark": skillindex.Mark(dock.Name), "skills": list, "commodore": cmd})
}

const installTimeout = 5 * time.Minute

// serveCommodore installs or updates Commodore as a plugin in a dock's own identity.
func (s *Service) serveCommodore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Fleet   string `json:"fleet"`
		Harness string `json:"harness"`
		Action  string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	fleet, ok := s.Config.Current().Fleet(req.Fleet)
	if !ok {
		writeError(w, http.StatusNotFound, "fleet "+req.Fleet+" not found")
		return
	}
	dock, ok := fleet.Harness(req.Harness)
	if !ok {
		writeError(w, http.StatusBadRequest, "fleet "+fleet.Name+" has no "+req.Harness+" dock")
		return
	}
	action := commodore.Action(req.Action)
	if action != commodore.Install && action != commodore.Update {
		writeError(w, http.StatusBadRequest, `action must be "install" or "update"`)
		return
	}
	// Fleets can share one identity, and its settings and plugin cache take one writer at a time.
	key := dock.Name + " " + commodore.Identity(dock)
	s.installing.Lock()
	if s.running[key] {
		s.installing.Unlock()
		writeError(w, http.StatusConflict, "Commodore is already being installed or updated on this dock")
		return
	}
	if s.running == nil {
		s.running = map[string]bool{}
	}
	s.running[key] = true
	s.installing.Unlock()
	defer func() {
		s.installing.Lock()
		delete(s.running, key)
		s.installing.Unlock()
	}()

	before := commodore.Detect(dock)
	if action == commodore.Update && before.State != commodore.StateOutdated {
		writeError(w, http.StatusConflict, "Commodore is "+before.State+" here, so there is nothing to update")
		return
	}
	installer := s.Installer
	if installer == nil {
		home, err := filepath.Abs(s.Home)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		installer = &commodore.Runner{Home: home, Timeout: installTimeout}
	}
	// The run finishes even if the client goes away, so a half-done install is not left behind.
	out, err := installer.Run(context.WithoutCancel(r.Context()), dock, action)
	if errors.Is(err, commodore.ErrAutoUpdate) {
		log.Printf("admiral: Commodore installed on %s/%s: %v", fleet.Key, dock.Name, err)
		writeJSON(w, http.StatusOK, struct {
			commodore.State
			Warning string `json:"warning"`
		}{commodore.Detect(dock), err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":    err.Error(),
			"output":   lastLines(out, 20),
			"commands": commodore.Commands(dock.Name, action),
			"state":    commodore.Detect(dock),
		})
		return
	}
	writeJSON(w, http.StatusOK, commodore.Detect(dock))
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// serveShipSkills lists the skills a ship's or boat's message box offers: its
// own harness's, read in its own checkout, which may be a worktree the launch
// dialog never lists.
func (s *Service) serveShipSkills(w http.ResponseWriter, r *http.Request) {
	session, boat := r.PathValue("session"), r.PathValue("name")
	var sh ships.Ship
	found := false
	for _, x := range s.Ships() {
		if x.Session == session {
			sh, found = x, true
			break
		}
	}
	harness, dir := sh.Harness, sh.Dir
	if found && boat != "" {
		found = false
		for _, b := range sh.Crew {
			if b.Name == boat {
				harness, dir, found = b.Harness, b.Dir, true
				break
			}
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	environ := os.Environ()
	if fleet, ok := s.Config.Current().Fleet(sh.Fleet); ok {
		if dock, ok := fleet.Harness(harness); ok {
			environ = dock.Environ()
		}
	}
	list := skillindex.List(harness, environ, dir)
	if list == nil {
		list = []skillindex.Skill{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"mark": skillindex.Mark(harness), "skills": list})
}

// serveCrew lists the members a launch could pin. Without a dir, the repo layer is left out.
func (s *Service) serveCrew(w http.ResponseWriter, r *http.Request) {
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
	dir := ""
	if q.Get("dir") != "" {
		var listed bool
		dir, listed = repofinder.Contains(repofinder.List(repofinder.FleetRoots(fleet.SearchRoots, fleet.CloneRoot)), q.Get("dir"))
		if !listed {
			writeError(w, http.StatusBadRequest, "directory "+q.Get("dir")+" is not a checkout or search root of fleet "+fleet.Name)
			return
		}
	}
	home, err := filepath.Abs(s.Home)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res := crew.Resolve(launcher.CrewLayers(home, fleet.Key, dock.Name, dock.Environ(), dir))
	type member struct {
		Name       string  `json:"name"`
		Role       string  `json:"role"`
		PickMeWhen string  `json:"pick-me-when"`
		Layer      string  `json:"layer"`
		Harness    *string `json:"harness"`
	}
	members := []member{}
	if res.Exit == 0 {
		for _, m := range res.Members {
			members = append(members, member{m.Name, m.Role, m.PickMeWhen, m.Layer, m.Harness})
		}
	}
	orEmpty := func(d []crew.Diagnostic) []crew.Diagnostic {
		if d == nil {
			return []crew.Diagnostic{}
		}
		return d
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members, "warnings": orEmpty(res.Warnings), "errors": orEmpty(res.Errors)})
}

// The five-star limit is checked here so the refusal can say what to do.
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
