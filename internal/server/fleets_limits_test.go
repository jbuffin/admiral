package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type limitsFleetView struct {
	Key    string `json:"key"`
	Preset string `json:"preset"`
	Limits struct {
		Ceilings struct {
			T1 struct {
				Level     string `json:"level"`
				AsksFirst bool   `json:"asks_first"`
			} `json:"t1"`
			T2 struct {
				Level      string `json:"level"`
				Restricted string `json:"restricted"`
			} `json:"t2"`
		} `json:"ceilings"`
		WallClockMinutes int `json:"wall_clock_minutes"`
	} `json:"limits"`
	Sentences []struct {
		Field    string `json:"field"`
		Sentence string `json:"sentence"`
	} `json:"sentences"`
}

func configFleet(t *testing.T, f *fleetFixture, key string) limitsFleetView {
	t.Helper()
	_, view := f.do(http.MethodGet, "/api/config", nil)
	var v struct {
		Fleets []limitsFleetView `json:"fleets"`
	}
	if err := json.Unmarshal([]byte(view), &v); err != nil {
		t.Fatal(err)
	}
	for _, fl := range v.Fleets {
		if fl.Key == key {
			return fl
		}
	}
	t.Fatalf("no fleet %q in %s", key, view)
	return limitsFleetView{}
}

func TestFleetSave_LimitsAndPresetShowInTheConfigViewAndInTheResponse(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{
		"preset": "home",
		"limits": map[string]any{
			"wall_clock_minutes": 30,
			"ceilings":           map[string]any{"t1": map[string]any{"level": "A5", "asks_first": true}},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("PUT limits = %d %s, want 200", code, body)
	}
	var put struct {
		Fleet limitsFleetView `json:"fleet"`
	}
	if err := json.Unmarshal([]byte(body), &put); err == nil && put.Fleet.Key != "" {
		if put.Fleet.Preset != "home" || put.Fleet.Limits.WallClockMinutes != 30 || len(put.Fleet.Sentences) == 0 {
			t.Errorf("PUT response fleet = %+v, want preset home, wall clock 30 and sentences", put.Fleet)
		}
	} else if !strings.Contains(body, `"sentences"`) || !strings.Contains(body, `"wall_clock_minutes":30`) {
		t.Errorf("PUT response %s, want limits and sentences", body)
	}

	w := configFleet(t, f, "work")
	if w.Preset != "home" || w.Limits.WallClockMinutes != 30 || w.Limits.Ceilings.T1.Level != "A5" || !w.Limits.Ceilings.T1.AsksFirst {
		t.Errorf("work in GET /api/config = %+v, want preset home, wall clock 30, T1 A5 asking first", w)
	}
	if len(w.Sentences) == 0 || w.Sentences[0].Field == "" || w.Sentences[0].Sentence == "" {
		t.Errorf("work sentences = %+v, want field paths with sentences", w.Sentences)
	}
	h := configFleet(t, f, "home")
	if h.Preset != "work" || h.Limits.WallClockMinutes != 60 || h.Limits.Ceilings.T2.Level != "A1" {
		t.Errorf("home in GET /api/config = %+v, want the Work preset: wall clock 60, T2 A1", h)
	}
}

func TestFleetSave_ALimitLeftOutStaysAsStored(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	if code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"limits": map[string]any{"wall_clock_minutes": 30}}); code != http.StatusOK {
		t.Fatalf("first PUT = %d %s", code, body)
	}
	if code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"wake_reviewers": []string{"alice"}}); code != http.StatusOK {
		t.Fatalf("second PUT = %d %s", code, body)
	}
	if got := configFleet(t, f, "work").Limits.WallClockMinutes; got != 30 {
		t.Errorf("wall clock after a save that left limits out = %d, want 30", got)
	}
	if code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"limits": map[string]any{"quota_hold_percent": 75}}); code != http.StatusOK {
		t.Fatalf("third PUT = %d %s", code, body)
	}
	if got := configFleet(t, f, "work").Limits.WallClockMinutes; got != 30 {
		t.Errorf("wall clock after saving another Limit = %d, want 30", got)
	}
}

func TestFleetSave_ABadLimitIsRefusedNamingItsPathAndWritesNothing(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	for _, c := range []struct {
		name   string
		body   map[string]any
		wantIn string
	}{
		{"T3 above A1", map[string]any{"limits": map[string]any{"ceilings": map[string]any{"t3": map[string]any{"level": "A3", "restricted": "investigate"}}}}, "limits.ceilings.t3.level"},
		{"A2", map[string]any{"limits": map[string]any{"ceilings": map[string]any{"t2": map[string]any{"level": "A2"}}}}, "limits.ceilings.t2.level"},
		{"a wall clock of 0", map[string]any{"limits": map[string]any{"wall_clock_minutes": 0}}, "limits.wall_clock_minutes"},
		{"an unknown Limit", map[string]any{"limits": map[string]any{"bogus": 1}}, "limits.bogus"},
		{"an unknown preset", map[string]any{"preset": "office"}, "preset"},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := f.file()
			code, body := f.do(http.MethodPut, "/api/fleets/work", c.body)
			if code != http.StatusBadRequest || !strings.Contains(body, c.wantIn) || !strings.Contains(body, `"error"`) {
				t.Errorf("PUT = %d %s, want 400 {\"error\"} naming %s", code, body, c.wantIn)
			}
			if f.file() != before {
				t.Error("a refused save changed config.json")
			}
		})
	}
}
