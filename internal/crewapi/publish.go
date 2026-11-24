package crewapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/gitleaks"
)

const (
	offText      = "Crew sharing is off for this fleet. Turn it on in Preferences."
	connectText  = "Connect GitHub (github.com) for this fleet first."
	importText   = "Imported members can't be shared. Make it yours first."
	changedText  = "The member changed since the preview. Preview it again."
	fileChanged  = "The published file changed since the preview. Preview it again."
	noRepoText   = "Choose a share repo first."
	defaultRepo  = "admiral-crew"
	crewDir      = "crew"
	listingTTL   = 5 * time.Minute
	listingErrTT = 30 * time.Second
)

var (
	blobPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sumPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const readme = "# Shared crew\n\nCrew members published from Admiral. Each file in crew/ is one member; import one into a fleet from its Crew page.\n"

// publishRoutes registers the publish and Unpublish routes.
func (s *Service) publishRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fleets/{key}/crew/sharing", s.serveSharing)
	mux.HandleFunc("POST /api/fleets/{key}/crew/sharing/repo", s.serveShareRepo)
	mux.HandleFunc("POST /api/fleets/{key}/crew/members/{name}/share/preview", s.servePreview)
	mux.HandleFunc("POST /api/fleets/{key}/crew/members/{name}/share", s.serveShare)
	mux.HandleFunc("GET /api/fleets/{key}/crew/published/{name}", s.servePublished)
	mux.HandleFunc("DELETE /api/fleets/{key}/crew/published/{name}", s.serveUnpublish)
}

// listing is one fleet's share repo crew/ directory as last read.
type listing struct {
	repo    string
	entries []github.DirEntry
	err     string
	at      time.Time
}

func (l listing) fresh() bool {
	ttl := listingTTL
	if l.err != "" {
		ttl = listingErrTT
	}
	return time.Since(l.at) < ttl
}

func (l listing) blob(name string) (string, bool) {
	for _, e := range l.entries {
		if e.Name == name+".md" {
			return e.Blob, true
		}
	}
	return "", false
}

// listings caches each fleet's published listing; Share and Unpublish clear a fleet's entry.
type listings struct {
	mu sync.Mutex
	by map[string]listing
}

func (l *listings) get(fleet, repo string) (listing, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.by[fleet]
	return v, ok && v.repo == repo && v.fresh()
}

func (l *listings) put(fleet string, v listing) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.by == nil {
		l.by = map[string]listing{}
	}
	v.at = time.Now()
	l.by[fleet] = v
}

func (l *listings) clear(fleet string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.by, fleet)
}

// sharing is what one publish request works on.
type sharing struct {
	c      fleetCrew
	fleet  config.Fleet
	gh     *github.Client
	repo   string // <owner>/<name>, empty when the fleet has none
	owner  string
	name   string
	cfg    *config.Config
	member string
}

// shareFleet resolves the fleet and its GitHub client. needOn refuses a fleet with sharing off; it answers for itself when it can't continue.
func (s *Service) shareFleet(w http.ResponseWriter, r *http.Request, needOn bool) (*sharing, bool) {
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return nil, false
	}
	cfg := s.Config.Current()
	fleet, _ := cfg.Fleet(c.key)
	if needOn && !fleet.CrewSharing.Enabled {
		writeError(w, http.StatusForbidden, offText)
		return nil, false
	}
	sh := &sharing{c: c, fleet: fleet, cfg: cfg, repo: fleet.CrewSharing.ShareRepo, gh: s.client(cfg, c.key)}
	if sh.gh == nil {
		writeError(w, http.StatusConflict, connectText)
		return nil, false
	}
	sh.owner, sh.name, _ = strings.Cut(sh.repo, "/")
	return sh, true
}

// client is the fleet's GitHub client when it is for github.com, else nil.
func (s *Service) client(cfg *config.Config, fleet string) *github.Client {
	if s.GitHub == nil {
		return nil
	}
	if gh := s.GitHub(cfg, fleet); gh != nil && gh.Host() == github.DefaultHost {
		return gh
	}
	return nil
}

// needRepo answers 409 when the fleet has no share repo.
func (sh *sharing) needRepo(w http.ResponseWriter) bool {
	if sh.repo == "" {
		writeError(w, http.StatusConflict, noRepoText)
		return false
	}
	return true
}

