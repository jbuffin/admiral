package follow

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jbuffin/admiral/internal/ships"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/repofinder"
)

// Routes registers the campaign routes.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/campaigns", s.serveList)
	mux.HandleFunc("POST /api/campaigns", s.serveFollow)
	mux.HandleFunc("GET /api/campaigns/{id}", s.serveGet)
	mux.HandleFunc("POST /api/campaigns/create", s.serveCreate)
	mux.HandleFunc("POST /api/campaigns/{id}/unfollow", s.serveUnfollow)
	mux.HandleFunc("PUT /api/campaigns/{id}/override", s.serveOverride)
	mux.HandleFunc("PUT /api/campaigns/{id}/work-repo", s.serveWorkRepo)
	mux.HandleFunc("POST /api/campaigns/{id}/launch", s.serveLaunch)
}

type skillView struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Args string `json:"args"`
}

type blockerView struct {
	URL    string `json:"url"`
	Title  string `json:"title,omitempty"`
	Capped bool   `json:"capped,omitempty"` // the stand-in for Blocked by lines past the cap, not an issue
}

type nodeView struct {
	Number     int           `json:"number"`
	Title      string        `json:"title"`
	URL        string        `json:"url"`
	Repo       string        `json:"repo"`
	Closed     bool          `json:"closed"`
	Parent     bool          `json:"parent"`
	Unreadable int           `json:"unreadable,omitempty"`
	Status     string        `json:"status"`
	Blockers   []blockerView `json:"blockers"`
	Ship       string        `json:"ship"`
	Mode       string        `json:"mode"`
	Skill      skillView     `json:"skill"`
	Labels     []string      `json:"labels"`
	Children   []nodeView    `json:"children"`

	Overridden   bool       `json:"overridden,omitempty"`
	DefaultMode  string     `json:"default_mode,omitempty"`
	DefaultSkill *skillView `json:"default_skill,omitempty"`
}

type campaignView struct {
	ID       string         `json:"id"`
	URL      string         `json:"url"`
	Fleet    string         `json:"fleet"`
	WorkRepo string         `json:"work_repo"`
	Ended    bool           `json:"ended"`
	LastRead string         `json:"last_read"`
	ReadErr  string         `json:"read_error"`
	Progress progressView   `json:"progress"`
	Counts   map[string]int `json:"counts"`
	Root     *nodeView      `json:"root"`

	Title           string `json:"title"`
	Number          int    `json:"number"`
	Repo            string `json:"repo"` // of the root issue, https://host/owner/repo
	FollowedAt      string `json:"followed_at"`
	EndedAt         string `json:"ended_at,omitempty"`
	ReadyWithYou    int    `json:"ready_with_you"`
	ReadyWithoutYou int    `json:"ready_without_you"`
	Reading         bool   `json:"reading"`
	LastTried       string `json:"last_tried,omitempty"`
}

type progressView struct {
	Closed int `json:"closed"`
	Total  int `json:"total"`
}

func toNode(n *campaigns.Node) *nodeView {
	if n == nil {
		return nil
	}
	out := &nodeView{Number: n.Number, Title: n.Title, URL: n.URL, Repo: n.Repo, Closed: n.Closed, Parent: n.Parent,
		Unreadable: n.Unreadable, Status: string(n.Status), Ship: n.Ship, Mode: string(n.Mode),
		Skill:  skillView{n.Skill.Kind, n.Skill.Name, n.Skill.Args},
		Labels: append([]string{}, n.Labels...), Blockers: []blockerView{}, Children: []nodeView{}}
	if n.Overridden {
		out.Overridden = true
	}
	if n.DefaultMode != "" {
		out.DefaultMode = string(n.DefaultMode)
		out.DefaultSkill = &skillView{n.DefaultSkill.Kind, n.DefaultSkill.Name, n.DefaultSkill.Args}
	}
	for _, b := range n.Blockers {
		out.Blockers = append(out.Blockers, blockerView{URL: b.URL, Title: b.Title, Capped: b.Capped})
	}
	for _, c := range n.Children {
		out.Children = append(out.Children, *toNode(c))
	}
	return out
}

