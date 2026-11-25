// Package crewapi serves a fleet's Crew page: its built-in and fleet members with their notes, and the
// edits the page makes to fleet members and to notes. The resolver in internal/crew stays read-only; every
// write to a crew directory lives here.
package crewapi

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/skillindex"
)

type Service struct {
	Home   string // $ADMIRAL_HOME, where the fleets' crew directories live
	Config *config.Manager
	// Judge makes the draft call; nil means judge.Run.
	Judge func(context.Context, judge.Choice, judge.Call) (judge.Reply, error)
	// GitHub returns the fleet's client; nil, or a nil result, means the fleet has no GitHub source.
	GitHub func(cfg *config.Config, fleet string) *github.Client
	// ScanTimeout bounds one gitleaks run; zero means the runner's default.
	ScanTimeout time.Duration
	// Clock times the update checker and the listing cache; nil means the real clock.
	Clock Clock

	listings listings
	updates  updateStore
}

func (s *Service) Routes(mux *http.ServeMux) {
	s.publishRoutes(mux)
	s.importRoutes(mux)
	s.updateRoutes(mux)
	mux.HandleFunc("GET /api/fleets/{key}/crew", s.serveCrew)
	mux.HandleFunc("GET /api/fleets/{key}/crew/skills", s.serveSkills)
	mux.HandleFunc("POST /api/fleets/{key}/crew/draft", s.serveDraft)
	mux.HandleFunc("POST /api/fleets/{key}/crew/members", s.serveCreate)
	mux.HandleFunc("PUT /api/fleets/{key}/crew/members/{name}", s.servePut)
	mux.HandleFunc("DELETE /api/fleets/{key}/crew/members/{name}", s.serveDelete)
	mux.HandleFunc("POST /api/fleets/{key}/crew/members/{name}/customize", s.serveCustomize)
	mux.HandleFunc("POST /api/fleets/{key}/crew/members/{name}/rename", s.serveRename)
	mux.HandleFunc("POST /api/fleets/{key}/crew/members/{name}/make-mine", s.serveMakeMine)
	mux.HandleFunc("POST /api/fleets/{key}/crew/notes", s.serveNotes)
}

// Connected sends nothing: the page asks when it opens.
func (s *Service) Connected(*server.Client) {}

// fleetCrew is what a request over one fleet's crew works on.
type fleetCrew struct {
	home       string
	key        string
	harness    string // the dock whose Commodore is the built-in layer; empty when the fleet has no docks
	builtInDir string // empty when that dock has no Commodore installed
}

// A fleet's crew directory is not made until a write needs it.
func (c fleetCrew) crewDir() string { return filepath.Join(c.home, "fleets", c.key, "crew") }

func (c fleetCrew) layers(withFleet bool) crew.Input {
	layers := map[string]string{}
	if withFleet {
		layers[crew.LayerFleet] = c.crewDir()
	}
	if c.builtInDir != "" {
		layers[crew.LayerBuiltIn] = c.builtInDir
	}
	return crew.Input{Admiral: true, Layers: layers}
}

// builtIns lists the built-in members; a layer with errors lists none.
func (c fleetCrew) builtIns() map[string]crew.Member {
	out := map[string]crew.Member{}
	if res := crew.Resolve(c.layers(false)); res.Exit == 0 {
		for _, m := range res.Members {
			out[m.Name] = m
		}
	}
	return out
}

func (s *Service) fleetCrew(w http.ResponseWriter, r *http.Request) (fleetCrew, bool) {
	key := r.PathValue("key")
	fleet, ok := s.Config.Current().Fleet(key)
	if !ok {
		writeError(w, http.StatusNotFound, "fleet "+key+" not found")
		return fleetCrew{}, false
	}
	home, err := filepath.Abs(s.Home)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return fleetCrew{}, false
	}
	c := fleetCrew{home: home, key: fleet.Key}
	if want := r.URL.Query().Get("harness"); want != "" {
		if _, ok := fleet.Harness(want); !ok {
			writeError(w, http.StatusBadRequest, "fleet "+fleet.Name+" has no "+want+" dock")
			return fleetCrew{}, false
		}
		c.harness = want
	} else if len(fleet.Harnesses) > 0 {
		c.harness = fleet.Harnesses[0].Name
	}
	if dock, ok := fleet.Harness(c.harness); ok {
		if d, ok := skillindex.CommodoreDir(dock.Name, dock.Environ(), ""); ok {
			c.builtInDir = filepath.Join(d, "crew")
		}
	}
	return c, true
}