// ghFailed answers a failed GitHub call: step names what Admiral was doing.
func ghFailed(w http.ResponseWriter, step, repo string, err error) {
	var signIn *github.SignInError
	switch status := github.HTTPStatus(err); {
	case errors.As(err, &signIn):
		msg := step + " failed: " + signIn.Message
		if signIn.Detail != "" {
			msg += " (" + signIn.Detail + ")"
		}
		writeError(w, http.StatusBadGateway, msg+". Reconnect GitHub for this fleet and try again.")
	case github.RateLimited(err):
		writeError(w, http.StatusTooManyRequests, "GitHub's rate limit stopped "+step+" on "+repo+". Try again later.")
	case status == http.StatusUnauthorized:
		writeError(w, http.StatusBadGateway, step+" failed: GitHub refused the sign-in. Reconnect GitHub for this fleet and try again.")
	case status == http.StatusNotFound:
		writeError(w, http.StatusNotFound, step+" failed: "+repo+" not found.")
	default:
		writeError(w, http.StatusBadGateway, step+" failed: "+err.Error())
	}
}

// repoProblem says why a share repo can't be published to, or "" when it can.
func repoProblem(info github.RepoInfo) string {
	switch {
	case !info.Public:
		return "is private"
	case info.Archived:
		return "is archived"
	case !info.Pushable:
		return "doesn't let you push to it"
	}
	return ""
}

// checkRepo reads the share repo's settings and answers for a repo that can't be published to.
func (sh *sharing) checkRepo(w http.ResponseWriter, ctx context.Context, step string) bool {
	info, err := sh.gh.Repo(ctx, sh.owner, sh.name)
	switch {
	case github.HTTPStatus(err) == http.StatusNotFound:
		writeError(w, http.StatusConflict, "The share repo "+sh.repo+" was not found. Choose another repo.")
		return false
	case err != nil:
		ghFailed(w, step, sh.repo, err)
		return false
	}
	if why := repoProblem(info); why != "" {
		writeError(w, http.StatusConflict, "The share repo "+sh.repo+" "+why+". Choose another repo.")
		return false
	}
	return true
}

// fetchListing reads the share repo's crew/ directory afresh and caches the answer. A missing directory is an empty listing.
func (s *Service) fetchListing(ctx context.Context, sh *sharing) (listing, error) {
	entries, err := sh.gh.ListDir(ctx, sh.owner, sh.name, crewDir)
	if github.HTTPStatus(err) == http.StatusNotFound {
		entries, err = nil, nil
	}
	l := listing{repo: sh.repo, entries: entries}
	if err != nil {
		l.err = err.Error()
	}
	s.listings.put(sh.c.key, l)
	return l, err
}

// publishedListing is the cached listing, read afresh unless the request is a non-engineer actor's, which never calls GitHub.
func (s *Service) publishedListing(ctx context.Context, sh *sharing) (listing, bool) {
	if sh.repo == "" {
		return listing{}, false
	}
	if l, ok := s.listings.get(sh.c.key, sh.repo); ok {
		return l, true
	}
	if _, bot := actor.FleetOf(ctx); bot {
		return listing{}, false
	}
	l, _ := s.fetchListing(ctx, sh)
	return l, true
}

// publishedNames is the set of members the fleet's share repo holds, empty when there is none or it can't be read.
func (s *Service) publishedNames(r *http.Request, c fleetCrew) map[string]bool {
	out := map[string]bool{}
	cfg := s.Config.Current()
	fleet, _ := cfg.Fleet(c.key)
	gh := s.client(cfg, c.key)
	if gh == nil || fleet.CrewSharing.ShareRepo == "" {
		return out
	}
	sh := &sharing{c: c, fleet: fleet, gh: gh, repo: fleet.CrewSharing.ShareRepo}
	sh.owner, sh.name, _ = strings.Cut(sh.repo, "/")
	if l, ok := s.publishedListing(r.Context(), sh); ok && l.err == "" {
		for _, e := range l.entries {
			out[strings.TrimSuffix(e.Name, ".md")] = true
		}
	}
	return out
}

