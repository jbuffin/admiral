// Package routines stores a fleet's routines, its trigger log and Hold fire, and serves them over HTTP.
// Every file under $ADMIRAL_HOME/routines is read and written only here.
package routines

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/autopilotgate"
	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/textsafe"
)

// Mode is a routine's Before acting setting.
type Mode string

const (
	ReportOnly Mode = "report_only"
	AskFirst   Mode = "ask_first"
	Act        Mode = "act"
)

var (
	ErrUnknownFleet = errors.New("unknown fleet")
	ErrNotFound     = errors.New("no such routine")
	ErrInvalid      = errors.New("invalid routine")
	// ErrNotYours is Hold fire turned off by AdmiralBot, which may only turn it on.
	ErrNotYours = errors.New("only the engineer turns Hold fire off")
)

// Launch is a stored launch spec: the launch form's fields, plus the extra harness args the form has no field for.
type Launch struct {
	Harness string   `json:"harness"`
	Dir     string   `json:"dir"`
	Text    string   `json:"text"`
	Model   string   `json:"model,omitempty"`
	Effort  string   `json:"effort,omitempty"`
	Args    []string `json:"args,omitempty"`
	// Skill and Resources are the launch form's fields of the same names.
	Skill     launcher.Skill `json:"skill,omitzero"`
	Resources []string       `json:"resources,omitempty"`
}

// Do is a launch or an AdmiralBot prompt, never both.
type Do struct {
	Launch *Launch `json:"launch,omitempty"`
	Prompt string  `json:"prompt,omitempty"`
}