func (s *Service) json(f Followed) campaignView {
	v := s.view(f)
	out := campaignView{ID: f.ID, URL: f.URL, Fleet: f.Fleet, WorkRepo: f.WorkRepo, Ended: f.Ended(), ReadErr: f.ReadError,
		Progress: progressView{v.Progress.Closed, v.Progress.Total}, Counts: map[string]int{}, Root: toNode(v.Root)}
	if !f.ReadAt.IsZero() {
		out.LastRead = f.ReadAt.UTC().Format(time.RFC3339)
	}
	if t := s.lastTried(f); !t.IsZero() {
		out.LastTried = t.UTC().Format(time.RFC3339)
	}
	out.Reading = s.reader(f.ID).active.Load() > 0
	out.FollowedAt = f.FollowedAt.UTC().Format(time.RFC3339)
	if f.Ended() {
		out.EndedAt = f.EndedAt.UTC().Format(time.RFC3339)
	}
	if v.Root != nil {
		out.Title, out.Number = v.Root.Title, v.Root.Number
	}
	if ref, err := campaigns.ParseIssueRef(f.URL); err == nil {
		if out.Number == 0 {
			out.Number = ref.Number
		}
		repo := ref.Owner + "/" + ref.Repo
		if v.Root != nil && v.Root.Repo != "" {
			repo = v.Root.Repo
		}
		out.Repo = "https://" + hostOf(ref) + "/" + repo
	}
	for _, n := range v.Ready {
		if n.Mode == campaigns.WithYou {
			out.ReadyWithYou++
		} else {
			out.ReadyWithoutYou++
		}
	}
	for k, n := range v.Counts {
		out.Counts[string(k)] = n
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return false
	}
	return true
}

func (s *Service) serveList(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("ended")
	out := []campaignView{}
	for _, f := range s.Store.List() {
		if (filter == "true" && !f.Ended()) || (filter == "false" && f.Ended()) {
			continue
		}
		if fleet, ok := actor.FleetOf(r.Context()); ok && f.Fleet != fleet {
			continue
		}
		c := s.json(f)
		c.Root = nil // the list shows progress and counts; the page asks for the tree
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaigns": out})
}

func (s *Service) serveFollow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reference string `json:"reference"`
	}
	if !decode(w, r, &body) {
		return
	}
	got, created, lerr := s.Follow(r.Context(), body.Reference)
	if lerr != nil {
		writeError(w, lerr.Status, lerr.Msg)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, s.json(got))
}

func (s *Service) campaign(w http.ResponseWriter, r *http.Request) (Followed, bool) {
	f, ok := s.Store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such campaign")
	}
	return f, ok
}

func (s *Service) serveGet(w http.ResponseWriter, r *http.Request) {
	f, ok := s.campaign(w, r)
	if !ok {
		return
	}
	if !f.Ended() && s.now().Sub(s.lastTried(f)) >= pageFloor {
		s.startReread(f.ID, false)
	}
	writeJSON(w, http.StatusOK, s.json(f))
}

