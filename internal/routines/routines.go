// Package routines stores a fleet's routines, its trigger log and Hold fire, and serves them over HTTP.
package routines

import (
	"errors"
	"net/http"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/server"
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
}

// Do is a launch or an AdmiralBot prompt, never both.
type Do struct {
	Launch *Launch `json:"launch,omitempty"`
	Prompt string  `json:"prompt,omitempty"`
}

type Routine struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	When      string    `json:"when"` // a 5-field cron expression in the server's local time zone
	Do        Do        `json:"do"`
	Before    Mode      `json:"before_acting"`
	Notify    bool      `json:"notify"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store reads and writes $ADMIRAL_HOME/routines.
type Store struct {
	Home   string
	Fleets func(key string) (config.Fleet, bool)
	Now    func() time.Time
}

var errTodo = errors.New("not implemented")

func (s *Store) List(fleet string) ([]Routine, error)                  { return []Routine{}, errTodo }
func (s *Store) Create(fleet string, r Routine) (Routine, error)       { return Routine{}, errTodo }
func (s *Store) Update(fleet, id string, r Routine) (Routine, error)   { return Routine{}, errTodo }
func (s *Store) Delete(fleet, id string) error                         { return errTodo }
func (s *Store) SetEnabled(fleet, id string, on bool) (Routine, error) { return Routine{}, errTodo }

// Outcome is what a trigger-log record decided.
type Outcome string

const (
	Launched Outcome = "launched"
	Proposed Outcome = "proposed"
	Skipped  Outcome = "skipped"
	Missed   Outcome = "missed"
	Failed   Outcome = "failed"
)

// Trigger is one trigger-log record.
type Trigger struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Routine  string    `json:"routine"`
	Outcome  Outcome   `json:"outcome"`
	Tier     string    `json:"tier"`
	Event    string    `json:"event,omitempty"`
	Sender   string    `json:"sender,omitempty"`
	Rule     string    `json:"rule,omitempty"`
	Verdict  string    `json:"verdict,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Action   string    `json:"action,omitempty"`
	Ship     string    `json:"ship,omitempty"`
	Undone   bool      `json:"undone,omitempty"`
	UndoneAt time.Time `json:"undone_at,omitempty"`
}

// AppendTrigger fills in an empty ID and a zero At.
func (s *Store) AppendTrigger(fleet string, t Trigger) (Trigger, error) { return Trigger{}, errTodo }

// Undo appends a mark that the launch record id was undone.
func (s *Store) Undo(fleet, id string) error { return errTodo }

// Recent is a routine's records newest first with marks folded in; limit 0 means all.
func (s *Store) Recent(fleet, routine string, limit int) ([]Trigger, error) {
	return []Trigger{}, errTodo
}

// Hold is one Hold fire switch.
type Hold struct {
	On bool      `json:"on"`
	By string    `json:"by,omitempty"` // "engineer" or actor.Via
	At time.Time `json:"at,omitempty"`
}

const Engineer = "engineer"

// SetHold turns the fleet's Hold fire on or off; an empty fleet is the global switch.
func (s *Store) SetHold(fleet string, on bool, by string) (Hold, error) { return Hold{}, errTodo }

// GetHold reads one switch; an empty fleet is the global one.
func (s *Store) GetHold(fleet string) (Hold, error) { return Hold{}, errTodo }

// HoldOn is true when the fleet's switch or the global one is on.
func (s *Store) HoldOn(fleet string) (bool, error) { return false, errTodo }

// Effective is the Before acting a routine runs with: min(stored, Ask first) while Hold fire is on.
func Effective(stored Mode, holdOn bool) Mode { return "" }

// Service serves the routes.
type Service struct {
	Store *Store
}

func (s *Service) Connected(*server.Client) {}

func (s *Service) Routes(mux *http.ServeMux) {
	notYet := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"not implemented"}`))
	}
	for _, p := range []string{
		"GET /api/fleets/{key}/routines", "POST /api/fleets/{key}/routines",
		"PUT /api/fleets/{key}/routines/{id}", "DELETE /api/fleets/{key}/routines/{id}",
		"POST /api/fleets/{key}/routines/{id}/on", "POST /api/fleets/{key}/routines/{id}/off",
		"GET /api/fleets/{key}/routines/{id}/runs",
		"GET /api/fleets/{key}/hold-fire", "POST /api/fleets/{key}/hold-fire/on", "POST /api/fleets/{key}/hold-fire/off",
		"GET /api/hold-fire", "POST /api/hold-fire/on", "POST /api/hold-fire/off",
	} {
		mux.HandleFunc(p, notYet)
	}
}
