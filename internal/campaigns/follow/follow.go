// Package follow keeps the campaigns an engineer follows: a store under $ADMIRAL_HOME, the reads that
// refresh their trees, the HTTP routes, and the campaign order source that launches a child by hand.
package follow

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/repofinder"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
)

// SourceKey is the order source key campaign ships carry.
const SourceKey = "campaign"

const (
	readTimeout  = 60 * time.Second // one tree read, so a refresh can't run forever
	pageFloor    = 30 * time.Second // opening a page rereads no more often than this
	launchFresh  = time.Minute      // a launch rereads first when the read is older than this
	tickInterval = 5 * time.Minute  // each active campaign is reread this often
)

// Service follows campaigns and launches their children.
type Service struct {
	Store *Store
	// Route names the fleet that owns a repo, as github.Router.Route does.
	Route func(host, nameWithOwner string) string
	// Clients gives the fleet's GitHub client for a host, read fresh on each call.
	Clients func(fleet, host string) (*github.Client, bool)
	// Ships lists the ships now, to see which child's ship anchored or left.
	Ships func() []ships.Ship
	// Claims maps a ClaimKey to the ship holding that issue.
	Claims func() map[string]string
	// Launch is ships.Service.Launch.
	Launch func(ctx context.Context, req launcher.Request) (ships.Ship, *launcher.Error)
	// Next answers every order that is not a campaign's.
	Next ships.Orders
	Now  func() time.Time

	once   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	reads map[string]*reader
	tried map[string]time.Time    // the last attempt at a read, good or not, for the floors
	snaps [](map[string]shipSeen) // ship snapshots ShipsChanged took, for the worker to diff in order
	poke  chan struct{}
	prev  map[string]shipSeen // the worker's last snapshot; nil before the first
}

type shipSeen struct{ status, key string }

// reader serialises the reads of one campaign.
type reader struct {
	mu    sync.Mutex
	dirty bool // a forced reread asked for while one ran
}

func (s *Service) background() context.Context {
	s.once.Do(func() {
		s.ctx, s.cancel = context.WithCancel(context.Background())
		s.poke = make(chan struct{}, 1)
		s.wg.Add(1)
		go s.watchShips(s.ctx)
	})
	return s.ctx
}

// Close stops the reads under way and waits for them.
func (s *Service) Close() {
	s.background()
	s.cancel()
	s.wg.Wait()
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) reader(id string) *reader {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reads == nil {
		s.reads = map[string]*reader{}
	}
	if s.reads[id] == nil {
		s.reads[id] = &reader{}
	}
	return s.reads[id]
}

func (s *Service) lastTried(f Followed) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.tried[f.ID]; t.After(f.ReadAt) {
		return t
	}
	return f.ReadAt
}

// startReread reads the campaign's tree on its own goroutine unless a read is under way; again asks the running
// read to be followed by another, for a change it may have missed.
func (s *Service) startReread(id string, again bool) {
	r := s.reader(id)
	if !r.mu.TryLock() {
		if again {
			s.mu.Lock()
			r.dirty = true
			s.mu.Unlock()
		}
		return
	}
	ctx := s.background()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			for ctx.Err() == nil {
				_ = s.read(ctx, id)
				if !s.takeDirty(r) {
					break
				}
			}
			r.mu.Unlock()
			// a request that came after the last check but before the unlock would be lost
			if ctx.Err() != nil || !s.takeDirty(r) || !r.mu.TryLock() {
				return
			}
		}
	}()
}

func (s *Service) takeDirty(r *reader) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	was := r.dirty
	r.dirty = false
	return was
}