func (s *Service) serveUnfollow(w http.ResponseWriter, r *http.Request) {
	f, ok := s.campaign(w, r)
	if !ok {
		return
	}
	if err := s.Store.End(f.ID, s.now()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, _ = s.Store.Get(f.ID)
	s.changed()
	writeJSON(w, http.StatusOK, s.json(f))
}

func (s *Service) serveOverride(w http.ResponseWriter, r *http.Request) {
	f, ok := s.campaign(w, r)
	if !ok {
		return
	}
	var body struct {
		ChildURL string          `json:"child_url"`
		Mode     string          `json:"mode"`
		Skill    json.RawMessage `json:"skill"`
	}
	if !decode(w, r, &body) {
		return
	}
	n := find(s.view(f).Root, body.ChildURL)
	switch {
	case n == nil:
		writeError(w, http.StatusNotFound, "that issue is not in this campaign")
		return
	case n.Parent:
		writeError(w, http.StatusBadRequest, "that issue is a parent; set a child's mode")
		return
	case body.Skill != nil:
		writeError(w, http.StatusBadRequest, "an override sets only the mode; pick a skill when you launch")
		return
	case body.Mode != "" && body.Mode != string(campaigns.WithYou) && body.Mode != string(campaigns.WithoutYou):
		writeError(w, http.StatusBadRequest, `mode is "with-you" or "without-you"`)
		return
	}
	o := campaigns.Override{Mode: campaigns.Mode(body.Mode)}
	if err := s.Store.SetOverride(f.ID, n.URL, o); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, _ = s.Store.Get(f.ID)
	writeJSON(w, http.StatusOK, s.json(f))
}

func (s *Service) serveWorkRepo(w http.ResponseWriter, r *http.Request) {
	f, ok := s.campaign(w, r)
	if !ok {
		return
	}
	var body struct {
		Repo string `json:"repo"`
	}
	if !decode(w, r, &body) {
		return
	}
	repo := strings.TrimSpace(body.Repo)
	if repo != "" && !validRepo(repo) {
		writeError(w, http.StatusBadRequest, "Give the work repo as host/owner/repo or its URL")
		return
	}
	if repo != "" {
		id, _ := repofinder.Identity(repoURL(repo))
		repo = "https://" + id // no userinfo or query: only the identity is kept and shown
	}
	if err := s.Store.SetWorkRepo(f.ID, repo); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, _ = s.Store.Get(f.ID)
	writeJSON(w, http.StatusOK, s.json(f))
}

func (s *Service) serveLaunch(w http.ResponseWriter, r *http.Request) {
	f, ok := s.campaign(w, r)
	if !ok {
		return
	}
	var body struct {
		ChildURL    string   `json:"child_url"`
		Issues      []string `json:"issues"` // a slice: two or more children launched as one ship
		Harness     string   `json:"harness"`
		Dir         string   `json:"dir"`
		Clone       bool     `json:"clone"`
		CloneRepo   string   `json:"clone_repo"` // the repo the form showed, as for POST /api/ships
		StartAnyway bool     `json:"start_anyway"`
		Model       string   `json:"model"`
		Effort      string   `json:"effort"`
		Skill       *struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"skill"` // for this launch only; the child's saved skill is not changed
	}
	if !decode(w, r, &body) {
		return
	}
	if f.Ended() {
		writeError(w, http.StatusConflict, "this campaign has ended")
		return
	}
	var pick *launcher.Skill
	if sk := body.Skill; sk != nil {
		if body.Issues != nil {
			writeError(w, http.StatusBadRequest, "a slice runs on the Commodore skill; send skill only with child_url")
			return
		}
		if refuseSkillKind(w, sk.Kind) {
			return
		}
		pick = &launcher.Skill{Kind: sk.Kind, Name: sk.Name}
		if err := launcher.CheckSkill(*pick); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if body.Issues != nil && body.ChildURL != "" {
		writeError(w, http.StatusBadRequest, "send child_url or issues, not both")
		return
	}
	req := launcher.Request{
		Fleet: f.Fleet, Harness: body.Harness, Dir: body.Dir, Clone: body.Clone, CloneRepo: body.CloneRepo, Model: body.Model, Effort: body.Effort}
	var got any
	var lerr *launcher.Error
	if body.Issues != nil {
		got, lerr = s.launchSlice(r.Context(), f, body.Issues, req)
	} else {
		got, lerr = s.launch(r.Context(), f, body.ChildURL, body.StartAnyway, pick, req)
	}
	if lerr != nil {
		writeError(w, lerr.Status, lerr.Msg)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ship": got})
}

func (s *Service) launch(ctx context.Context, f Followed, childURL string, startAnyway bool, pick *launcher.Skill, req launcher.Request) (any, *launcher.Error) {
	// the page's statuses are those of the last read, so a stale read is renewed first; a failed renewal keeps the last
	_ = s.rereadIfOlder(ctx, f.ID, launchFresh)
	f, _ = s.Store.Get(f.ID)
	n := find(s.view(f).Root, childURL)
	if lerr := launchable(n, "that issue", startAnyway); lerr != nil {
		return nil, lerr
	}
	req.Source, req.OrderID = SourceKey, OrderID(f.ID, n.URL)
	req.Skill = launcher.Skill{Kind: n.Skill.Kind, Name: n.Skill.Name}
	req.Text = launchText(n)
	if pick != nil {
		req.Skill = *pick
		if !sameSkill(*pick, n.Skill) {
			// the saved args belong to the saved skill
			req.Text = launchText(&campaigns.Node{Title: n.Title, URL: n.URL, Skill: campaigns.Skill{Kind: pick.Kind}})
		}
	}
	req.Autopilot = false
	// the launcher compares clone_repo with the repo's host/owner/repo identity
	if req.Clone && req.CloneRepo == "" {
		req.CloneRepo, _ = repofinder.Identity(s.repoFor(f, n))
	}
	got, lerr := s.Launch(ctx, req)
	if lerr != nil {
		return nil, lerr
	}
	return got, nil
}

// sameSkill compares a picked skill with a child's resolved one; Commodore and Just launch ignore the name, which the
// form does not send for them.
func sameSkill(pick launcher.Skill, have campaigns.Skill) bool {
	if pick.Kind != have.Kind {
		return false
	}
	if pick.Kind != "skill" {
		return true
	}
	picked, saved := launcher.TrimSkillMark(pick.Name), launcher.TrimSkillMark(have.Name)
	if picked == saved {
		return true
	}
	// The dialog fills a bare saved name as the installed plugin skill that owns it.
	plugin, own, qualified := strings.Cut(picked, ":")
	return qualified && plugin != "" && own == saved
}

// refuseSkillKind answers 400 and reports true when kind is not one a skill can have.
func refuseSkillKind(w http.ResponseWriter, kind string) bool {
	if kind == "commodore" || kind == "skill" || kind == "none" {
		return false
	}
	writeError(w, http.StatusBadRequest, `skill kind is "commodore", "skill" or "none"`)
	return true
}

// launchable is the refusal for a child that cannot be launched now; what names the child in the message.
func launchable(n *campaigns.Node, what string, startAnyway bool) *launcher.Error {
	switch {
	case n == nil:
		return orderError(http.StatusNotFound, what+" is not in this campaign")
	case n.Parent:
		return orderError(http.StatusBadRequest, what+" is a parent; launch one of its children")
	case n.Status == campaigns.Done:
		return orderError(http.StatusConflict, what+" is done")
	case n.Status == campaigns.Underway:
		return orderError(http.StatusConflict, what+" already has a ship ("+n.Ship+")")
	case n.Status == campaigns.Blocked:
		urls := make([]string, 0, len(n.Blockers))
		for _, b := range n.Blockers {
			urls = append(urls, b.URL)
		}
		sort.Strings(urls)
		return orderError(http.StatusConflict, what+" is blocked by "+strings.Join(urls, ", "))
	case n.Status == campaigns.ClaimedNoShip && !startAnyway:
		return orderError(http.StatusConflict, what+" is assigned to someone and has no ship; send start_anyway to launch it")
	}
	return nil
}

// launchSlice launches two or more children as one Commodore ship holding all of them; start_anyway is not read,
// so an assigned child refuses the slice.
func (s *Service) launchSlice(ctx context.Context, f Followed, issues []string, req launcher.Request) (any, *launcher.Error) {
	seen := map[string]bool{}
	for _, u := range issues {
		k, ok := ships.ClaimKey(u)
		switch {
		case !ok:
			return nil, orderError(http.StatusBadRequest, u+" is not an issue URL")
		case seen[k]:
			return nil, orderError(http.StatusBadRequest, "a slice lists each issue once; "+u+" is listed twice")
		case strings.Contains(u, ","):
			return nil, orderError(http.StatusBadRequest, u+" cannot be launched in a slice")
		}
		seen[k] = true
	}
	if len(issues) < 2 {
		return nil, orderError(http.StatusBadRequest, "a slice is two or more issues; launch one issue with child_url")
	}
	_ = s.rereadIfOlder(ctx, f.ID, launchFresh)
	f, _ = s.Store.Get(f.ID)
	root := s.view(f).Root
	nodes := make([]*campaigns.Node, 0, len(issues))
	for _, u := range issues {
		n := find(root, u)
		if lerr := launchable(n, u, false); lerr != nil {
			return nil, lerr
		}
		switch {
		case n.Mode != campaigns.WithoutYou:
			return nil, orderError(http.StatusConflict, u+" is With you; a slice launches only Without you children")
		case n.Skill.Kind != "commodore":
			return nil, orderError(http.StatusConflict, u+" does not run on the Commodore skill; a slice launches only Commodore children")
		case n.Status != campaigns.Ready:
			return nil, orderError(http.StatusConflict, u+" is not ready")
		case len(nodes) > 0 && s.repoFor(f, n) != s.repoFor(f, nodes[0]):
			return nil, orderError(http.StatusBadRequest, u+" is in another repo than "+nodes[0].URL+"; a slice stays in one repo")
		}
		nodes = append(nodes, n)
	}
	urls := make([]string, 0, len(nodes))
	for _, n := range nodes {
		urls = append(urls, n.URL)
	}
	req.Source, req.OrderID, req.Issues = SourceKey, SliceOrderID(f.ID, urls), urls
	req.Skill = launcher.Skill{Kind: "commodore", Name: nodes[0].Skill.Name}
	req.Text = sliceText(nodes)
	req.Autopilot = false
	if req.Clone && req.CloneRepo == "" {
		req.CloneRepo, _ = repofinder.Identity(s.repoFor(f, nodes[0]))
	}
	got, lerr := s.Launch(ctx, req)
	if lerr != nil {
		return nil, lerr
	}
	return got, nil
}

const maxCreateIssues = 50

var repoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// createTimeout bounds the writes and the follow after them, which run even if the engineer's browser hangs up.
const createTimeout = 2 * time.Minute

type createdFailure struct {
	Error  string   `json:"error"`
	Parent string   `json:"parent"`
	Linked []string `json:"linked"`
}

// serveCreate files a campaign's parent issue, links the chosen issues under it in order and follows it.
// It never edits an existing tree: a child that already has a parent is refused, not moved.
func (s *Service) serveCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repo   string   `json:"repo"`
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Issues []string `json:"issues"`
	}
	if !decode(w, r, &body) {
		return
	}
	title := strings.TrimSpace(body.Title)
	switch {
	case title == "":
		writeError(w, http.StatusBadRequest, "title: write a title for the campaign")
		return
	case len(body.Issues) == 0 || len(body.Issues) > maxCreateIssues:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("issues: pick 1 to %d", maxCreateIssues))
		return
	}
	owner, repoName, ok := strings.Cut(strings.TrimSpace(body.Repo), "/")
	if !ok || !repoPart.MatchString(owner) || !repoPart.MatchString(repoName) || owner == "." || owner == ".." || repoName == "." || repoName == ".." {
		writeError(w, http.StatusBadRequest, "repo: write owner/name")
		return
	}
	type child struct {
		ref campaigns.IssueRef
		url string
	}
	var kids []child
	seen := map[string]bool{}
	for _, raw := range body.Issues {
		ref, err := campaigns.ParseIssueRef(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, raw+": "+err.Error())
			return
		}
		url := fmt.Sprintf("https://%s/%s/%s/issues/%d", hostOf(ref), ref.Owner, ref.Repo, ref.Number)
		key := strings.ToLower(url)
		if seen[key] {
			writeError(w, http.StatusBadRequest, "issues: "+url+" is listed twice")
			return
		}
		seen[key] = true
		kids = append(kids, child{ref, url})
	}
	host := hostOf(kids[0].ref)
	fleet := ""
	if s.Route != nil {
		fleet = s.Route(host, owner+"/"+repoName)
	}
	if fleet == "" {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("No fleet owns %s/%s, so Admiral can't file the campaign there", owner, repoName))
		return
	}
	for _, k := range kids {
		switch {
		case hostOf(k.ref) != host:
			writeError(w, http.StatusUnprocessableEntity, "issues: every issue must be on the same GitHub host")
			return
		case !strings.EqualFold(k.ref.Owner, owner):
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("%s is not owned by %s, so it can't be linked under a parent in %s/%s", k.url, owner, owner, repoName))
			return
		case s.Route(host, k.ref.Owner+"/"+k.ref.Repo) != fleet:
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("%s belongs to another fleet than %s/%s", k.url, owner, repoName))
			return
		}
	}
	client, ok := s.Clients(fleet, host)
	switch {
	case !ok:
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("fleet %s has no GitHub source for %s", fleet, host))
		return
	case !strings.EqualFold(client.Host(), host):
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("the GitHub source of fleet %s reads %s, not %s", fleet, client.Host(), host))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	ids := make([]int64, len(kids))
	for i, k := range kids {
		rec, err := client.IssueRecord(ctx, k.ref.Owner, k.ref.Repo, k.ref.Number)
		if err != nil {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("Could not read %s, so nothing was created: %v", k.url, err))
			return
		}
		if rec.PullRequest {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("%s is a pull request, not an issue, so nothing was created", k.url))
			return
		}
		if rec.ParentURL != "" {
			writeError(w, http.StatusConflict, fmt.Sprintf("%s already has a parent, and Admiral never moves an issue, so nothing was created", k.url))
			return
		}
		ids[i] = rec.RESTID
	}

	wctx, wcancel := context.WithTimeout(context.WithoutCancel(r.Context()), createTimeout)
	defer wcancel()
	parent, err := client.CreateIssue(wctx, owner, repoName, title, body.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Could not create the parent issue, so nothing was linked: %v", err))
		return
	}
	linked := []string{}
	fail := func(msg string) {
		writeJSON(w, http.StatusBadGateway, createdFailure{Error: msg, Parent: parent.URL, Linked: linked})
	}
	for i, k := range kids {
		if err := client.AddSubIssue(wctx, owner, repoName, parent.Number, ids[i]); err != nil {
			fail(fmt.Sprintf("Created %s but could not link %s: %v", parent.URL, k.url, err))
			return
		}
		linked = append(linked, k.url)
	}
	got, _, lerr := s.Follow(wctx, parent.URL)
	if lerr != nil {
		fail(fmt.Sprintf("Created and linked %s but could not follow it: %s", parent.URL, lerr.Msg))
		return
	}
	writeJSON(w, http.StatusCreated, s.json(got))
}
