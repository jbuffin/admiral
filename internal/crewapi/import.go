package crewapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/github"
)

const botImportText = "Only the engineer can import crew members."

// writeSidecar is WriteSidecar, a variable so a test can fail it: nothing outside can, once the member is written under the lock.
var writeSidecar = WriteSidecar

// importRoutes registers the import, crew source and browse routes.
func (s *Service) importRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/fleets/{key}/crew/imports/preview", s.servePreviewImport)
	mux.HandleFunc("POST /api/fleets/{key}/crew/imports", s.serveImport)
	mux.HandleFunc("PUT /api/fleets/{key}/crew/sources", s.servePutSources)
	mux.HandleFunc("GET /api/fleets/{key}/crew/sources/{owner}/{repo}", s.serveBrowse)
}

// importFleet resolves the fleet for an import route, which works with sharing on or off. needClient answers 409 when the fleet has no github.com client.
func (s *Service) importFleet(w http.ResponseWriter, r *http.Request, needClient bool) (*sharing, bool) {
	if _, bot := actor.FleetOf(r.Context()); bot {
		writeError(w, http.StatusForbidden, botImportText)
		return nil, false
	}
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return nil, false
	}
	cfg := s.Config.Current()
	sh := newSharing(cfg, c, s.client(cfg, c.key))
	if needClient && sh.gh == nil {
		writeError(w, http.StatusConflict, connectText)
		return nil, false
	}
	return sh, true
}

// checkImported is the rules an imported file meets, on preview and again on import: the text of the first it breaks, or "" with the member it holds.
func checkImported(data []byte, stem string) (crew.Member, string) {
	var none crew.Member
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if !utf8.ValidString(line) {
			return none, fmt.Sprintf("The file isn't valid UTF-8 on line %d, so Admiral won't import it.", i+1)
		}
	}
	if r, line := firstHidden(lines); line > 0 {
		return none, fmt.Sprintf("U+%04X on line %d: Admiral won't import a file with a character a reader can't see.", r, line)
	}
	for i := 1; i < len(lines) && strings.TrimRight(lines[i], "\r") != "---"; i++ {
		for _, key := range []string{"ad-hoc", "ad-hoc-run"} {
			if strings.HasPrefix(lines[i], key+":") {
				return none, fmt.Sprintf("%s on line %d: an ad hoc member can't be imported.", key, i+1)
			}
		}
	}
	m, ferr := crew.CheckFile(data, stem)
	if ferr != nil {
		msg := ferr.Message
		if ferr.Field != "" && !strings.Contains(msg, ferr.Field) {
			msg = ferr.Field + ": " + msg
		}
		return none, msg
	}
	return m, ""
}

// memberTaken is whether a member, a built-in, a file or a sidecar already stands at name.
func (c fleetCrew) memberTaken(name string) (bool, error) {
	for _, m := range crew.Resolve(c.layers(true)).Members {
		if m.Name == name {
			return true, nil
		}
	}
	if _, ok := c.builtIns()[name]; ok {
		return true, nil
	}
	for _, file := range []string{name + ".md", sidecarFile(name)} {
		_, err := os.Lstat(filepath.Join(c.crewDir(), file))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// suggestedName is <name>-<owner> as a crew slug, or nil when that is invalid or taken.
func suggestedName(c fleetCrew, name, owner string) any {
	suffix := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(owner), "-"), "-")
	if suffix == "" {
		return nil
	}
	sug := name + "-" + suffix
	if len(sug) > 64 {
		sug = strings.TrimRight(sug[:64], "-")
	}
	if taken, err := c.memberTaken(sug); !crew.ValidName(sug) || taken || err != nil {
		return nil
	}
	return sug
}

// missingSkills lists the skills the page's dock (every dock when harness is empty) can't see.
func missingSkills(fleet config.Fleet, harness string, skills []string) []string {
	out := []string{}
	if len(skills) == 0 {
		return out
	}
	seen := map[string]bool{}
	for _, e := range fleetSkills(fleet, harness) {
		seen[e.Name] = true
	}
	for _, s := range skills {
		if !seen[s] && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// readImportable reads path of owner/repo at commit and runs the import rules over it, answering the refusal itself; ok is false once it has. what names the file in a not-found answer.
func readImportable(w http.ResponseWriter, ctx context.Context, gh *github.Client, owner, repo, path, commit, what, stem, wantSum string) (file github.File, member crew.Member, ok bool) {
	full := owner + "/" + repo
	file, err := gh.ReadFileAt(ctx, owner, repo, path, commit)
	switch {
	case errors.Is(err, github.ErrTooLarge):
		writeError(w, http.StatusUnprocessableEntity, path+" in "+full+" is over 64 KiB, so Admiral won't import it.")
		return file, member, false
	case err != nil:
		notFoundIn(w, "Reading "+path, full, what, err)
		return file, member, false
	}
	if wantSum != "" && sumOf(file.Bytes) != wantSum {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "The file changed since the preview. Preview it again.", "kind": "changed"})
		return file, member, false
	}
	member, problem := checkImported(file.Bytes, stem)
	if problem != "" {
		writeError(w, http.StatusUnprocessableEntity, problem)
		return file, member, false
	}
	return file, member, true
}

