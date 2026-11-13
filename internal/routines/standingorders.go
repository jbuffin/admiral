package routines

import (
	"errors"
	"net/http"
)

// StandingOrders are a fleet's Checks and Guidance, kept beside routines.json.
type StandingOrders struct {
	Checks   []string `json:"checks"`
	Guidance string   `json:"guidance"`
}

// StandingOrdersPatch leaves a nil field as stored.
type StandingOrdersPatch struct {
	Checks   *[]string
	Guidance *string
}

var errNotImplemented = errors.New("not implemented")

func (s *Store) StandingOrders(fleet string) (StandingOrders, error) {
	return StandingOrders{}, errNotImplemented
}

func (s *Store) SetStandingOrders(fleet string, p StandingOrdersPatch) (StandingOrders, error) {
	return StandingOrders{}, errNotImplemented
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}
