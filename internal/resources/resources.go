// Package resources tracks locked resources. A ship holds one unit of each
// resource recorded on it while it is underway or needs you; a lock and a launch in flight hold one too.
package resources

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/ships"
)

const (
	DefaultCheckTimeout = 10 * time.Second
	DefaultCheckEvery   = 15 * time.Second
)

var (
	ErrBusy    = errors.New("no unit of the resource is free")
	ErrUnknown = errors.New("no such resource")
)

const (
	CheckOK      = "ok"
	CheckMissing = "missing"
	CheckPending = "pending" // the check has not finished once yet
	CheckNone    = "none"    // the resource has no check
)

const (
	Busy    = "busy"
	Missing = "missing"
	Pending = "pending"
)

type Options struct {
	Config func() *config.Config
	// Ships is called under the Set's lock and must not call back into the Set.
	// It includes launched ships the host has not listed yet.
	Ships                    func() []ships.Ship
	CheckTimeout, CheckEvery time.Duration
	// OnChange runs outside the Set's lock. It must not block.
	OnChange func()
}

type Holder struct {
	Kind    string `json:"kind"` // ship or lock
	Session string `json:"session,omitempty"`
	Label   string `json:"label,omitempty"`
}

// State has no timestamps, so an unchanged state doesn't push again.
type State struct {
	Name     string   `json:"name"`
	Capacity int      `json:"capacity"`
	InUse    int      `json:"in_use"` // holders and reservations
	Holders  []Holder `json:"holders"`
	Check    string   `json:"check"`
}

type Wait struct {
	Name string `json:"name"`
	Why  string `json:"why"` // busy, missing or pending
}

type Set struct {
	opt Options

	mu       sync.Mutex // guards everything below, and makes check, reserve and lock one step
	results  map[string]result
	reserved map[string]int
	locks    map[string][]*lock
	changed  chan struct{}
	checking map[string]*sync.Mutex // one check at a time per resource
	kick     chan struct{}
}

type result struct {
	argv []string // the check this result is for
	ok   bool
}

type lock struct{ label string }

// Call Run to keep the checks going.
func New(opt Options) *Set {
	if opt.CheckTimeout == 0 {
		opt.CheckTimeout = DefaultCheckTimeout
	}
	if opt.CheckEvery == 0 {
		opt.CheckEvery = DefaultCheckEvery
	}
	return &Set{opt: opt, results: map[string]result{}, reserved: map[string]int{}, locks: map[string][]*lock{},
		changed: make(chan struct{}), checking: map[string]*sync.Mutex{}, kick: make(chan struct{}, 1)}
}

func (s *Set) Refresh() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Set) Run(ctx context.Context) {
	tick := time.NewTicker(s.opt.CheckEvery)
	defer tick.Stop()
	for {
		s.checkAll(false)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.kick:
		}
	}
}

func (s *Set) CheckNow() { s.checkAll(true) }

func (s *Set) checkAll(wait bool) {
	var wg sync.WaitGroup
	for name, def := range s.opt.Config().Resources {
		if len(def.Check) == 0 {
			continue
		}
		s.mu.Lock()
		gate := s.checking[name]
		if gate == nil {
			gate = &sync.Mutex{}
			s.checking[name] = gate
		}
		s.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if wait {
				gate.Lock()
			} else if !gate.TryLock() {
				return
			}
			defer gate.Unlock()
			s.record(name, def.Check, s.run(def.Check))
		}()
	}
	wg.Wait()
}

// run puts the check in its own process group so a timeout kills the children too.
func (s *Set) run(argv []string) bool {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(s.opt.CheckTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err == nil
	case <-timer.C:
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return false
	}
}

func (s *Set) record(name string, argv []string, ok bool) {
	s.mu.Lock()
	old := s.results[name]
	s.results[name] = result{argv, ok}
	same := slices.Equal(old.argv, argv) && old.ok == ok && old.argv != nil
	s.mu.Unlock()
	if !same {
		s.announce()
	}
}