// usableTag is tag unless a reader couldn't see all of it or it isn't a ref the import box takes, which is no tag at all.
func usableTag(tag string) string {
	if _, line := firstHidden([]string{tag}); line > 0 || !validImportRef(tag) {
		return ""
	}
	return tag
}

func cardOf(m crew.Member) map[string]any {
	skills := m.Skills
	if skills == nil {
		skills = []string{}
	}
	return map[string]any{"name": m.Name, "role": m.Role, "based-on": m.BasedOn, "skills": skills, "model": m.Model,
		"harness": m.Harness, "pick-me-when": m.PickMeWhen, "instructions": m.Instructions}
}

// notFoundIn answers a GitHub 404 or 422 for something inside repo; any other failure goes through ghFailed.
func notFoundIn(w http.ResponseWriter, step, repo, what string, err error) {
	if status := github.HTTPStatus(err); status == http.StatusNotFound || status == http.StatusUnprocessableEntity {
		writeError(w, http.StatusNotFound, what+" was not found in "+repo+".")
		return
	}
	ghFailed(w, step, repo, err)
}

func (s *Service) servePreviewImport(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.importFleet(w, r, false)
	if !ok {
		return
	}
	var body struct {
		Ref string `json:"ref"`
	}
	if !decode(w, r, &body) {
		return
	}
	ref, err := ParseImportRef(body.Ref)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if sh.gh == nil {
		writeError(w, http.StatusConflict, connectText)
		return
	}
	full := ref.Owner + "/" + ref.Repo
	if ref.Source {
		writeJSON(w, http.StatusOK, map[string]string{"source": full})
		return
	}
	ctx := r.Context()
	info, err := sh.gh.Repo(ctx, ref.Owner, ref.Repo)
	if err != nil {
		ghFailed(w, "Reading "+full, full, err)
		return
	}
	at := ref.Ref
	if at == "" {
		at = info.DefaultBranch
	}
	if at == "" {
		writeError(w, http.StatusBadGateway, "Reading "+full+" failed: GitHub didn't name its default branch.")
		return
	}
	commit, err := sh.gh.ResolveCommit(ctx, ref.Owner, ref.Repo, at)
	if err != nil {
		notFoundIn(w, "Reading "+full, full, "The ref "+at, err)
		return
	}
	path := crewDir + "/" + ref.Name + ".md"
	file, member, ok := readImportable(w, ctx, sh.gh, ref.Owner, ref.Repo, path, commit, path+" ("+at+")", ref.Name, "")
	if !ok {
		return
	}
	tag, err := sh.gh.TagAt(ctx, ref.Owner, ref.Repo, commit)
	if err != nil {
		ghFailed(w, "Reading the tags of "+full, full, err)
		return
	}
	tag = usableTag(tag)
	var clash any
	taken, err := sh.c.memberTaken(ref.Name)
	switch {
	case err != nil:
		refuse(w, err)
		return
	case taken:
		clash = map[string]any{"name": ref.Name, "suggested": suggestedName(sh.c, ref.Name, ref.Owner)}
	}
	var tagOut any
	if tag != "" {
		tagOut = tag
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"owner": ref.Owner, "repo": ref.Repo, "path": path, "commit": commit, "tag": tagOut, "blob": file.Blob,
		"sha256": sumOf(file.Bytes), "bytes": string(file.Bytes), "card": cardOf(member), "clash": clash,
		"missing_skills": missingSkills(sh.fleet, r.URL.Query().Get("harness"), member.Skills), "scan": s.scan(ctx, sh, file.Bytes),
	})
}

