// Package allowrules keeps each fleet's allow rules and skipped allow rule proposals (ADR 0046) in
// $ADMIRAL_HOME/allow-rules.json, and renders a fleet's Codex rules as the rules file Admiral owns.
package allowrules

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// FileName is the store in $ADMIRAL_HOME.
const FileName = "allow-rules.json"

// Rule lets one harness's ships in one fleet run one narrow kind of call without asking. A Claude or
// Copilot rule holds its harness's own rule text for one repo; a Codex rule is a prefix_rule pattern
// for every repo of the fleet, so it has no repo.
type Rule struct {
	Harness string `json:"harness"`
	// Repo is the remote's owner/repo, else the checkout's absolute path (RepoKey).
	Repo    string    `json:"repo,omitempty"`
	Rule    string    `json:"rule,omitempty"`
	Pattern []string  `json:"pattern,omitempty"`
	Added   time.Time `json:"added,omitzero"`
}

// Same is whether a and b allow the same thing; when they were added doesn't count.
func (r Rule) Same(o Rule) bool {
	return r.Harness == o.Harness && r.Repo == o.Repo && r.Rule == o.Rule && slices.Equal(r.Pattern, o.Pattern)
}

// Check refuses a rule that has the wrong shape for its harness.
func (r Rule) Check() error {
	switch r.Harness {
	case "claude", "copilot":
		if r.Rule == "" || r.Repo == "" || len(r.Pattern) > 0 {
			return fmt.Errorf("a %s allow rule needs a rule and a repo, and no pattern", r.Harness)
		}
	case "codex":
		if len(r.Pattern) == 0 || r.Rule != "" || r.Repo != "" {
			return errors.New("a codex allow rule needs a pattern, and no rule or repo: it covers every repo of the fleet")
		}
	default:
		return fmt.Errorf("unknown harness %q", r.Harness)
	}
	for _, s := range append([]string{r.Repo, r.Rule}, r.Pattern...) {
		if strings.IndexFunc(s, func(c rune) bool { return c < 0x20 || c == 0x7f }) >= 0 {
			return errors.New("an allow rule contains a control character")
		}
	}
	for _, s := range r.Pattern {
		if s == "" {
			return errors.New("a codex allow rule's pattern has an empty token")
		}
	}
	return nil
}

// Fleet is one fleet's allow rules and the proposals the engineer skipped.
type Fleet struct {
	Rules   []Rule `json:"rules"`
	Skipped []Rule `json:"skipped"`
}

type file struct {
	Fleets map[string]*Fleet `json:"fleets"`
}

// Store reads and writes the file on every call, because the server and `admiral crew` inside a
// ship are separate processes. OnChange runs after a write that changed a fleet's rules.
type Store struct {
	home     string
	OnChange func(fleet string)
}

// Open returns a store over home. It does no I/O.
func Open(home string) *Store { return &Store{home: home} }

func (s *Store) path() string { return filepath.Join(s.home, FileName) }

// load reads the file; a missing file is an empty store.
func (s *Store) load() (*file, error) {
	f := &file{Fleets: map[string]*Fleet{}}
	raw, err := os.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	var parsed file
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return f, fmt.Errorf("%s is corrupt: %w", s.path(), err)
	}
	if parsed.Fleets != nil {
		f = &parsed
	}
	return f, nil
}

// List is a fleet's rules and skips, each in the order they were added.
func (s *Store) List(fleet string) (Fleet, error) {
	f, err := s.load()
	if err != nil {
		return Fleet{}, err
	}
	out := Fleet{Rules: []Rule{}, Skipped: []Rule{}}
	if fl := f.Fleets[fleet]; fl != nil {
		out.Rules = append(out.Rules, fl.Rules...)
		out.Skipped = append(out.Skipped, fl.Skipped...)
	}
	return out, nil
}

// For is the fleet's rules a ship of harness in repo gets; repo is ignored for Codex.
func (s *Store) For(fleet, harness, repo string) ([]Rule, error) {
	fl, err := s.List(fleet)
	var out []Rule
	for _, r := range fl.Rules {
		if r.Harness == harness && (harness == "codex" || r.Repo == repo) {
			out = append(out, r)
		}
	}
	return out, err
}

// Add keeps a rule, once.
func (s *Store) Add(fleet string, r Rule) error {
	return s.edit(fleet, r, true, func(fl *Fleet) bool { return add(&fl.Rules, r) })
}

// Remove drops a rule; removing one that isn't there is no error.
func (s *Store) Remove(fleet string, r Rule) error {
	return s.edit(fleet, r, true, func(fl *Fleet) bool { return remove(&fl.Rules, r) })
}

// Skip keeps a proposal from being made again.
func (s *Store) Skip(fleet string, r Rule) error {
	return s.edit(fleet, r, false, func(fl *Fleet) bool { return add(&fl.Skipped, r) })
}

// Unskip lets a skipped proposal be made again.
func (s *Store) Unskip(fleet string, r Rule) error {
	return s.edit(fleet, r, false, func(fl *Fleet) bool { return remove(&fl.Skipped, r) })
}

func add(list *[]Rule, r Rule) bool {
	if slices.ContainsFunc(*list, r.Same) {
		return false
	}
	if r.Added.IsZero() {
		r.Added = time.Now().UTC().Truncate(time.Second)
	}
	*list = append(*list, r)
	return true
}

func remove(list *[]Rule, r Rule) bool {
	n := len(*list)
	*list = slices.DeleteFunc(*list, r.Same)
	return len(*list) != n
}

func (s *Store) edit(fleet string, r Rule, rules bool, change func(*Fleet) bool) error {
	if fleet == "" {
		return errors.New("an allow rule needs a fleet")
	}
	if err := r.Check(); err != nil {
		return err
	}
	changed, err := s.update(func(f *file) bool {
		fl := f.Fleets[fleet]
		if fl == nil {
			fl = &Fleet{}
			f.Fleets[fleet] = fl
		}
		return change(fl)
	})
	if err == nil && changed && rules && s.OnChange != nil {
		s.OnChange(fleet)
	}
	return err
}

// update re-reads the file under the lock, applies change and writes it back when it changed anything.
// A corrupt file refuses the write, so no rule or skip the engineer made is dropped by a rewrite.
func (s *Store) update(change func(*file) bool) (bool, error) {
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		return false, err
	}
	lock, err := os.OpenFile(s.path()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return false, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	f, err := s.load()
	if err != nil {
		return false, err
	}
	if !change(f) {
		return false, nil
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return false, err
	}
	return true, WriteFile(s.path(), append(raw, '\n'))
}

// WriteFile replaces path through a temp file beside it, mode 0600.
func WriteFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}