type Routine struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	When    string `json:"when"` // a 5-field cron expression in the server's local time zone
	On      *On    `json:"on,omitempty"`
	Do      Do     `json:"do"`
	Before  Mode   `json:"before_acting"`
	Notify  bool   `json:"notify"`
	// OffBy says who last turned the routine off when it was not the engineer.
	OffBy     string    `json:"off_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store reads and writes $ADMIRAL_HOME/routines.
type Store struct {
	Home   string
	Fleets func(key string) (config.Fleet, bool)
	Now    func() time.Time
	// ResourceKnown says whether the config names a resource; a Launch naming an unknown one is refused on save.
	ResourceKnown func(name string) bool
	// Events lists the kinds a fleet's source can emit; ok is false when the fleet has no such source.
	Events func(fleet, source string) (kinds []EventKind, ok bool)

	mu sync.Mutex
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func invalidf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// fleet resolves a key to a known fleet; a key that could name another directory is refused before it is looked up.
func (s *Store) fleet(key string) (config.Fleet, error) {
	if key == "" || key == "." || key == ".." || key != filepath.Base(key) || strings.ContainsAny(key, `/\`) {
		return config.Fleet{}, ErrUnknownFleet
	}
	f, ok := s.Fleets(key)
	if !ok {
		return config.Fleet{}, ErrUnknownFleet
	}
	return f, nil
}

func (s *Store) dir(key string) string { return filepath.Join(s.Home, "routines", key) }

// readJSON loads path into v; a file that doesn't parse is kept aside as .bad and reads as empty.
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if json.Unmarshal(b, v) != nil {
		return os.Rename(path, path+".bad")
	}
	return nil
}

// writeFile writes through a temp file in the same folder, so a crash leaves the old file.
func writeFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

type routinesFile struct {
	Routines []Routine `json:"routines"`
}

func (s *Store) load(key string) ([]Routine, error) {
	var f routinesFile
	if err := readJSON(filepath.Join(s.dir(key), "routines.json"), &f); err != nil {
		return nil, err
	}
	if f.Routines == nil {
		f.Routines = []Routine{}
	}
	return f.Routines, nil
}

func (s *Store) save(key string, list []Routine) error {
	return writeFile(filepath.Join(s.dir(key), "routines.json"), routinesFile{list})
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// validate checks what the form can get wrong. It never calls autopilotgate.Check, which posts an Inbox notice.
func (s *Store) validate(f config.Fleet, r Routine) error {
	if strings.TrimSpace(r.Name) == "" {
		return invalidf("name is required")
	}
	if err := checkName(r.Name); err != nil {
		return err
	}
	if r.On != nil {
		if err := s.validateOn(f, r); err != nil {
			return err
		}
	} else {
		sched, err := ParseCron(r.When)
		if err != nil {
			return invalidf("when: %v", err)
		}
		if sched.Next(s.now()).IsZero() {
			return invalidf("when: the cron expression %q never fires", r.When)
		}
	}
	switch r.Before {
	case ReportOnly, AskFirst, Act:
	default:
		return invalidf("before_acting must be report_only, ask_first or act, not %q", r.Before)
	}
	switch l := r.Do.Launch; {
	case l != nil && r.Do.Prompt != "":
		return invalidf("do takes a launch or a prompt, not both")
	case l == nil && r.Do.Prompt == "":
		return invalidf("do needs a launch or a prompt")
	case l == nil && strings.TrimSpace(r.Do.Prompt) == "":
		return invalidf("the prompt is empty")
	case l == nil:
		return nil
	}
	return s.validateLaunch(f, *r.Do.Launch)
}

// maxNameRunes caps a name, which AdmiralBot reads through its tools and in the [Routine: <name>] chat prefix.
const maxNameRunes = 80

// checkName refuses what could pass for the end of the chat prefix or a line of its own.
func checkName(name string) error {
	if utf8.RuneCountInString(name) > maxNameRunes {
		return invalidf("name is longer than %d characters", maxNameRunes)
	}
	for _, c := range name {
		if textsafe.Hidden(c) || c == '[' || c == ']' {
			return invalidf("name can't hold control or invisible characters or square brackets")
		}
	}
	return nil
}

func (s *Store) validateLaunch(f config.Fleet, l Launch) error {
	if l.Harness == "" || strings.TrimSpace(l.Dir) == "" || strings.TrimSpace(l.Text) == "" {
		return invalidf("do.launch needs a harness, a dir and a prompt text")
	}
	dock, ok := f.Harness(l.Harness)
	if !ok {
		return invalidf("fleet %s has no %s dock", f.Key, l.Harness)
	}
	if !autopilotgate.Proven(s.Home, f.Key, l.Harness) {
		return invalidf("autopilot is not proven on %s in fleet %s: its guard has not passed", l.Harness, f.Key)
	}
	if ok, reason := autopilotgate.Advisor(f, capabilities.Open(s.Home)); !ok {
		return invalidf("autopilot is not proven on %s in fleet %s: its advisor is not proven (%s)", l.Harness, f.Key, reason)
	}
	if err := launcher.AutopilotArgs(l.Harness, dock.Args, l.Args); err != nil {
		return invalidf("args: %v", err)
	}
	if l.Skill != (launcher.Skill{}) {
		if err := launcher.CheckSkill(l.Skill); err != nil {
			return invalidf("skill: %v", err)
		}
	}
	if s.ResourceKnown != nil {
		for _, name := range l.Resources {
			if !s.ResourceKnown(name) {
				return invalidf("no resource named %q", name)
			}
		}
	}
	return nil
}

func (s *Store) List(fleet string) ([]Routine, error) {
	if _, err := s.fleet(fleet); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(fleet)
}

func (s *Store) Create(fleet string, r Routine) (Routine, error) {
	f, err := s.fleet(fleet)
	if err != nil {
		return Routine{}, err
	}
	if err := s.validate(f, r); err != nil {
		return Routine{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load(fleet)
	if err != nil {
		return Routine{}, err
	}
	r.ID, r.OffBy, r.CreatedAt, r.UpdatedAt = newID(), "", s.now(), s.now()
	if err := s.save(fleet, append(list, r)); err != nil {
		return Routine{}, err
	}
	return r, nil
}

func (s *Store) Update(fleet, id string, r Routine) (Routine, error) {
	f, err := s.fleet(fleet)
	if err != nil {
		return Routine{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load(fleet)
	if err != nil {
		return Routine{}, err
	}
	i := indexOf(list, id)
	if i < 0 {
		return Routine{}, ErrNotFound
	}
	if err := s.validate(f, r); err != nil {
		return Routine{}, err
	}
	// only the on and off routes change the switch
	r.ID, r.Enabled, r.OffBy, r.CreatedAt, r.UpdatedAt = id, list[i].Enabled, list[i].OffBy, list[i].CreatedAt, s.now()
	list[i] = r
	if err := s.save(fleet, list); err != nil {
		return Routine{}, err
	}
	return r, nil
}

func (s *Store) Delete(fleet, id string) error {
	if _, err := s.fleet(fleet); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load(fleet)
	if err != nil {
		return err
	}
	i := indexOf(list, id)
	if i < 0 {
		return ErrNotFound
	}
	return s.save(fleet, append(list[:i], list[i+1:]...))
}

// SetEnabled flips the switch without validating the rest: a routine that was saved can always be turned off.
func (s *Store) SetEnabled(fleet, id string, on bool) (Routine, error) {
	return s.SetEnabledBy(fleet, id, on, "")
}

// SetEnabledBy is SetEnabled that stamps who turned the routine off when it was not the engineer.
func (s *Store) SetEnabledBy(fleet, id string, on bool, by string) (Routine, error) {
	if _, err := s.fleet(fleet); err != nil {
		return Routine{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load(fleet)
	if err != nil {
		return Routine{}, err
	}
	i := indexOf(list, id)
	if i < 0 {
		return Routine{}, ErrNotFound
	}
	list[i].Enabled, list[i].UpdatedAt = on, s.now()
	list[i].OffBy = ""
	if !on && by != Engineer {
		list[i].OffBy = by
	}
	if err := s.save(fleet, list); err != nil {
		return Routine{}, err
	}
	return list[i], nil
}

// ViaProposal is the Via of a launch that came from an accepted proposal.
const ViaProposal = "proposal"

// ProposalExpiry is when a proposal made at now closes: at the next run or after 24 hours, whichever comes first.
func ProposalExpiry(next, now time.Time) time.Time {
	day := now.Add(24 * time.Hour)
	if next.IsZero() || next.After(day) {
		return day
	}
	return next
}

func indexOf(list []Routine, id string) int {
	for i, r := range list {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// Outcome is what a trigger-log record decided.
type Outcome string

const (
	Launched Outcome = "launched"
	Proposed Outcome = "proposed"
	Skipped  Outcome = "skipped"
	Missed   Outcome = "missed"
	Failed   Outcome = "failed"
	// Reported is a Report only run: Action says what it would have done.
	Reported Outcome = "reported"
	// Breaker is the record the circuit breaker writes when it sets Before acting to Ask first.
	Breaker Outcome = "breaker"
)

// Trigger is one trigger-log record.
type Trigger struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Routine string    `json:"routine"`
	Outcome Outcome   `json:"outcome"`
	Tier    string    `json:"tier"`
	Event   string    `json:"event,omitempty"`
	Sender  string    `json:"sender,omitempty"`
	Rule    string    `json:"rule,omitempty"`
	Verdict string    `json:"verdict,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Action  string    `json:"action,omitempty"`
	Ship    string    `json:"ship,omitempty"`
	// DedupeKey and Costs are set on an event record: the Lookout's key and what its judge calls cost.
	DedupeKey string `json:"dedupe_key,omitempty"`
	Costs     []Cost `json:"costs,omitempty"`
	// Via marks a launch that came from an accepted proposal.
	Via      string    `json:"via,omitempty"`
	Undone   bool      `json:"undone,omitempty"`
	UndoneAt time.Time `json:"undone_at,omitzero"`
}