func (s *Service) serveImport(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.importFleet(w, r, false)
	if !ok {
		return
	}
	var body struct {
		Repo   string `json:"repo"`
		Path   string `json:"path"`
		Commit string `json:"commit"`
		SHA256 string `json:"sha256"`
		Name   string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	stem, isMd := strings.CutSuffix(strings.TrimPrefix(body.Path, crewDir+"/"), ".md")
	switch {
	case !github.ValidRepo(body.Repo):
		writeError(w, http.StatusBadRequest, "repo must be <owner>/<name>")
		return
	case !strings.HasPrefix(body.Path, crewDir+"/") || !isMd || !crew.ValidName(stem):
		writeError(w, http.StatusBadRequest, "path must be crew/<name>.md")
		return
	case !blobPattern.MatchString(body.Commit):
		writeError(w, http.StatusBadRequest, "commit must come from a preview")
		return
	case !sumPattern.MatchString(body.SHA256):
		writeError(w, http.StatusBadRequest, "sha256 must come from a preview")
		return
	case body.Name != "" && !crew.ValidName(body.Name):
		writeError(w, http.StatusBadRequest, "name must be a lowercase slug of at most 64 characters")
		return
	}
	if sh.gh == nil {
		writeError(w, http.StatusConflict, connectText)
		return
	}
	name := body.Name
	if name == "" {
		name = stem
	}
	owner, repo, _ := strings.Cut(body.Repo, "/")
	ctx := r.Context()
	file, member, ok := readImportable(w, ctx, sh.gh, owner, repo, body.Path, body.Commit, body.Path, stem, body.SHA256)
	if !ok {
		return
	}
	tag, err := sh.gh.TagAt(ctx, owner, repo, body.Commit)
	if err != nil {
		ghFailed(w, "Reading the tags of "+body.Repo, body.Repo, err)
		return
	}
	tag = usableTag(tag)
	data := file.Bytes
	if name != stem {
		data = renamed(data, name)
		if _, problem := checkImported(data, name); problem != "" {
			writeError(w, http.StatusUnprocessableEntity, problem)
			return
		}
	}

	writeMu.Lock()
	defer writeMu.Unlock()
	taken, err := sh.c.memberTaken(name)
	if err == nil && !taken {
		err = WriteImported(sh.c.home, sh.c.key, name, string(data))
		taken = errors.Is(err, ErrNameTaken)
	}
	switch {
	case taken:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "A crew member named " + name + " is already in this fleet. Choose another name.", "suggested": suggestedName(sh.c, stem, owner)})
		return
	case err != nil:
		refuseImport(w, err)
		return
	}
	var tagPtr *string
	if tag != "" {
		tagPtr = &tag
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	err = writeSidecar(sh.c.crewDir(), name, Sidecar{Version: 1, Host: github.DefaultHost, Repo: body.Repo, Path: body.Path, UpstreamName: stem,
		Commit: body.Commit, Tag: tagPtr, UpstreamBlob: file.Blob, ContentSHA256: sumOf(data), ImportedAt: now, UpdatedAt: now})
	if err != nil {
		// The member stays as a plain fleet member the Crew page shows as one.
		refuse(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "missing_skills": missingSkills(sh.fleet, r.URL.Query().Get("harness"), member.Skills), "scan": s.scan(ctx, sh, data)})
}

// refuseImport answers a failed import write: a refusal from the path guard is a conflict, and anything else goes through refuse.
func refuseImport(w http.ResponseWriter, err error) {
	var refused *MissionRefusedError
	if errors.As(err, &refused) {
		writeError(w, http.StatusConflict, refused.Msg)
		return
	}
	refuse(w, err)
}

// sourceRepo is the owner/name a crew source entry names, from owner/name or a GitHub repo URL.
func sourceRepo(entry string) (string, bool) {
	entry = strings.TrimSpace(entry)
	if github.ValidRepo(entry) {
		return entry, true
	}
	if ref, err := ParseImportRef(entry); err == nil && ref.Source {
		return ref.Owner + "/" + ref.Repo, true
	}
	return "", false
}

