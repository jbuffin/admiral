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

func TestMooredSliceShipKeepsClaimingEveryIssue(t *testing.T) {
	f := mooredService(t, nil)
	if err := moor.Write(f.home, moor.Record{Name: "Slice", Session: "slice", Fleet: "work", URL: sliceA, Issues: []string{sliceA, sliceB}}); err != nil {
		t.Fatal(err)
	}
	got := f.s.mooredShips(nil)
	if len(got) != 1 || len(got[0].Issues) != 2 || got[0].Issues[1] != sliceB {
		t.Fatalf("moored ships = %+v, want one carrying both issues", got)
	}
	for _, u := range []string{sliceA, sliceB} {
		if k, _ := ClaimKey(u); f.s.Claims()[k] != "Slice" {
			t.Errorf("Claims = %v, want %s held by the moored slice ship", f.s.Claims(), u)
		}
	}
}
