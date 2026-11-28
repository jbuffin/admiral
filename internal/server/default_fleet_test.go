package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

func defaultOf(t *testing.T, body string) (string, bool) {
	t.Helper()
	var v struct {
		Default  string `json:"default_fleet"`
		Fallback bool   `json:"default_fleet_fallback"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return v.Default, v.Fallback
}

func TestPutDefaultFleetSetsItAndKeepsOtherKeys(t *testing.T) {
	for _, prefix := range []string{"/api", "/api/v1"} {
		f := newFleetFixture(t, fleetsFixture)
		code, body := f.do("PUT", prefix+"/default-fleet", `{"fleet":"home"}`)
		if code != 200 {
			t.Fatalf("%s: code %d, body %s", prefix, code, body)
		}
		if def, fb := defaultOf(t, body); def != "home" || fb {
			t.Errorf("%s answered view default %q fallback %v; want home, false", prefix, def, fb)
		}
		if file := f.file(); !strings.Contains(file, `"default_fleet": "home"`) || !strings.Contains(file, `"x_note": "kept"`) {
			t.Errorf("%s: config.json lost a key or missed the default:\n%s", prefix, file)
		}
		_, got := f.do("GET", prefix+"/config", nil)
		if def, fb := defaultOf(t, got); def != "home" || fb {
			t.Errorf("%s GET config default %q fallback %v; want home, false", prefix, def, fb)
		}
	}
}

func TestPutDefaultFleetRefusals(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       int
	}{
		{"unknown fleet", `{"fleet":"nope"}`, 422},
		{"bad JSON", `{"fleet":`, 400},
		{"missing fleet", `{}`, 400},
		{"non-string fleet", `{"fleet":3}`, 400},
		{"empty key", `{"fleet":""}`, 400},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFleetFixture(t, fleetsFixture)
			before := f.file()
			code, body := f.do("PUT", "/api/default-fleet", c.body)
			if code != c.want {
				t.Errorf("code %d, want %d; body %s", code, c.want, body)
			}
			if f.file() != before {
				t.Error("config.json changed")
			}
		})
	}
}

func TestPutDefaultFleetOnABrokenConfigIsAConflict(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	if err := os.WriteFile(filepath.Join(f.home, config.FileName), []byte(`{"fleets": [`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, body := f.do("PUT", "/api/default-fleet", `{"fleet":"home"}`); code != 409 {
		t.Errorf("code %d, want 409; body %s", code, body)
	}
}

func TestPutDefaultFleetOnAnUnreadableConfigIs500(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	path := filepath.Join(f.home, config.FileName)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if code, body := f.do("PUT", "/api/default-fleet", `{"fleet":"home"}`); code != 500 {
		t.Errorf("code %d, want 500; body %s", code, body)
	}
}