// rereadIfOlder reads now, waiting for a read under way, when the last good read is older than age or a
// forced reread is waiting.
func (s *Service) rereadIfOlder(ctx context.Context, id string, age time.Duration) error {
	r := s.reader(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	forced := s.takeDirty(r)
	f, ok := s.Store.Get(id)
	if !ok || f.Ended() || (!forced && !f.ReadAt.IsZero() && s.now().Sub(f.ReadAt) <= age) {
		return nil
	}
	return s.read(ctx, id)
}

// read rereads one campaign's tree and stores the result: a failed read keeps the last good tree and records why.
func (s *Service) read(ctx context.Context, id string) error {
	f, ok := s.Store.Get(id)
	if !ok || f.Ended() {
		return nil
	}
	s.mu.Lock()
	if s.tried == nil {
		s.tried = map[string]time.Time{}
	}
	s.tried[id] = s.now()
	s.mu.Unlock()
	tree, err := s.readTree(ctx, f.Fleet, f.URL)
	if err != nil {
		if ctx.Err() != nil {
			return err // shutting down or the caller left: not a failed read
		}
		_ = s.Store.SetRead(id, nil, s.now(), err.Error())
		return err
	}
	if err := s.Store.SetRead(id, tree, s.now(), ""); err != nil {
		return err
	}
	if allClosed(tree) {
		_ = s.Store.End(id, s.now())
	}
	return nil
}

// readTree reads the tree under the issue at url with the fleet's GitHub client for the issue's host.
func (s *Service) readTree(ctx context.Context, fleet, url string) (*github.TreeNode, error) {
	ref, err := campaigns.ParseIssueRef(url)
	if err != nil {
		return nil, err
	}
	host := hostOf(ref)
	client, ok := s.Clients(fleet, host)
	if !ok {
		return nil, fmt.Errorf("fleet %s has no GitHub source for %s", fleet, host)
	}
	if !strings.EqualFold(client.Host(), host) {
		return nil, fmt.Errorf("the GitHub source of fleet %s reads %s, not %s", fleet, client.Host(), host)
	}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	token, err := client.Token(ctx)
	if err != nil {
		return nil, err
	}
	return client.Tree(ctx, token, ref.Owner, ref.Repo, ref.Number)
}

// hostOf is the issue's host; owner/repo#n names none and means github.com.
func hostOf(ref campaigns.IssueRef) string {
	if ref.Host == "" {
		return github.DefaultHost
	}
	return strings.ToLower(ref.Host)
}

// allClosed is true when the parent and every issue under it, nested parents too, is closed.
func allClosed(n *github.TreeNode) bool {
	if n == nil || !n.Closed || n.Unreadable > 0 {
		return false
	}
	for _, c := range n.Children {
		if !allClosed(c) {
			return false
		}
	}
	return true
}

// Tick is the periodic pass: it rereads each active campaign last read five minutes ago or more.
func (s *Service) Tick() {
	for _, f := range s.Store.List() {
		if !f.Ended() && s.now().Sub(s.lastTried(f)) >= tickInterval {
			s.startReread(f.ID, false)
		}
	}
}

// Run ticks until ctx is done.
func (s *Service) Run(ctx context.Context) {
	s.Tick()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.Tick()
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) Connected(c *server.Client) {}

// ShipsChanged is the ships' OnChange hook. It only takes a snapshot and pokes the worker, so it never waits on
// the store.
func (s *Service) ShipsChanged() {
	if s.Ships == nil {
		return
	}
	cur := map[string]shipSeen{}
	for _, sh := range s.Ships() {
		if k, ok := ships.ClaimKey(sh.URL); ok {
			id := sh.Session
			if id == "" {
				id = sh.Name
			}
			cur[id] = shipSeen{sh.Status, k}
		}
	}
	s.background()
	s.mu.Lock()
	s.snaps = append(s.snaps, cur)
	s.mu.Unlock()
	select {
	case s.poke <- struct{}{}:
	default:
	}
}

// watchShips diffs each snapshot with the one before: a child's ship that anchored, is sailing off or is gone
// rereads the campaign holding the child.
func (s *Service) watchShips(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-s.poke:
		case <-ctx.Done():
			return
		}
		s.mu.Lock()
		snaps := s.snaps
		s.snaps = nil
		s.mu.Unlock()
		left := map[string]bool{}
		for _, cur := range snaps {
			if s.prev != nil {
				for id, was := range s.prev {
					now, present := cur[id]
					if !present || (leaving(now.status) && !leaving(was.status)) {
						left[was.key] = true
					}
				}
			}
			s.prev = cur
		}
		if len(left) == 0 {
			continue
		}
		for _, f := range s.Store.List() {
			if !f.Ended() && hasChild(f.Tree, left) {
				s.startReread(f.ID, true)
			}
		}
	}
}

func leaving(status string) bool {
	return status == ships.StatusAnchored || status == ships.StatusSailingOff
}

func hasChild(n *github.TreeNode, keys map[string]bool) bool {
	if n == nil {
		return false
	}
	if k, ok := ships.ClaimKey(n.URL); ok && keys[k] {
		return true
	}
	for _, c := range n.Children {
		if hasChild(c, keys) {
			return true
		}
	}
	return false
}