// logLine is one line of triggers.jsonl: a record, or a mark that an earlier launch was undone or its ship ended failed.
type logLine struct {
	Trigger
	UndoOf string `json:"undo_of,omitempty"`
	FailOf string `json:"fail_of,omitempty"`
}

func (s *Store) appendLine(fleet string, l logLine) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	dir := s.dir(fleet)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "triggers.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// readLog skips a line that doesn't parse: a crash can cut one short.
func (s *Store) readLog(fleet string) ([]logLine, error) {
	f, err := os.Open(filepath.Join(s.dir(fleet), "triggers.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []logLine
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var l logLine
		if len(line) > 0 && json.Unmarshal(line, &l) == nil && (l.ID != "" || l.UndoOf != "" || l.FailOf != "") {
			out = append(out, l)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// AppendTrigger fills in an empty ID and a zero At.
func (s *Store) AppendTrigger(fleet string, t Trigger) (Trigger, error) {
	if _, err := s.fleet(fleet); err != nil {
		return Trigger{}, err
	}
	if t.ID == "" {
		t.ID = newID()
	}
	if t.At.IsZero() {
		t.At = s.now()
	}
	t.Undone, t.UndoneAt = false, time.Time{}
	s.mu.Lock()
	defer s.mu.Unlock()
	return t, s.appendLine(fleet, logLine{Trigger: t})
}

// Undo appends a mark that the launch record id was undone.
func (s *Store) Undo(fleet, id string) error {
	if _, err := s.fleet(fleet); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lines, err := s.readLog(fleet)
	if err != nil {
		return err
	}
	for _, l := range lines {
		if l.UndoOf == id {
			return nil
		}
	}
	for _, l := range lines {
		if l.ID == id {
			if l.Outcome != Launched {
				return invalidf("only a launch can be undone")
			}
			return s.appendLine(fleet, logLine{Trigger: Trigger{At: s.now()}, UndoOf: id})
		}
	}
	return ErrNotFound
}

// MarkFailed appends a mark that the launch record id's ship ended failed; Recent then reads the record as failed.
func (s *Store) MarkFailed(fleet, id, reason string) error {
	if _, err := s.fleet(fleet); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lines, err := s.readLog(fleet)
	if err != nil {
		return err
	}
	for _, l := range lines {
		if l.FailOf == id {
			return nil
		}
	}
	for _, l := range lines {
		if l.ID == id {
			if l.Outcome != Launched {
				return invalidf("only a launch can end failed")
			}
			return s.appendLine(fleet, logLine{Trigger: Trigger{At: s.now(), Reason: reason}, FailOf: id})
		}
	}
	return ErrNotFound
}

// SetBefore changes only Before acting. It skips the save-time checks, as SetEnabled does: the circuit breaker must be able to tighten a routine whose launch no longer passes them.
func (s *Store) SetBefore(fleet, id string, m Mode) (Routine, error) {
	if _, err := s.fleet(fleet); err != nil {
		return Routine{}, err
	}
	switch m {
	case ReportOnly, AskFirst, Act:
	default:
		return Routine{}, invalidf("before_acting must be report_only, ask_first or act, not %q", m)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load(fleet)
	if err != nil {
		return Routine{}, err
	}
	i := indexOf(list, id)
	if i < 0 {
		return Routine{}, ErrNotFound
	}
	// UpdatedAt stays: the scheduler counts a routine's next run from its last save, and this change must not move it
	list[i].Before = m
	if err := s.save(fleet, list); err != nil {
		return Routine{}, err
	}
	return list[i], nil
}

// FleetKeys lists the fleets that have routines stored, which the scheduler walks.
func (s *Store) FleetKeys() []string {
	entries, err := os.ReadDir(filepath.Join(s.Home, "routines"))
	if err != nil {
		return nil
	}
	var keys []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := s.fleet(e.Name()); err == nil {
				keys = append(keys, e.Name())
			}
		}
	}
	return keys
}

// Recent is a routine's records newest first with marks folded in; limit 0 means all.
func (s *Store) Recent(fleet, routine string, limit int) ([]Trigger, error) {
	recs, err := s.Records(fleet)
	if err != nil {
		return nil, err
	}
	out := []Trigger{}
	for _, r := range recs {
		if r.Routine == routine {
			out = append(out, r)
			if limit > 0 && len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

// validateOn checks an event routine: no schedule, a source and kind the fleet's catalog lists, and a launch to run.
func (s *Store) validateOn(f config.Fleet, r Routine) error {
	if r.When != "" {
		return invalidf("an event routine has no when: a routine runs on a schedule or on an event, not both")
	}
	if r.Do.Prompt != "" {
		return invalidf("an event routine launches a ship; a source event never prompts AdmiralBot")
	}
	on := *r.On
	if on.Source == "" || on.Kind == "" {
		return invalidf("on needs a source and a kind")
	}
	var kinds []EventKind
	ok := false
	if s.Events != nil {
		kinds, ok = s.Events(f.Key, on.Source)
	}
	if !ok {
		return invalidf("on: the fleet has no source %q that raises events", on.Source)
	}
	for _, k := range kinds {
		if k.Kind == on.Kind {
			if k.Panel != on.Panel {
				return invalidf("on: the kind %q belongs to panel %q, not %q", on.Kind, k.Panel, on.Panel)
			}
			return nil
		}
	}
	return invalidf("on: the source %q lists no event kind %q", on.Source, on.Kind)
}
