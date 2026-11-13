package sources

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync"

	"github.com/jbuffin/admiral/internal/config"
)

// Supervisor reconciles against every config reload, whoever caused it.
type Supervisor struct {
	Home   string
	Config *config.Manager
	// OnChange must not block.
	OnChange func()
	// OnEvent receives each event a source raised that passed its catalog; it must not block.
	OnEvent func(source string, ev Event)

	mu      sync.Mutex
	ctx     context.Context
	runners map[string]*runner
	cancels map[string]context.CancelFunc
	// stopping holds removed runners until they exit, so a source added again
	// under the same key waits for the old process.
	stopping map[string]*runner
	order    []string
	// envs is each source's fleet env as its runner started with it, and
	// routing the fleets' claims as the GitHub sources last saw them.
	envs    map[string]map[string]string
	routing string
	wg      sync.WaitGroup
}

// Every plugin is stopped before Wait returns.
func (s *Supervisor) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.runners = map[string]*runner{}
	s.cancels = map[string]context.CancelFunc{}
	s.stopping = map[string]*runner{}
	s.envs = map[string]map[string]string{}
	s.mu.Unlock()
	changes, unsubscribe := s.Config.Subscribe()
	s.Reconcile()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer unsubscribe()
		for {
			select {
			case <-changes:
				s.Reconcile()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Supervisor) Wait() { s.wg.Wait() }

func (s *Supervisor) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

// Reconcile makes the runners match the current config: new sources start,
// ones whose plugin or settings changed restart, removed ones stop.
func (s *Supervisor) Reconcile() { s.reconcile("", 0) }

// ReconcileForce is Reconcile that also restarts source key, unless it was
// restarted after generation gen (see Generation).
func (s *Supervisor) ReconcileForce(key string, gen uint64) { s.reconcile(key, gen) }

func (s *Supervisor) Generation(key string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.runners[key]; r != nil {
		return r.gen.Load()
	}
	return 0
}

func (s *Supervisor) reconcile(force string, gen uint64) {
	cfg := s.Config.Current()
	s.mu.Lock()
	if s.ctx == nil || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	dirty := false
	wanted := map[string]bool{}
	order := make([]string, 0, len(cfg.SourceList))
	for _, def := range cfg.SourceList {
		wanted[def.Key] = true
		order = append(order, def.Key)
		env := fleetEnv(cfg, def.Fleet)
		r := s.runners[def.Key]
		if r == nil {
			s.envs[def.Key] = env
			s.start(def)
			dirty = true
			continue
		}
		envChanged := !maps.Equal(s.envs[def.Key], env)
		s.envs[def.Key] = env
		r.mu.Lock()
		old := r.def
		r.def = def
		r.mu.Unlock()
		switch {
		case !old.SameRun(def), envChanged:
			r.restart()
			dirty = true
		case def.Key == force && r.gen.Load() == gen:
			r.restart()
			dirty = true
		case old.Name != def.Name, !slices.Equal(old.PanelOrder, def.PanelOrder):
			dirty = true
		}
	}
	routing := routingOf(cfg.Fleets)
	if s.routing != "" && routing != s.routing {
		for _, r := range s.runners { // SameRun ignores fleets: a changed claim needs a new search
			go r.refreshNow()
		}
	}
	s.routing = routing
	for key, r := range s.runners {
		if !wanted[key] {
			s.cancels[key]()
			s.stopping[key] = r
			delete(s.runners, key)
			delete(s.envs, key)
			delete(s.cancels, key)
			dirty = true
		}
	}
	if !equalStrings(order, s.order) {
		dirty = true
	}
	s.order = order
	s.mu.Unlock()
	if dirty {
		s.changed()
	}
}

// s.mu must be held.
func (s *Supervisor) start(def config.Source) {
	ctx, cancel := context.WithCancel(s.ctx)
	r := newRunner(ctx, s.Home, s.Config.Current, def, s.changed, s.stopping[def.Key])
	s.runners[def.Key], s.cancels[def.Key] = r, cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-r.done
		cancel()
		s.mu.Lock()
		if s.stopping[def.Key] == r {
			delete(s.stopping, def.Key)
		}
		s.mu.Unlock()
	}()
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Supervisor) runner(key string) *runner {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runners[key]
}

func (s *Supervisor) Restart(key string) bool {
	r := s.runner(key)
	if r == nil {
		return false
	}
	r.restart()
	return true
}

func (s *Supervisor) snapshot() []sourceState {
	s.mu.Lock()
	order := append([]string(nil), s.order...)
	runners := make([]*runner, len(order))
	for i, k := range order {
		runners[i] = s.runners[k]
	}
	s.mu.Unlock()
	out := make([]sourceState, 0, len(order))
	for _, r := range runners {
		if r != nil {
			out = append(out, r.snapshot())
		}
	}
	return out
}

func fleetEnv(cfg *config.Config, fleet string) map[string]string {
	f, _ := cfg.Fleet(fleet)
	return f.Env
}

func routingOf(fleets []config.Fleet) string {
	type claim struct {
		Key       string
		Roots     []string
		CloneRoot string
		Owners    []string
	}
	list := make([]claim, len(fleets))
	for i, f := range fleets {
		list[i] = claim{f.Key, f.SearchRoots, f.CloneRoot, f.Owners}
	}
	b, _ := json.Marshal(list)
	return string(b)
}
