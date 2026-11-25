package crewapi

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/delay"
	"github.com/jbuffin/admiral/internal/github"
)

const (
	updateNone      = "none"
	updateAvailable = "available"
	updateRemoved   = "removed"
	updateUnknown   = "unknown"
	updateNoAccess  = "no_access"

	updateEvery = 24 * time.Hour
	// resultTTL is how long the page-open check reuses the last result.
	resultTTL = 5 * time.Minute
)

// Clock is the update checker's source of time; tests drive it by hand.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (s *Service) clock() Clock {
	if s.Clock != nil {
		return s.Clock
	}
	return realClock{}
}

func (s *Service) now() time.Time { return s.clock().Now() }

// updateResult is one fleet's last check: each attached member's state.
type updateResult struct {
	at     time.Time
	states map[string]string
}

// updateStore keeps each fleet's last check in memory; a restart starts with none.
type updateStore struct {
	mu  sync.Mutex
	by  map[string]updateResult
	seq int
	// setAt is the seq of each member's latest set, so a check that began earlier doesn't undo it.
	setAt map[string]map[string]int
}

// begin marks the start of a check for put.
func (u *updateStore) begin() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.seq
}

func (u *updateStore) get(fleet string) (updateResult, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	res, ok := u.by[fleet]
	if ok {
		states := make(map[string]string, len(res.states))
		for k, v := range res.states {
			states[k] = v
		}
		res.states = states
	}
	return res, ok
}

// put stores a check that began at began, keeping any member state set since, and returns what it stored.
func (u *updateStore) put(fleet string, at time.Time, states map[string]string, began int) map[string]string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.by == nil {
		u.by = map[string]updateResult{}
	}
	for name, seq := range u.setAt[fleet] {
		if kept, ok := u.by[fleet].states[name]; ok && seq > began {
			if _, checked := states[name]; checked {
				states[name] = kept
			}
		}
	}
	u.by[fleet] = updateResult{at: at, states: states}
	return states
}

// set changes one member's state and leaves the check's age alone.
func (u *updateStore) set(fleet, name, state string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.by == nil {
		u.by = map[string]updateResult{}
	}
	res := u.by[fleet]
	states := make(map[string]string, len(res.states)+1)
	for k, v := range res.states {
		states[k] = v
	}
	states[name] = state
	u.by[fleet] = updateResult{at: res.at, states: states}
	u.seq++
	if u.setAt == nil {
		u.setAt = map[string]map[string]int{}
	}
	if u.setAt[fleet] == nil {
		u.setAt[fleet] = map[string]int{}
	}
	u.setAt[fleet][name] = u.seq
}

func (u *updateStore) state(fleet, name string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if s, ok := u.by[fleet].states[name]; ok {
		return s
	}
	return updateUnknown
}

// updateStateOf is what GET crew shows: the last check's answer, never a GitHub call.
func (s *Service) updateStateOf(c fleetCrew, m crew.Member) string {
	if m.Layer != crew.LayerFleet || m.Shared == nil || m.Shared.Detached {
		return updateNone
	}
	if readSidecar(c.crewDir(), m.Name).kind != usableSidecar {
		return updateNone
	}
	return s.updates.state(c.key, m.Name)
}

type attachedMember struct {
	name string
	side sidecarRead
}

// attachedMembers are the fleet's shared members that still follow their upstream, by name.
func attachedMembers(dir string) []attachedMember {
	var out []attachedMember
	for _, name := range sidecarStems(dir) {
		if side := readSidecar(dir, name); side.kind == usableSidecar && !side.shared.Detached {
			out = append(out, attachedMember{name, side})
		}
	}
	return out
}

// runCheck lists each repo the attached members came from once, and the share repo when withShare is set, and stores each member's state.
func (s *Service) runCheck(ctx context.Context, sh *sharing, withShare bool) map[string]string {
	began := s.updates.begin()
	members := attachedMembers(sh.c.crewDir())
	var repos []string // first-seen spelling, one per repo ignoring case
	byRepo := map[string][]attachedMember{}
	add := func(repo string) string {
		key := strings.ToLower(repo)
		if _, seen := byRepo[key]; !seen {
			byRepo[key] = nil
			repos = append(repos, repo)
		}
		return key
	}
	for _, m := range members {
		key := add(m.side.repo)
		byRepo[key] = append(byRepo[key], m)
	}
	if withShare && sh.repo != "" {
		add(sh.repo)
	}
	states := map[string]string{}
	for _, repo := range repos {
		l := s.repoListing(ctx, sh.gh, sh.c.key, repo)
		if l.err != "" {
			log.Printf("crew updates: reading %s failed: %s", repo, l.err)
		}
		for _, m := range byRepo[strings.ToLower(repo)] {
			blob, listed := l.blob(m.side.stem)
			switch {
			case l.noAccess:
				states[m.name] = updateNoAccess
			case l.err != "" || l.notFound:
				states[m.name] = updateUnknown
			case !listed:
				states[m.name] = updateRemoved
			case blob != m.side.blob:
				states[m.name] = updateAvailable
			default:
				states[m.name] = updateNone
			}
		}
	}
	return s.updates.put(sh.c.key, s.now(), states, began)
}