func (s *Service) servePutSources(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.importFleet(w, r, false)
	if !ok {
		return
	}
	var body struct {
		Sources []string `json:"sources"`
	}
	if !decode(w, r, &body) {
		return
	}
	list := []string{}
	for _, entry := range body.Sources {
		repo, ok := sourceRepo(entry)
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a GitHub repo: use owner/name or its URL", entry))
			return
		}
		if !slices.ContainsFunc(list, func(have string) bool { return strings.EqualFold(have, repo) }) {
			list = append(list, repo)
		}
	}
	var added []string
	for _, repo := range list {
		if !slices.ContainsFunc(sh.fleet.CrewSharing.Sources, func(have string) bool { return strings.EqualFold(have, repo) }) {
			added = append(added, repo)
		}
	}
	if len(added) > 0 && sh.gh == nil {
		writeError(w, http.StatusConflict, connectText)
		return
	}
	for _, repo := range added {
		if !s.checkSource(w, r.Context(), sh.gh, repo) {
			return
		}
	}
	raw, _ := json.Marshal(list)
	err := s.Config.SetFleetFields(sh.c.key, config.FleetFields{CrewSharing: map[string]json.RawMessage{"sources": raw}})
	switch {
	case errors.Is(err, config.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string][]string{"sources": list})
	}
}

// checkSource answers for a repo that is missing or has no crew/ folder.
func (s *Service) checkSource(w http.ResponseWriter, ctx context.Context, gh *github.Client, repo string) bool {
	owner, name, _ := strings.Cut(repo, "/")
	if _, err := gh.Repo(ctx, owner, name); err != nil {
		ghFailed(w, "Checking "+repo, repo, err)
		return false
	}
	if _, err := gh.ListDir(ctx, owner, name, crewDir); err != nil {
		if github.HTTPStatus(err) == http.StatusNotFound {
			writeError(w, http.StatusNotFound, repo+" has no "+crewDir+"/ folder.")
		} else {
			ghFailed(w, "Reading "+repo, repo, err)
		}
		return false
	}
	return true
}

type browseRow struct {
	Name      string  `json:"name"`
	Path      string  `json:"path"`
	Blob      string  `json:"blob"`
	Imported  *string `json:"imported"`
	Update    *string `json:"update"`
	Published bool    `json:"published"`
}

func (s *Service) serveBrowse(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.importFleet(w, r, true)
	if !ok {
		return
	}
	owner, repo := r.PathValue("owner"), r.PathValue("repo")
	if !github.ValidSegment(owner) || !github.ValidSegment(repo) {
		writeError(w, http.StatusBadRequest, owner+"/"+repo+" is not a GitHub repo")
		return
	}
	full := owner + "/" + repo
	entries, err := sh.gh.ListDir(r.Context(), owner, repo, crewDir)
	if err != nil {
		if github.HTTPStatus(err) == http.StatusNotFound {
			writeError(w, http.StatusNotFound, full+" has no "+crewDir+"/ folder.")
		} else {
			ghFailed(w, "Reading "+full, full, err)
		}
		return
	}
	imported := importedFrom(sh.c.crewDir(), full)
	rows := []browseRow{}
	for _, e := range entries {
		stem, isMd := strings.CutSuffix(e.Name, ".md")
		if !isMd || !crew.ValidName(stem) {
			continue
		}
		path := crewDir + "/" + e.Name
		row := browseRow{Name: stem, Path: path, Blob: e.Blob, Published: strings.EqualFold(full, sh.repo)}
		if m, ok := imported[path]; ok {
			row.Imported = &m.name
			state := updateNone
			if !m.side.shared.Detached {
				state = s.updates.state(sh.c.key, m.name)
			}
			row.Update = &state
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string][]browseRow{"members": rows})
}

// importedFrom maps each upstream path of repo that a usable sidecar in dir records to the member's local name and sidecar.
func importedFrom(dir, repo string) map[string]importedMember {
	out := map[string]importedMember{}
	for _, name := range sidecarStems(dir) {
		side := readSidecar(dir, name)
		if side.kind != usableSidecar || !strings.EqualFold(side.repo, repo) {
			continue
		}
		if _, dup := out[side.path]; !dup {
			out[side.path] = importedMember{name: name, side: side}
		}
	}
	return out
}

type importedMember struct {
	name string
	side sidecarRead
}

// sharedURL is the page of the member's upstream file at the commit it came from; nil when it has no usable sidecar.
func sharedURL(dir string, m crew.Member) *string {
	if m.Shared == nil || m.Layer != crew.LayerFleet {
		return nil
	}
	side := readSidecar(dir, m.Name)
	if side.kind != usableSidecar {
		return nil
	}
	file := strings.TrimPrefix(side.path, crewDir+"/")
	u := "https://github.com/" + url.PathEscape(m.Shared.Owner) + "/" + url.PathEscape(m.Shared.Repo) +
		"/blob/" + url.PathEscape(m.Shared.Commit) + "/" + crewDir + "/" + url.PathEscape(file)
	return &u
}