type notesView struct {
	Fleet []string            `json:"fleet"`
	Repos map[string][]string `json:"repos"`
}

type memberView struct {
	Name         string       `json:"name"`
	Role         string       `json:"role"`
	BasedOn      *string      `json:"based-on"`
	Skills       []string     `json:"skills"`
	Model        *string      `json:"model"`
	Harness      *string      `json:"harness"`
	PickMeWhen   string       `json:"pick-me-when"`
	Instructions string       `json:"instructions"`
	Layer        string       `json:"layer"`
	Customized   bool         `json:"customized"`
	Notes        notesView    `json:"notes"`
	Shared       *crew.Shared `json:"shared"`
	// SidecarUnreadable is true when the member's sidecar is there but can't be used.
	SidecarUnreadable bool `json:"sidecar_unreadable"`
	// Published is true when the fleet's share repo holds a file of this name.
	Published bool `json:"published"`
	// SharedURL is the page of the upstream file at the commit the member was imported from.
	SharedURL *string `json:"shared_url"`
	// Update is the last update check's answer: none, available, removed or unknown.
	Update string `json:"update"`
}

type unclaimedView struct {
	Member string    `json:"member"`
	Notes  notesView `json:"notes"`
}

func (s *Service) serveCrew(w http.ResponseWriter, r *http.Request) {
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return
	}
	res := crew.Resolve(c.layers(true))
	warns := res.Warnings
	if warns == nil {
		warns = []crew.Diagnostic{}
	}
	errs := res.Errors
	if errs == nil {
		errs = []crew.Diagnostic{}
	}
	out := struct {
		Harness   string            `json:"harness"`
		Members   []memberView      `json:"members"`
		Unclaimed []unclaimedView   `json:"unclaimed"`
		Warnings  []crew.Diagnostic `json:"warnings"`
		Errors    []crew.Diagnostic `json:"errors"`
	}{c.harness, []memberView{}, []unclaimedView{}, warns, errs}
	if res.Exit != 0 {
		writeJSON(w, http.StatusOK, out)
		return
	}
	builtIns := c.builtIns()
	memory := filepath.Join(c.crewDir(), "memory")
	memoryOK := memoryDir(memory, &out.Warnings)
	listed := map[string]bool{}
	published := s.publishedNames(r, c)
	for _, m := range res.Members {
		listed[m.Name] = true
		v := memberView{m.Name, m.Role, m.BasedOn, m.Skills, m.Model, m.Harness, m.PickMeWhen, m.Instructions, m.Layer,
			m.Layer == crew.LayerFleet && builtIns[m.Name].Name != "", notesView{[]string{}, map[string][]string{}},
			m.Shared, unreadableSidecar(warns, m), published[m.Name], sharedURL(c.crewDir(), m), s.updateStateOf(c, m)}
		if memoryOK && m.Role != crew.FixChecker {
			v.Notes.Fleet, v.Notes.Repos = crew.MemberNotes(memory, m.Name, &out.Warnings)
		}
		out.Members = append(out.Members, v)
	}
	if memoryOK {
		out.Unclaimed = unclaimed(memory, listed, &out.Warnings)
	}
	writeJSON(w, http.StatusOK, out)
}

func unreadableSidecar(warns []crew.Diagnostic, m crew.Member) bool {
	for _, d := range warns {
		if d.Code == "shared-unreadable" && d.Layer == m.Layer && d.File != nil && *d.File == m.Name+crew.SidecarSuffix {
			return true
		}
	}
	return false
}