// Run is the background update check: the first after a delay, then daily. It blocks until ctx ends.
func (s *Service) Run(ctx context.Context) {
	clk := s.clock()
	wait := delay.From("ADMIRAL_CREW_UPDATE_FIRST_CHECK", 2*time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-clk.After(wait):
		}
		s.checkFleets(ctx)
		wait = updateEvery
	}
}

func (s *Service) checkFleets(ctx context.Context) {
	home, err := filepath.Abs(s.Home)
	if err != nil {
		log.Printf("crew updates: %v", err)
		return
	}
	cfg := s.Config.Current()
	for _, f := range cfg.Fleets {
		gh := s.client(cfg, f.Key)
		if gh == nil {
			continue
		}
		s.runCheck(ctx, newSharing(cfg, fleetCrew{home: home, key: f.Key}, gh), true)
	}
}

// updateRoutes registers the update check, preview and Apply routes.
func (s *Service) updateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fleets/{key}/crew/updates", s.serveUpdates)
	mux.HandleFunc("POST /api/fleets/{key}/crew/members/{name}/update/preview", s.servePreviewUpdate)
	mux.HandleFunc("POST /api/fleets/{key}/crew/members/{name}/update", s.serveApplyUpdate)
}

// serveUpdates checks the fleet as the page opens, reusing a result younger than resultTTL that covers every attached member.
func (s *Service) serveUpdates(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.importFleet(w, r, false)
	if !ok {
		return
	}
	members := attachedMembers(sh.c.crewDir())
	states := map[string]string{}
	if len(members) > 0 {
		if sh.gh == nil {
			writeError(w, http.StatusConflict, connectText)
			return
		}
		states = s.reusableResult(sh.c.key, members)
		if states == nil {
			states = s.runCheck(r.Context(), sh, true)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"updates": states})
}

func (s *Service) reusableResult(fleet string, members []attachedMember) map[string]string {
	res, ok := s.updates.get(fleet)
	if !ok || s.now().Sub(res.at) >= resultTTL {
		return nil
	}
	out := map[string]string{}
	for _, m := range members {
		state, have := res.states[m.name]
		if !have {
			return nil
		}
		if state == updateUnknown || state == updateNoAccess {
			return nil // a failed or unreadable listing is worth asking again
		}
		out[m.name] = state
	}
	return out
}

// attachedFor answers for a member that has no usable upstream to update from or whose fleet has no github.com client.
func (s *Service) attachedFor(w http.ResponseWriter, sh *sharing, name string) (sidecarRead, bool) {
	side := readSidecar(sh.c.crewDir(), name)
	switch {
	case side.kind == noSidecar:
		writeError(w, http.StatusNotFound, name+" is not an imported member, so it has no updates.")
	case side.kind == unusableSide:
		writeError(w, http.StatusConflict, "The sidecar of "+name+" can't be used: "+side.problem+". Nothing changed.")
	case side.shared.Detached:
		writeError(w, http.StatusConflict, editedText(side.repo))
	case sh.gh == nil:
		writeError(w, http.StatusConflict, connectText)
	default:
		return side, true
	}
	return side, false
}

func editedText(repo string) string {
	return "Edited here, so it no longer gets updates from " + repo + "."
}

// readUpdate reads the member's upstream file at commit, its tag and the bytes as this member's name, checking each as import does.
// wantSum, when set, must match the raw upstream bytes.
func (s *Service) readUpdate(w http.ResponseWriter, ctx context.Context, sh *sharing, side sidecarRead, name, commit, wantSum string) (file github.File, data []byte, member crew.Member, tag string, ok bool) {
	owner, repo, _ := strings.Cut(side.repo, "/")
	file, member, ok = readImportable(w, ctx, sh.gh, owner, repo, side.path, commit, side.path, side.stem, wantSum)
	if !ok {
		return file, nil, member, "", false
	}
	tag, err := sh.gh.TagAt(ctx, owner, repo, commit)
	if err != nil {
		ghFailed(w, "Reading the tags of "+side.repo, side.repo, err)
		return file, nil, member, "", false
	}
	data = file.Bytes
	if name != side.stem {
		data = renamed(data, name)
		var problem string
		if member, problem = checkImported(data, name); problem != "" {
			writeError(w, http.StatusUnprocessableEntity, problem)
			return file, nil, member, "", false
		}
	}
	return file, data, member, usableTag(tag), true
}

