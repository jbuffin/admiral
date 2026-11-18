package ships

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// A ship carries the round of its voyage, so a feedback round or a wake is told from the first launch.

func TestALiveShipCarriesTheRoundItsSessionWasLaunchedFor(t *testing.T) {
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		round string
		want  int
	}{{"", 0}, {"1", 1}, {"4", 4}} {
		got := FromSession(sessionhost.Session{Name: "saltpaw", Options: map[string]string{"round": c.round}}, now)
		if got.Round != c.want {
			t.Errorf("round option %q: Round = %d, want %d", c.round, got.Round, c.want)
		}
	}
}

func TestAMooredShipCarriesTheRoundOfItsRecord(t *testing.T) {
	f := mooredService(t, nil)
	if err := moor.Write(f.home, moor.Record{Name: "Saltpaw", Session: "saltpaw", Fleet: "work", Launched: "2026-11-12T06:00:00Z", Round: 3}); err != nil {
		t.Fatal(err)
	}
	if got := f.s.mooredShips(nil); len(got) != 1 || got[0].Round != 3 {
		t.Errorf("moored ships = %+v, want one in round 3", got)
	}
}
