package ships

import (
	"time"

	"github.com/jbuffin/admiral/internal/repokey"
)

// originLifetime lets a remote added later show up without a git call per ship per list.
const originLifetime = 5 * time.Minute

type originEntry struct {
	slug string
	at   time.Time
}

// originRepo is the owner/repo of dir's origin remote, "" when there is none. A miss reads git once per
// directory per lifetime; no lock is held while git runs.
func (s *Service) originRepo(dir string) string {
	if dir == "" {
		return ""
	}
	s.originMu.Lock()
	e, ok := s.origins[dir]
	s.originMu.Unlock()
	if ok && time.Since(e.at) < originLifetime {
		return e.slug
	}
	read := s.originSlug
	if read == nil {
		read = func(d string) string { slug, _ := repokey.OriginSlug(d); return slug }
	}
	slug := read(dir)
	s.originMu.Lock()
	if s.origins == nil {
		s.origins = map[string]originEntry{}
	}
	s.origins[dir] = originEntry{slug: slug, at: time.Now()}
	s.originMu.Unlock()
	return slug
}

// checkoutOf is where a ship works: its worktree when it has one, else its dir.
func checkoutOf(sh Ship) string {
	if sh.Worktree != "" {
		return sh.Worktree
	}
	return sh.Dir
}
