package ships

import (
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/state"
)

// A moored ship keeps its order hidden, so the order cannot be launched twice.
func TestMooredShipHidesItsOrder(t *testing.T) {
	home := t.TempDir()
	if err := moor.Write(home, moor.Record{Name: "Saltpaw II", Session: "saltpaw-ii", Fleet: "work", Source: "gh", OrderID: "acme/widgets#12"}); err != nil {
		t.Fatal(err)
	}
	s := &Service{State: &state.Store{}, Launcher: &launcher.Launcher{Home: home, Config: func() *config.Config { return &config.Config{} }}}
	got := s.mooredShips(nil)
	if len(got) != 1 || got[0].Status != StatusMoored || !HiddenOrders(got)[OrderKey{"gh", "acme/widgets#12"}] {
		t.Errorf("moored ships = %+v, want one that hides order gh/acme/widgets#12", got)
	}
}
