package routines

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/server"
)

// Service serves the routes.
type Service struct {
	Store *Store
	// OnChange runs after a routine is saved, deleted or switched.
	OnChange func()
	// Config reads and saves the fleet's Limits; Judge runs the drafters' judge calls (judge.Run when nil).
	Config *config.Manager
	Judge  func(context.Context, judge.Choice, judge.Call) (judge.Reply, error)
	// Stop stops a ship by session name and returns ErrShipNotListed when the host does not list it.
	Stop func(session string) error

	undoMu sync.Mutex

	// ordersMu holds a Copy's snapshot, store write, config write and restore apart from any other standing-orders save.
	ordersMu sync.Mutex
}

func (s *Service) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

// Connected sends nothing: the screen asks when it opens.
func (s *Service) Connected(*server.Client) {}

// Each direction of a switch has its own path: the actor guard matches method and path only.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fleets/{key}/routines", s.serveList)
	mux.HandleFunc("POST /api/fleets/{key}/routines", s.serveCreate)
	mux.HandleFunc("PUT /api/fleets/{key}/routines/{id}", s.serveUpdate)
	mux.HandleFunc("DELETE /api/fleets/{key}/routines/{id}", s.serveDelete)
	mux.HandleFunc("POST /api/fleets/{key}/routines/{id}/on", s.serveEnable(true))
	mux.HandleFunc("POST /api/fleets/{key}/routines/{id}/off", s.serveEnable(false))
	mux.HandleFunc("GET /api/fleets/{key}/routines/{id}/runs", s.serveRuns)
	mux.HandleFunc("POST /api/fleets/{key}/routines/{id}/mute", s.serveMute)
	mux.HandleFunc("GET /api/fleets/{key}/routines/{id}/runs/{trigger}", s.serveRun)
	mux.HandleFunc("POST /api/fleets/{key}/routines/{id}/runs/{trigger}/undo", s.serveUndo)
	mux.HandleFunc("GET /api/fleets/{key}/hold-fire", s.serveFleetHold)
	mux.HandleFunc("POST /api/fleets/{key}/hold-fire/on", s.serveSetHold(true, false))
	mux.HandleFunc("POST /api/fleets/{key}/hold-fire/off", s.serveSetHold(false, false))
	mux.HandleFunc("GET /api/fleets/{key}/standing-orders", s.serveOrdersGet)
	mux.HandleFunc("PUT /api/fleets/{key}/standing-orders", s.serveOrdersPut)
	mux.HandleFunc("POST /api/fleets/{key}/standing-orders/copy", s.serveOrdersCopy)
	mux.HandleFunc("POST /api/fleets/{key}/standing-orders/draft", s.serveOrdersDraft)
	mux.HandleFunc("POST /api/fleets/{key}/routines/draft", s.serveRoutineDraft)
	mux.HandleFunc("GET /api/hold-fire", s.serveGlobalHold)
	mux.HandleFunc("POST /api/hold-fire/on", s.serveSetHold(true, true))
	mux.HandleFunc("POST /api/hold-fire/off", s.serveSetHold(false, true))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail maps a store error to its status.
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnknownFleet), errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNotYours):
		writeError(w, http.StatusForbidden, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func readRoutine(w http.ResponseWriter, r *http.Request) (Routine, bool) {
	var in Routine
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "the body is not a routine: "+err.Error())
		return in, false
	}
	return in, true
}

func (s *Service) serveList(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.List(r.PathValue("key"))
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]listed, len(list))
	for i, rt := range list {
		out[i].Routine = rt
		if runs, err := s.Store.Recent(r.PathValue("key"), rt.ID, 1); err == nil && len(runs) > 0 {
			out[i].LastRun = &lastRun{At: runs[0].At, Outcome: runs[0].Outcome}
		}
	}
	body := map[string]any{"routines": out}
	if sp, ok := s.spend(r.PathValue("key")); ok {
		body["spend"] = sp
	}
	writeJSON(w, http.StatusOK, body)
}

