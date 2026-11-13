package shipconv

import (
	"sync"

	"github.com/jbuffin/admiral/internal/conversation"
)

// candFollow is one transcript read for PR URLs. The reader holds only recent tool calls, so the URLs found are kept apart.
type candFollow struct {
	launched string
	path     string
	reader   *conversation.Reader
	urls     []string
	seen     map[string]bool
}

type candidates struct {
	mu      sync.Mutex
	follows map[string]*candFollow
}

func candReader(harness string) *conversation.Reader {
	return conversation.NewReader(conversation.Options{Harness: harness, Keep: func(it conversation.Item) bool { return it.Kind == conversation.KindToolCall }, MaxBytes: 1 << 20})
}

// PRCandidates is the PR URLs the ship and its crew boats printed in tool results, ship first, then each boat in crew order.
// It reads what each transcript gained since the last call; a boat whose transcript is not found yet adds nothing until a later call finds it.
func (s *Service) PRCandidates(session string) []string {
	sh, ok := s.shipOf(session, false)
	if !ok {
		return nil
	}
	s.cands.mu.Lock()
	defer s.cands.mu.Unlock()
	if s.cands.follows == nil {
		s.cands.follows = map[string]*candFollow{}
	}
	var out []string
	seen := map[string]bool{}
	add := func(crew string) {
		w, ok, none := s.locateShip(sh, crew)
		k := key(session, crew)
		f := s.cands.follows[k]
		if f != nil && f.launched != sh.Launched {
			delete(s.cands.follows, k)
			f = nil
		}
		if ok && !none {
			if f == nil {
				f = &candFollow{launched: sh.Launched, seen: map[string]bool{}}
				s.cands.follows[k] = f
			}
			s.readCandidates(f, w)
		}
		if f == nil {
			return
		}
		for _, u := range f.urls {
			if !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	add("")
	for _, b := range sh.Crew {
		add(b.Name)
	}
	return out
}

func (s *Service) readCandidates(f *candFollow, w where) {
	file, info, p, err := w.open(true)
	if err != nil || file == nil {
		return
	}
	defer file.Close()
	if f.reader == nil || f.path != p {
		f.reader, f.path = candReader(w.harness), p
	}
	if _, err := f.reader.Refresh(file, info); err != nil {
		return
	}
	for _, u := range conversation.PRCandidates(f.reader.Items()) {
		if !f.seen[u] {
			f.seen[u] = true
			f.urls = append(f.urls, u)
		}
	}
}