func (s *Service) servePreviewUpdate(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.importFleet(w, r, false)
	if !ok {
		return
	}
	name, ok := memberName(w, r)
	if !ok {
		return
	}
	side, ok := s.attachedFor(w, sh, name)
	if !ok {
		return
	}
	owner, repo, _ := strings.Cut(side.repo, "/")
	ctx := r.Context()
	info, err := sh.gh.Repo(ctx, owner, repo)
	if github.HTTPStatus(err) == http.StatusNotFound {
		writeNoAccess(w, side.repo)
		return
	}
	if err != nil {
		ghFailed(w, "Reading "+side.repo, side.repo, err)
		return
	}
	commit, err := sh.gh.ResolveCommit(ctx, owner, repo, info.DefaultBranch)
	if err != nil {
		notFoundIn(w, "Reading "+side.repo, side.repo, "The branch "+info.DefaultBranch, err)
		return
	}
	file, data, member, tag, ok := s.readUpdate(w, ctx, sh, side, name, commit, "")
	if !ok {
		return
	}
	var tagOut any
	if tag != "" {
		tagOut = tag
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": name, "repo": side.repo, "path": side.path,
		"current_bytes": string(side.member), "current_sha256": sumOf(side.member),
		"bytes": string(data), "sha256": sumOf(file.Bytes), "commit": commit, "tag": tagOut, "blob": file.Blob,
		"up_to_date": file.Blob == side.blob, "card": cardOf(member),
		"missing_skills": missingSkills(sh.fleet, r.URL.Query().Get("harness"), member.Skills), "scan": s.scan(ctx, sh, data),
	})
}

func (s *Service) serveApplyUpdate(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.importFleet(w, r, false)
	if !ok {
		return
	}
	name, ok := memberName(w, r)
	if !ok {
		return
	}
	var body struct {
		Commit        string `json:"commit"`
		SHA256        string `json:"sha256"`
		CurrentSHA256 string `json:"current_sha256"`
	}
	if !decode(w, r, &body) {
		return
	}
	switch {
	case !blobPattern.MatchString(body.Commit):
		writeError(w, http.StatusBadRequest, "commit must come from a preview")
		return
	case !sumPattern.MatchString(body.SHA256):
		writeError(w, http.StatusBadRequest, "sha256 must come from a preview")
		return
	case !sumPattern.MatchString(body.CurrentSHA256):
		writeError(w, http.StatusBadRequest, "current_sha256 must come from a preview")
		return
	}
	side, ok := s.attachedFor(w, sh, name)
	if !ok {
		return
	}
	file, data, member, tag, ok := s.readUpdate(w, r.Context(), sh, side, name, body.Commit, body.SHA256)
	if !ok {
		return
	}

	writeMu.Lock()
	defer writeMu.Unlock()
	dir, found, err := walk(sh.c.home, false, "fleets", sh.c.key, crewDir)
	if err != nil {
		refuseImport(w, unsafePath(err))
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, name+" is not an imported member, so it has no updates.")
		return
	}
	now := readSidecar(dir, name)
	switch {
	case now.kind != usableSidecar:
		writeError(w, http.StatusConflict, "The sidecar of "+name+" changed since the preview. Preview it again.")
		return
	case now.shared.Detached:
		writeError(w, http.StatusConflict, editedText(now.repo))
		return
	case !strings.EqualFold(now.repo, side.repo) || now.path != side.path || now.stem != side.stem,
		sumOf(now.member) != body.CurrentSHA256:
		writeJSON(w, http.StatusConflict, map[string]any{"error": changedText, "kind": "changed"})
		return
	}
	if err := writeAtomic(dir, name+".md", data); err != nil {
		refuse(w, err)
		return
	}
	var tagPtr *string
	if tag != "" {
		tagPtr = &tag
	}
	err = writeSidecar(dir, name, Sidecar{Version: 1, Host: github.DefaultHost, Repo: now.repo, Path: now.path, UpstreamName: now.stem,
		Commit: body.Commit, Tag: tagPtr, UpstreamBlob: file.Blob, ContentSHA256: sumOf(data), ImportedAt: now.at,
		UpdatedAt: s.now().UTC().Format("2006-01-02T15:04:05Z")})
	if err != nil {
		msg := "Updating " + name + " failed: " + err.Error() + "; nothing changed."
		if rerr := writeAtomic(dir, name+".md", now.member); rerr != nil {
			msg = "Updating " + name + " failed: " + err.Error() + ", and putting its file back failed: " + rerr.Error() + ". Check " + name + ".md."
		}
		writeError(w, http.StatusInternalServerError, msg)
		return
	}
	s.updates.set(sh.c.key, name, updateNone)
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "commit": body.Commit, "tag": tagPtr,
		"missing_skills": missingSkills(sh.fleet, r.URL.Query().Get("harness"), member.Skills)})
}