// announce wakes Changed's waiters and runs OnChange.
func (s *Set) announce() {
	s.Wake()
	if s.opt.OnChange != nil {
		s.opt.OnChange()
	}
}

// Wake is for changes the Set can't see itself, such as a ship that stopped
// holding a unit. It does not run OnChange.
func (s *Set) Wake() {
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

// Changed: fetch a fresh channel each time.
func (s *Set) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

// checkState: the caller holds mu.
func (s *Set) checkState(name string, def config.Resource) string {
	if len(def.Check) == 0 {
		return CheckNone
	}
	r, ok := s.results[name]
	switch {
	case !ok || !slices.Equal(r.argv, def.Check):
		return CheckPending
	case r.ok:
		return CheckOK
	}
	return CheckMissing
}

func (s *Set) shipHolders(defs map[string]config.Resource) map[string][]string {
	held := map[string][]string{}
	for _, sh := range s.opt.Ships() {
		if sh.Status != ships.StatusUnderway && sh.Status != ships.StatusNeedsYou {
			continue
		}
		for _, name := range sh.Resources {
			if _, ok := defs[name]; ok && !slices.Contains(held[name], sh.Session) {
				held[name] = append(held[name], sh.Session)
			}
		}
	}
	for _, sessions := range held {
		sort.Strings(sessions)
	}
	return held
}

func (s *Set) State() []State {
	defs := s.opt.Config().Resources
	s.mu.Lock()
	defer s.mu.Unlock()
	held := s.shipHolders(defs)
	out := make([]State, 0, len(defs))
	for name, def := range defs {
		st := State{Name: name, Capacity: def.Capacity, Holders: []Holder{}, Check: s.checkState(name, def)}
		for _, sess := range held[name] {
			st.Holders = append(st.Holders, Holder{Kind: "ship", Session: sess})
		}
		for _, l := range s.locks[name] {
			st.Holders = append(st.Holders, Holder{Kind: "lock", Label: l.label})
		}
		st.InUse = len(st.Holders) + s.reserved[name]
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// free: the caller holds mu.
func (s *Set) free(name string, def config.Resource, held map[string][]string) bool {
	return len(held[name])+len(s.locks[name])+s.reserved[name] < def.Capacity
}

// Reserve takes a unit of each name, or none. The release is idempotent.
func (s *Set) Reserve(names []string) (release func(), waits []Wait) {
	defs := s.opt.Config().Resources
	s.mu.Lock()
	held := s.shipHolders(defs)
	names = slices.Clone(names)
	sort.Strings(names)
	names = slices.Compact(names)
	for _, name := range names {
		def, ok := defs[name]
		switch check := s.checkState(name, def); {
		case !ok:
			waits = append(waits, Wait{name, Missing})
		case check == CheckPending:
			waits = append(waits, Wait{name, Pending})
		case check == CheckMissing:
			waits = append(waits, Wait{name, Missing})
		case !s.free(name, def, held):
			waits = append(waits, Wait{name, Busy})
		}
	}
	if waits != nil {
		s.mu.Unlock()
		return nil, waits
	}
	for _, name := range names {
		s.reserved[name]++
	}
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			for _, name := range names {
				s.reserved[name]--
			}
			s.mu.Unlock()
			s.announce()
		})
	}, nil
}

// Lock doesn't wait, and it ignores the check. The release is idempotent.
func (s *Set) Lock(name, label string) (release func(), err error) {
	defs := s.opt.Config().Resources
	s.mu.Lock()
	def, ok := defs[name]
	if !ok {
		s.mu.Unlock()
		return nil, ErrUnknown
	}
	if !s.free(name, def, s.shipHolders(defs)) {
		s.mu.Unlock()
		return nil, ErrBusy
	}
	l := &lock{label}
	s.locks[name] = append(s.locks[name], l)
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.locks[name] = slices.DeleteFunc(s.locks[name], func(x *lock) bool { return x == l })
			s.mu.Unlock()
			s.announce()
		})
	}, nil
}