// view folds the campaign's tree with the live claims and the stored overrides.
func (s *Service) view(f Followed) campaigns.Campaign {
	claims := map[string]string{}
	if s.Claims != nil {
		held := s.Claims()
		var walk func(n *github.TreeNode)
		walk = func(n *github.TreeNode) {
			if k, ok := ships.ClaimKey(n.URL); ok && held[k] != "" {
				claims[n.URL] = held[k]
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		if f.Tree != nil {
			walk(f.Tree)
		}
	}
	return campaigns.Build(campaigns.Input{Tree: f.Tree, Claims: claims, Overrides: f.Overrides})
}

func find(n *campaigns.Node, url string) *campaigns.Node {
	if n == nil {
		return nil
	}
	if sameIssue(n.URL, url) {
		return n
	}
	for _, c := range n.Children {
		if got := find(c, url); got != nil {
			return got
		}
	}
	return nil
}

// OrderID is the order id of a campaign child: the campaign's id, a slash and the child's issue URL, so the
// same child in two campaigns is two orders.
func OrderID(campaignID, childURL string) string { return campaignID + "/" + childURL }

// childOf finds the child an order id names in its campaign.
func (s *Service) childOf(orderID string) (Followed, *campaigns.Node, *launcher.Error) {
	id, url, ok := strings.Cut(orderID, "/")
	if !ok {
		return Followed{}, nil, orderError(http.StatusNotFound, "a campaign order id is <campaign id>/<child URL>")
	}
	f, ok := s.Store.Get(id)
	if !ok {
		return Followed{}, nil, orderError(http.StatusNotFound, "no such campaign")
	}
	if f.Ended() {
		return Followed{}, nil, orderError(http.StatusConflict, "this campaign has ended")
	}
	n := find(s.view(f).Root, url)
	if n == nil {
		return Followed{}, nil, orderError(http.StatusNotFound, "that issue is not in this campaign")
	}
	return f, n, nil
}

func orderError(status int, msg string) *launcher.Error {
	return &launcher.Error{Status: status, Msg: msg}
}

// Order answers for a campaign child (source SourceKey, id from OrderID) and hands the rest to Next.
func (s *Service) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	if source != SourceKey {
		if s.Next == nil {
			return "", "", "", "", "", orderError(http.StatusNotFound, "unknown order source "+source)
		}
		return s.Next.Order(source, id)
	}
	f, n, lerr := s.childOf(id)
	if lerr != nil {
		return "", "", "", "", "", lerr
	}
	if n.Parent {
		return "", "", "", "", "", orderError(http.StatusBadRequest, "that issue is a parent; launch one of its children")
	}
	return launchText(n), n.Title, s.repoFor(f, n), n.URL, f.ID, nil
}

// launchText is what the launcher puts after the skill: the wayfinder map and ticket, or the ticket URL;
// with no skill the title and URL, so the ship knows what it was handed.
func launchText(n *campaigns.Node) string {
	switch {
	case n.Skill.Args != "":
		return n.Skill.Args
	case n.Skill.Kind == "none":
		return n.Title + " " + n.URL
	}
	return n.URL
}

// repoFor is the repo a child's launch preselects or clones: the work repo for a child filed in the parent's
// repo, else the child's own.
func (s *Service) repoFor(f Followed, n *campaigns.Node) string {
	if ref, err := campaigns.ParseIssueRef(f.URL); err == nil {
		if f.WorkRepo != "" && f.Tree != nil && strings.EqualFold(n.Repo, f.Tree.Repo) {
			return repoURL(f.WorkRepo)
		}
		return "https://" + hostOf(ref) + "/" + n.Repo
	}
	return ""
}

// repoURL gives a stored work repo in the https form repofinder reads; host/owner/repo gets its scheme.
func repoURL(s string) string {
	if strings.Contains(s, "://") || strings.Contains(s, "@") {
		return s
	}
	return "https://" + s
}

func validRepo(s string) bool {
	_, ok := repofinder.Identity(repoURL(s))
	return ok
}

// Launched triggers a reread of the child's campaign.
func (s *Service) Launched(source, id string) {
	if source != SourceKey {
		if s.Next != nil {
			s.Next.Launched(source, id)
		}
		return
	}
	if f, _, lerr := s.childOf(id); lerr == nil {
		s.startReread(f.ID, true)
	}
}
