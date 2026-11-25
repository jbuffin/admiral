package crewapi

import (
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/github"
)

// listing is one repo's crew/ directory as last read.
type listing struct {
	entries []github.DirEntry
	err     string
	// notFound is a repo or crew/ folder that isn't there: nothing published, but not proof a file was removed.
	notFound bool
	// noAccess is a repo gh.Repo answers 404 for: held as briefly as an error, and kept apart from err so the update check can name it.
	noAccess bool
	at       time.Time
}

func (l listing) fresh(now time.Time) bool {
	ttl := listingTTL
	if l.err != "" || l.noAccess {
		ttl = listingErrTT
	}
	return now.Sub(l.at) < ttl
}

func (l listing) blob(name string) (string, bool) {
	for _, e := range l.entries {
		if e.Name == name+".md" {
			return e.Blob, true
		}
	}
	return "", false
}

// listings caches each fleet's crew/ listings by repo, ignoring case; Share and Unpublish invalidate the share repo's.
type listings struct {
	mu sync.Mutex
	by map[string]listing
}

func listingKey(fleet, repo string) string { return fleet + "\x00" + strings.ToLower(repo) }

func (l *listings) get(now time.Time, fleet, repo string) (listing, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.by[listingKey(fleet, repo)]
	return v, ok && v.fresh(now)
}

func (l *listings) put(now time.Time, fleet, repo string, v listing) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.by == nil {
		l.by = map[string]listing{}
	}
	v.at = now
	l.by[listingKey(fleet, repo)] = v
}

func (l *listings) invalidate(fleet, repo string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.by, listingKey(fleet, repo))
}
