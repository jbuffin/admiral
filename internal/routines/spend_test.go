package routines_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

type spendView struct {
	Routines []json.RawMessage `json:"routines"`
	Spend    *struct {
		TodayUSD float64 `json:"today_usd"`
		CapUSD   float64 `json:"cap_usd"`
		Reached  bool    `json:"reached"`
	} `json:"spend"`
}

func TestTheRoutinesListCarriesTodaysWorkerSpendAgainstTheCap(t *testing.T) {
	r := newRig(t)
	local := time.Local
	r.now = time.Date(2026, 11, 12, 12, 0, 0, 0, local)
	limits := standing.PresetLimits(standing.Work)
	limits.Spend.DailyWorkerUSD = 2.00
	fleets := r.store.Fleets
	r.store.Fleets = func(k string) (config.Fleet, bool) {
		f, ok := fleets(k)
		f.Limits = limits
		return f, ok
	}
	add := func(at time.Time, usd float64) {
		t.Helper()
		if _, err := r.store.AppendTrigger("work", routines.Trigger{Routine: "r1", Outcome: routines.Proposed, Tier: "T3", At: at,
			Costs: []routines.Cost{{Call: "lookout", USD: usd}}}); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	(&routines.Service{Store: r.store}).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	read := func() spendView {
		t.Helper()
		res, err := http.Get(srv.URL + "/api/fleets/work/routines")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var v spendView
		if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
			t.Fatal(err)
		}
		if v.Spend == nil {
			t.Fatal("the routines list has no spend")
		}
		return v
	}

	add(time.Date(2026, 11, 11, 23, 30, 0, 0, local), 3.00) // yesterday: not counted
	add(time.Date(2026, 11, 12, 1, 0, 0, 0, local), 1.50)
	add(time.Date(2026, 11, 12, 2, 0, 0, 0, local), 0.25)
	got := read().Spend
	if got.TodayUSD != 1.75 || got.CapUSD != 2.00 || got.Reached {
		t.Errorf("spend = %+v, want 1.75 of 2.00, not reached (yesterday's 3.00 doesn't count)", got)
	}

	add(time.Date(2026, 11, 12, 3, 0, 0, 0, local), 0.25)
	got = read().Spend
	if got.TodayUSD != 2.00 || !got.Reached {
		t.Errorf("spend = %+v, want 2.00 of 2.00 reached (at the cap counts)", got)
	}
}