// Spend is a fleet's daily worker spend against its cap.
type Spend struct {
	TodayUSD float64 `json:"today_usd"`
	CapUSD   float64 `json:"cap_usd"`
	Reached  bool    `json:"reached"`
	// Unreadable is set when today's log couldn't be read; events are held then, as if the cap were reached.
	Unreadable bool `json:"unreadable,omitempty"`
}

// spend is the same sum and cap check the event path makes, in the server's local zone.
func (s *Service) spend(fleet string) (Spend, bool) {
	f, ok := s.Store.Fleets(fleet)
	if !ok {
		return Spend{}, false
	}
	spent, err := s.Store.SpendToday(fleet, s.Store.now(), time.Local)
	if err != nil {
		return Spend{CapUSD: f.Limits.Spend.DailyWorkerUSD, Reached: true, Unreadable: true}, true
	}
	return Spend{TodayUSD: spent, CapUSD: f.Limits.Spend.DailyWorkerUSD, Reached: f.Limits.DailySpendReached(spent)}, true
}

// lastRun is the newest trigger record of a routine, for its row in the list.
type lastRun struct {
	At      time.Time `json:"at"`
	Outcome Outcome   `json:"outcome"`
}

type listed struct {
	Routine
	LastRun *lastRun `json:"last_run,omitempty"`
}

func (s *Service) serveCreate(w http.ResponseWriter, r *http.Request) {
	// an unknown fleet is 404 whatever the body says
	if _, err := s.Store.List(r.PathValue("key")); err != nil {
		fail(w, err)
		return
	}
	in, ok := readRoutine(w, r)
	if !ok {
		return
	}
	got, err := s.Store.Create(r.PathValue("key"), in)
	if err != nil {
		fail(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusCreated, got)
}

func (s *Service) serveUpdate(w http.ResponseWriter, r *http.Request) {
	in, ok := readRoutine(w, r)
	if !ok {
		return
	}
	got, err := s.Store.Update(r.PathValue("key"), r.PathValue("id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, got)
}

func (s *Service) serveDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Delete(r.PathValue("key"), r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	s.changed()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) serveEnable(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		by := Engineer
		if _, bot := actor.FleetOf(r.Context()); bot {
			by = actor.Via
		}
		got, err := s.Store.SetEnabledBy(r.PathValue("key"), r.PathValue("id"), on, by)
		if err != nil {
			fail(w, err)
			return
		}
		s.changed()
		writeJSON(w, http.StatusOK, got)
	}
}

const defaultRuns = 20

func (s *Service) serveRuns(w http.ResponseWriter, r *http.Request) {
	key, id := r.PathValue("key"), r.PathValue("id")
	list, err := s.Store.List(key)
	if err != nil {
		fail(w, err)
		return
	}
	if indexOf(list, id) < 0 {
		fail(w, ErrNotFound)
		return
	}
	limit := defaultRuns
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	runs, err := s.Store.Recent(key, id, limit)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// fleetHold is a fleet's answer: on when its own switch or the global one is.
func (s *Service) fleetHold(key string) (map[string]any, error) {
	f, err := s.Store.GetHold(key)
	if err != nil {
		return nil, err
	}
	g, err := s.Store.GetHold("")
	if err != nil {
		return nil, err
	}
	return map[string]any{"on": f.On || g.On, "fleet": f, "global": g}, nil
}

func (s *Service) serveFleetHold(w http.ResponseWriter, r *http.Request) {
	out, err := s.fleetHold(r.PathValue("key"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) serveGlobalHold(w http.ResponseWriter, r *http.Request) {
	h, err := s.Store.GetHold("")
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Service) serveSetHold(on, global bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := ""
		if !global {
			key = r.PathValue("key")
		}
		by := Engineer
		if _, bot := actor.FleetOf(r.Context()); bot {
			by = actor.Via
		}
		h, err := s.Store.SetHold(key, on, by)
		if err != nil {
			fail(w, err)
			return
		}
		if global {
			writeJSON(w, http.StatusOK, h)
			return
		}
		out, err := s.fleetHold(key)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}