type sharingView struct {
	CrewSharing struct {
		Enabled   bool     `json:"enabled"`
		ShareRepo string   `json:"share_repo"`
		Sources   []string `json:"sources"`
	} `json:"crew_sharing"`
	Connected      bool      `json:"connected"`
	Login          string    `json:"login"`
	LoginError     string    `json:"login_error"`
	Repo           *repoView `json:"repo"`
	Published      []string  `json:"published"`
	PublishedError string    `json:"published_error"`
}

type repoView struct {
	Exists   bool   `json:"exists"`
	Public   bool   `json:"public"`
	Archived bool   `json:"archived"`
	Pushable bool   `json:"pushable"`
	Error    string `json:"error"`
}

// serveSharing answers the Crew page's sharing state; a GitHub failure goes in a field, never in the status.
func (s *Service) serveSharing(w http.ResponseWriter, r *http.Request) {
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return
	}
	cfg := s.Config.Current()
	fleet, _ := cfg.Fleet(c.key)
	view := sharingView{Published: []string{}}
	view.CrewSharing.Enabled = fleet.CrewSharing.Enabled
	view.CrewSharing.ShareRepo = fleet.CrewSharing.ShareRepo
	view.CrewSharing.Sources = append([]string{}, fleet.CrewSharing.Sources...)
	gh := s.client(cfg, c.key)
	if gh == nil {
		writeJSON(w, http.StatusOK, view)
		return
	}
	view.Connected = true
	login, err := gh.Login(r.Context())
	view.Login = login
	if err != nil {
		view.LoginError = err.Error()
	}
	sh := &sharing{c: c, fleet: fleet, gh: gh, repo: fleet.CrewSharing.ShareRepo}
	if sh.repo != "" {
		sh.owner, sh.name, _ = strings.Cut(sh.repo, "/")
		view.Repo = &repoView{}
		info, err := gh.Repo(r.Context(), sh.owner, sh.name)
		switch {
		case github.HTTPStatus(err) == http.StatusNotFound:
		case err != nil:
			view.Repo.Error = err.Error()
		default:
			view.Repo = &repoView{Exists: true, Public: info.Public, Archived: info.Archived, Pushable: info.Pushable}
		}
		if l, ok := s.publishedListing(r.Context(), sh); ok {
			view.PublishedError = l.err
			for _, e := range l.entries {
				view.Published = append(view.Published, strings.TrimSuffix(e.Name, ".md"))
			}
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// serveShareRepo creates the share repo, or adopts an existing one, and saves it as the fleet's.
func (s *Service) serveShareRepo(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.shareFleet(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Repo        string `json:"repo"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx := r.Context()
	if body.Repo != "" {
		if !github.ValidRepo(body.Repo) {
			writeError(w, http.StatusBadRequest, body.Repo+" is not <owner>/<name>")
			return
		}
		sh.repo = body.Repo
		sh.owner, sh.name, _ = strings.Cut(body.Repo, "/")
		if !sh.checkRepo(w, ctx, "Checking the share repo") {
			return
		}
		s.saveShareRepo(w, sh.c.key, body.Repo)
		return
	}
	if body.Name == "" {
		body.Name = defaultRepo
	}
	if !github.ValidSegment(body.Name) {
		writeError(w, http.StatusBadRequest, body.Name+" is not a repository name")
		return
	}
	full, err := sh.gh.CreateRepo(ctx, body.Name, body.Description)
	if github.HTTPStatus(err) == http.StatusUnprocessableEntity {
		s.nameTaken(w, sh, body.Name)
		return
	}
	if err != nil {
		ghFailed(w, "Creating the share repo", body.Name, err)
		return
	}
	owner, name, _ := strings.Cut(full, "/")
	if _, err := sh.gh.PutFile(ctx, owner, name, "README.md", "Add README", []byte(readme), ""); err != nil {
		ghFailed(w, "Adding the README to "+full, full, err)
		return
	}
	s.saveShareRepo(w, sh.c.key, full)
}

// nameTaken answers a create that GitHub refused: offers the account's own repo of that name when it can be used.
func (s *Service) nameTaken(w http.ResponseWriter, sh *sharing, name string) {
	ctx := context.Background()
	login, err := sh.gh.Login(ctx)
	if err != nil {
		ghFailed(w, "Creating the share repo", name, err)
		return
	}
	full := login + "/" + name
	info, err := sh.gh.Repo(ctx, login, name)
	switch {
	case github.HTTPStatus(err) == http.StatusNotFound:
		writeError(w, http.StatusConflict, "GitHub refused to create "+full+". Choose another name.")
	case err != nil:
		ghFailed(w, "Checking "+full, full, err)
	case repoProblem(info) != "":
		writeError(w, http.StatusConflict, full+" already exists and "+repoProblem(info)+". Choose another name.")
	default:
		writeJSON(w, http.StatusConflict, map[string]string{"error": full + " already exists. Use it, or choose another name.", "use_existing": full})
	}
}

func (s *Service) saveShareRepo(w http.ResponseWriter, fleet, repo string) {
	raw, _ := json.Marshal(repo)
	err := s.Config.SetFleetFields(fleet, config.FleetFields{CrewSharing: map[string]json.RawMessage{"share_repo": raw}})
	switch {
	case errors.Is(err, config.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]string{"share_repo": repo})
	}
}

// shareable is the member's canonical file, or the answer to give for a member that can't be published.
func (s *Service) shareable(w http.ResponseWriter, sh *sharing, name string) ([]byte, bool) {
	res := crew.Resolve(sh.c.layers(true))
	var found *crew.Member
	for i := range res.Members {
		if res.Members[i].Name == name {
			found = &res.Members[i]
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound, "crew member "+name+" not found")
		return nil, false
	}
	present, err := SidecarPresent(sh.c.crewDir(), name)
	switch {
	case err != nil:
		refuse(w, err)
		return nil, false
	case found.Layer != crew.LayerFleet:
		writeError(w, http.StatusConflict, "Built-in members can't be shared.")
		return nil, false
	case present || found.Shared != nil:
		writeError(w, http.StatusConflict, importText)
		return nil, false
	}
	data := CanonicalMemberBytes(*found)
	if _, ferr := crew.CheckFile(data, name); ferr != nil {
		writeError(w, http.StatusUnprocessableEntity, ferr.Message)
		return nil, false
	}
	home, _ := os.UserHomeDir()
	paths := PublishPaths{AdmiralHome: sh.c.home, Home: home, CloneRoot: sh.fleet.CloneRoot, SearchRoots: sh.fleet.SearchRoots}
	if ref := CheckPublish(data, paths); ref != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": ref.Message, "check": ref.Check, "line": ref.Line})
		return nil, false
	}
	return data, true
}

func (s *Service) scan(ctx context.Context, sh *sharing, data []byte) gitleaks.Result {
	return gitleaks.Runner{Env: sh.fleet.Environ(), Timeout: s.ScanTimeout}.Scan(ctx, data)
}

// scanRefusal answers a scan that found a secret or failed, with the rules and lines but never the text.
func scanRefusal(w http.ResponseWriter, res gitleaks.Result) {
	msg := "The secret scan failed: " + res.Error
	if res.State == gitleaks.Hit {
		var found []string
		for _, f := range res.Findings {
			found = append(found, fmt.Sprintf("%s on line %d", f.Rule, f.Line))
		}
		msg = "The secret scan found " + strings.Join(found, ", ") + ". Remove it and preview again."
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": msg, "scan": res})
}

func sumOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) servePreview(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.shareFleet(w, r, true)
	if !ok || !sh.needRepo(w) {
		return
	}
	name, ok := memberName(w, r)
	if !ok {
		return
	}
	data, ok := s.shareable(w, sh, name)
	if !ok {
		return
	}
	ctx := r.Context()
	if !sh.checkRepo(w, ctx, "Checking the share repo") {
		return
	}
	l, err := s.fetchListing(ctx, sh)
	if err != nil {
		ghFailed(w, "Listing the published crew", sh.repo, err)
		return
	}
	out := map[string]any{"name": name, "path": crewDir + "/" + name + ".md", "repo": sh.repo, "bytes": string(data), "sha256": sumOf(data), "replaces_blob": nil}
	if blob, replaces := l.blob(name); replaces {
		old, err := sh.gh.ReadFile(ctx, sh.owner, sh.name, crewDir+"/"+name+".md")
		if err != nil {
			ghFailed(w, "Reading the published "+name, sh.repo, err)
			return
		}
		out["replaces_blob"] = blob
		out["published_bytes"] = string(old.Bytes)
	}
	res := s.scan(ctx, sh, data)
	out["scan"] = res
	if res.State == gitleaks.Hit || res.State == gitleaks.Failed {
		scanRefusal(w, res)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) serveShare(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.shareFleet(w, r, true)
	if !ok || !sh.needRepo(w) {
		return
	}
	name, ok := memberName(w, r)
	if !ok {
		return
	}
	var body struct {
		SHA256         string  `json:"sha256"`
		Repo           string  `json:"repo"`
		ReplacesBlob   *string `json:"replaces_blob"`
		SkipSecretScan bool    `json:"skip_secret_scan"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !sumPattern.MatchString(body.SHA256) || (body.ReplacesBlob != nil && !blobPattern.MatchString(*body.ReplacesBlob)) {
		writeError(w, http.StatusBadRequest, "sha256 and replaces_blob must come from a preview")
		return
	}
	if body.Repo != sh.repo {
		writeError(w, http.StatusConflict, "The share repo changed since the preview. Preview it again.")
		return
	}
	data, ok := s.shareable(w, sh, name)
	if !ok {
		return
	}
	ctx := r.Context()
	res := s.scan(ctx, sh, data)
	usable := res.State != gitleaks.Missing && res.State != gitleaks.TooOld
	switch {
	case body.SkipSecretScan && usable:
		writeError(w, http.StatusBadRequest, "skip_secret_scan is only for a missing or too old gitleaks")
		return
	case sumOf(data) != body.SHA256:
		writeError(w, http.StatusConflict, changedText)
		return
	case res.State == gitleaks.Hit || res.State == gitleaks.Failed:
		scanRefusal(w, res)
		return
	case !usable && !body.SkipSecretScan:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "No secret scan ran. Install gitleaks: " + res.Install, "scan": res})
		return
	}
	blob := ""
	if body.ReplacesBlob != nil {
		blob = *body.ReplacesBlob
	}
	url, err := sh.gh.PutFile(ctx, sh.owner, sh.name, crewDir+"/"+name+".md", "Share crew member "+name, data, blob)
	s.listings.clear(sh.c.key)
	if status := github.HTTPStatus(err); status == http.StatusConflict || status == http.StatusUnprocessableEntity {
		writeError(w, http.StatusConflict, fileChanged)
		return
	} else if err != nil {
		ghFailed(w, "Sharing "+name, sh.repo, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "repo": sh.repo, "path": crewDir + "/" + name + ".md", "commit_url": url})
}

func (s *Service) servePublished(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.shareFleet(w, r, false)
	if !ok || !sh.needRepo(w) {
		return
	}
	name, ok := memberName(w, r)
	if !ok {
		return
	}
	file, err := sh.gh.ReadFile(r.Context(), sh.owner, sh.name, crewDir+"/"+name+".md")
	switch {
	case github.HTTPStatus(err) == http.StatusNotFound:
		writeError(w, http.StatusNotFound, name+" is not published in "+sh.repo)
	case errors.Is(err, github.ErrTooLarge):
		writeError(w, http.StatusUnprocessableEntity, "The published "+name+" is over 64 KiB, so Admiral won't show it.")
	case err != nil:
		ghFailed(w, "Reading the published "+name, sh.repo, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"name": name, "repo": sh.repo, "bytes": string(file.Bytes), "blob": file.Blob})
	}
}

func (s *Service) serveUnpublish(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.shareFleet(w, r, false)
	if !ok || !sh.needRepo(w) {
		return
	}
	name, ok := memberName(w, r)
	if !ok {
		return
	}
	var body struct {
		Blob string `json:"blob"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !blobPattern.MatchString(body.Blob) {
		writeError(w, http.StatusBadRequest, "blob must be the published file's blob from its GET")
		return
	}
	url, err := sh.gh.DeleteFile(r.Context(), sh.owner, sh.name, crewDir+"/"+name+".md", "Unpublish crew member "+name, body.Blob)
	s.listings.clear(sh.c.key)
	switch status := github.HTTPStatus(err); {
	case status == http.StatusConflict || status == http.StatusUnprocessableEntity:
		writeError(w, http.StatusConflict, "The published file changed since you looked. Reload and try again.")
	case err != nil:
		ghFailed(w, "Unpublishing "+name, sh.repo, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"name": name, "repo": sh.repo, "commit_url": url})
	}
}