// memoryDir reports whether the memory directory can be read, with a warning for one that is a link or a file.
func memoryDir(path string, warns *[]crew.Diagnostic) bool {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return false
	case err != nil:
		*warns = append(*warns, memoryDiag("memory-unreadable", "", "the memory root can't be read: "+err.Error()))
		return false
	case !fi.IsDir():
		*warns = append(*warns, memoryDiag("memory-refused", "", "the memory root is not a directory, skipped"))
		return false
	}
	return true
}

func memoryDiag(code, file, message string) crew.Diagnostic {
	d := crew.Diagnostic{Layer: crew.LayerMemory, Code: code, Message: message}
	if file != "" {
		d.File = &file
	}
	return d
}

// unclaimed lists the memory directories no listed member has a name for, when they hold a note.
func unclaimed(memory string, listed map[string]bool, warns *[]crew.Diagnostic) []unclaimedView {
	out := []unclaimedView{}
	entries, err := os.ReadDir(memory)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !crew.ValidName(name) {
			*warns = append(*warns, memoryDiag("memory-refused", name, name+" is not a member name, skipped"))
			continue
		}
		if listed[name] {
			continue
		}
		if fleet, repos := crew.MemberNotes(memory, name, warns); len(fleet) > 0 || len(repos) > 0 {
			out = append(out, unclaimedView{name, notesView{fleet, repos}})
		}
	}
	return out
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

// noAccessText is the answer for a repo the fleet's login can't read; GitHub says 404 for a missing repo and a private one alike.
func noAccessText(repo string) string {
	return "You need access to " + repo + ". Ask its owner to add you."
}

// writeNoAccess answers 403 with the no-access text and its code, the one answer that carries a code.
func writeNoAccess(w http.ResponseWriter, repo string) {
	writeJSON(w, http.StatusForbidden, map[string]string{"error": noAccessText(repo), "code": "no_access"})
}

// repoUnreadable asks gh.Repo after a contents 404 whether the repo itself is the thing GitHub can't find, and answers no access if so.
// A readable repo, or any other error from gh.Repo, leaves the route's own answer to give.
func repoUnreadable(w http.ResponseWriter, ctx context.Context, gh *github.Client, owner, name string) bool {
	if _, err := gh.Repo(ctx, owner, name); github.HTTPStatus(err) == http.StatusNotFound {
		writeNoAccess(w, owner+"/"+name)
		return true
	}
	return false
}

// refuse answers a failed file operation: a link or odd file in the path is a refusal, anything else a server error.
func refuse(w http.ResponseWriter, err error) {
	if errors.Is(err, errLink) || errors.Is(err, errNotDir) || errors.Is(err, errUnreadable) {
		writeError(w, http.StatusConflict, err.Error()+"; nothing changed")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return false
	}
	return true
}

// serveSkills lists the user and plugin skills of the fleet's docks, never a checkout's.
func (s *Service) serveSkills(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.fleetCrew(w, r); !ok {
		return
	}
	fleet, _ := s.Config.Current().Fleet(r.PathValue("key"))
	writeJSON(w, http.StatusOK, map[string][]skillindex.Entry{"skills": fleetSkills(fleet, r.URL.Query().Get("harness"))})
}

// fleetSkills is the skills the fleet's docks can see, narrowed to one dock when harness is set, with only names a crew file may list.
func fleetSkills(fleet config.Fleet, harness string) []skillindex.Entry {
	docks := fleet.Harnesses
	if harness != "" {
		dock, _ := fleet.Harness(harness)
		docks = []config.Harness{dock}
	}
	lists := make([]skillindex.HarnessSkills, 0, len(docks))
	for _, d := range docks {
		lists = append(lists, skillindex.HarnessSkills{Harness: d.Name, Skills: skillindex.List(d.Name, d.Environ(), "")})
	}
	skills := []skillindex.Entry{}
	for _, e := range skillindex.Merge(lists) {
		if crew.ValidSkillName(e.Name) {
			skills = append(skills, e)
		}
	}
	return skills
}
