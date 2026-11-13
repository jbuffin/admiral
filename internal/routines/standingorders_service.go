package routines

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/standing"
)

// ordersView is the GET shape: the stored orders with the fleet's effective Limits beside them.
func (s *Service) ordersView(key string, o StandingOrders) map[string]any {
	f, _ := s.Config.Current().Fleet(key)
	overridden := f.Overridden
	if overridden == nil {
		overridden = []string{}
	}
	return map[string]any{"checks": o.Checks, "guidance": o.Guidance, "preset": string(f.Preset),
		"limits": f.Limits.Clone(), "sentences": standing.Sentences(f.Limits), "overridden": overridden}
}

// configFail maps a config error the way a fleet save does.
func configFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, config.ErrNoFleet):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, config.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "the body is not valid JSON of the expected shape: "+err.Error())
		return false
	}
	return true
}

func (s *Service) serveOrdersGet(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	o, err := s.Store.StandingOrders(key)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.ordersView(key, o))
}

func (s *Service) serveOrdersPut(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if _, err := s.Store.StandingOrders(key); err != nil {
		fail(w, err)
		return
	}
	var req struct {
		Checks   *[]string `json:"checks"`
		Guidance *string   `json:"guidance"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	s.ordersMu.Lock()
	defer s.ordersMu.Unlock()
	o, err := s.Store.SetStandingOrders(key, StandingOrdersPatch{Checks: req.Checks, Guidance: req.Guidance})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.ordersView(key, o))
}

// serveOrdersCopy writes the target's orders file first and its Limits second, and puts the file back when the Limits are refused.
func (s *Service) serveOrdersCopy(w http.ResponseWriter, r *http.Request) {
	from := r.PathValue("key")
	src, err := s.Store.StandingOrders(from)
	if err != nil {
		fail(w, err)
		return
	}
	var req struct {
		To string `json:"to"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	switch {
	case req.To == "":
		writeError(w, http.StatusBadRequest, "to: name the fleet to copy to")
		return
	case req.To == from:
		writeError(w, http.StatusBadRequest, "to: a fleet's standing orders can't be copied to itself")
		return
	}
	if _, err := s.Store.fleet(req.To); err != nil {
		fail(w, err)
		return
	}
	s.ordersMu.Lock()
	defer s.ordersMu.Unlock()
	src, err = s.Store.StandingOrders(from)
	if err != nil {
		fail(w, err)
		return
	}
	if _, _, err := s.Config.PreviewCopyLimits(from, req.To); err != nil {
		configFail(w, err)
		return
	}
	restore, err := s.Store.snapshotOrders(req.To)
	if err != nil {
		fail(w, err)
		return
	}
	if _, err := s.Store.SetStandingOrders(req.To, StandingOrdersPatch{Checks: &src.Checks, Guidance: &src.Guidance}); err != nil {
		fail(w, err)
		return
	}
	before, after, err := s.Config.CopyLimits(from, req.To)
	if err != nil {
		if rerr := restore(); rerr != nil {
			err = fmt.Errorf("%w (putting the target's standing orders back failed: %v)", err, rerr)
		}
		configFail(w, err)
		return
	}
	out := s.ordersView(req.To, src)
	loosens := standing.Loosens(before, after)
	if loosens == nil {
		loosens = []string{}
	}
	out["loosens"] = loosens
	writeJSON(w, http.StatusOK, out)
}
