package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/server"
)

// Tab-indented with an unknown key, so any rewrite of what the route should
// not touch shows as different bytes.
const fleetsFixture = `{
	"version": 1,
	"x_note": "kept",
	"fleets": {
		"work": {
			"name": "Work",
			"color": "#112233",
			"search_roots": ["/srv/work"],
			"owners": ["acme"],
			"clone_root": "/srv/work/clones",
			"env": {"FOO": "decoy-secret-0a1b2c"},
			"claude": {"executable": "/bin/echo", "args": ["--flag"]}
		},
		"home": {"name": "Home", "search_roots": ["/srv/home"], "claude": {}}
	}
}
`

type fleetFixture struct {
	t    *testing.T
	home string
	h    http.Handler
}

func newFleetFixture(t *testing.T, cfg string) *fleetFixture {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return &fleetFixture{t: t, home: home, h: (&server.Server{Config: m}).Handler()}
}

func (f *fleetFixture) do(method, target string, body any) (int, string) {
	f.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		r = bytes.NewReader(raw)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequest(method, target, r))
	res := rec.Result()
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func (f *fleetFixture) file() string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.home, config.FileName))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

type fleetsView struct {
	Fleets []struct {
		Key         string   `json:"key"`
		Name        string   `json:"name"`
		Color       string   `json:"color"`
		SearchRoots []string `json:"search_roots"`
		Owners      []string `json:"owners"`
		CloneRoot   *string  `json:"clone_root"`
		Docks       []struct {
			Executable string `json:"executable"`
		} `json:"docks"`
	} `json:"fleets"`
}

func (f *fleetFixture) view() fleetsView {
	f.t.Helper()
	code, body := f.do(http.MethodGet, "/api/config", nil)
	if code != http.StatusOK {
		f.t.Fatalf("GET /api/config = %d: %s", code, body)
	}
	var v fleetsView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		f.t.Fatal(err)
	}
	return v
}

func (f *fleetFixture) fleet(key string) (out struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Color       string   `json:"color"`
	SearchRoots []string `json:"search_roots"`
	Owners      []string `json:"owners"`
	CloneRoot   *string  `json:"clone_root"`
	Docks       []struct {
		Executable string `json:"executable"`
	} `json:"docks"`
}) {
	f.t.Helper()
	for _, fl := range f.view().Fleets {
		if fl.Key == key {
			return fl
		}
	}
	f.t.Fatalf("GET /api/config has no fleet %q", key)
	return
}

// Route under test: PUT /api/fleets/{key}, registered in Server.Handler beside
// GET /api/config (so it also answers under /api/v1). The body is
// {"search_roots": [...], "owners": [...], "clone_root": "...", "color": "#RRGGBB"};
// all four are sent. It answers 200 once the file is written and the Manager has
// reloaded. Other keys in the body (name, docks, env) are ignored.
func TestFleetSave_EachFieldShowsInTheConfigViewRightAway(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{
		"search_roots": []string{"~/code", "/srv/other"},
		"owners":       []string{"acme", "octo"},
		"clone_root":   "/srv/clones",
		"color":        "#AABBCC",
	})
	if code != http.StatusOK {
		t.Fatalf("PUT /api/fleets/work = %d %s, want 200", code, body)
	}
	w := f.fleet("work") // read at once, with no wait: the save reloads before it answers
	if w.Color != "#AABBCC" {
		t.Errorf("color = %q, want #AABBCC", w.Color)
	}
	if got := strings.Join(w.Owners, ","); got != "acme,octo" {
		t.Errorf("owners = %v, want [acme octo]", w.Owners)
	}
	if w.CloneRoot == nil || *w.CloneRoot != "/srv/clones" {
		t.Errorf("clone_root = %v, want /srv/clones", w.CloneRoot)
	}
	if len(w.SearchRoots) != 2 || w.SearchRoots[1] != "/srv/other" || !strings.HasSuffix(w.SearchRoots[0], "/code") {
		t.Errorf("search_roots = %v, want [<home>/code /srv/other]", w.SearchRoots)
	}
	if !strings.Contains(f.file(), `"~/code"`) {
		t.Errorf("config.json does not hold the search root as received (~/code):\n%s", f.file())
	}
}

func TestFleetSave_AnswersUnderV1(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	code, body := f.do(http.MethodPut, "/api/v1/fleets/home", map[string]any{
		"search_roots": []string{"/srv/home"}, "owners": []string{"me"}, "clone_root": "", "color": "#010203",
	})
	if code != http.StatusOK {
		t.Fatalf("PUT /api/v1/fleets/home = %d %s, want 200", code, body)
	}
	if got := f.fleet("home").Color; got != "#010203" {
		t.Errorf("color = %q, want #010203", got)
	}
}

func TestFleetSave_LeavesDocksEnvNameAndTheRestOfTheFileAlone(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{
		"search_roots": []string{"/srv/new"}, "owners": []string{"acme"}, "clone_root": "/srv/new/clones", "color": "#112233",
		"name": "Renamed", "key": "other", "env": map[string]string{"FOO": "x"},
		"claude": map[string]any{"executable": "/bin/false"},
	})
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s, want 200", code, body)
	}
	want := strings.ReplaceAll(fleetsFixture, `"/srv/work"`, `"/srv/new"`)
	want = strings.ReplaceAll(want, `"/srv/work/clones"`, `"/srv/new/clones"`)
	var got, exp any
	if err := json.Unmarshal([]byte(f.file()), &got); err != nil {
		t.Fatalf("config.json is not JSON after the save: %v", err)
	}
	_ = json.Unmarshal([]byte(want), &exp)
	if g, e := mustJSON(got), mustJSON(exp); g != e {
		t.Errorf("config.json after the save:\n%s\nwant only work's search_roots and clone_root changed:\n%s", g, e)
	}
	if !strings.Contains(f.file(), "\t\t\"home\": {") || !strings.Contains(f.file(), `"x_note": "kept"`) {
		t.Errorf("other fleets and keys must keep their bytes:\n%s", f.file())
	}
	w := f.fleet("work")
	if w.Name != "Work" || len(w.Docks) != 1 || w.Docks[0].Executable != "/bin/echo" {
		t.Errorf("fleet after the save = %+v, want name Work and the claude dock /bin/echo", w)
	}
	if code, _ := f.do(http.MethodGet, "/api/config", nil); code != http.StatusOK || f.fleet("home").Name != "Home" {
		t.Error("the other fleet changed")
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestFleetSave_RefusesWithoutWritingTheFile(t *testing.T) {
	valid := map[string]any{"search_roots": []string{"/srv/ok"}, "owners": []string{"acme"}, "clone_root": "", "color": "#445566"}
	with := func(k string, v any) map[string]any {
		out := map[string]any{}
		for a, b := range valid {
			out[a] = b
		}
		out[k] = v
		return out
	}
	for _, c := range []struct {
		name, key string
		body      any
		status    int
		message   bool
	}{
		{"an unknown fleet", "nosuch", valid, http.StatusNotFound, true},
		{"malformed JSON", "work", `{"search_roots": [`, http.StatusBadRequest, false},
		{"a field of the wrong type", "work", with("owners", "acme"), http.StatusBadRequest, false},
		{"a bad color", "work", with("color", "red"), http.StatusBadRequest, true},
		{"a relative search root", "work", with("search_roots", []string{"code"}), http.StatusBadRequest, true},
		{"an empty owner", "work", with("owners", []string{"acme", " "}), http.StatusBadRequest, true},
	} {
		f := newFleetFixture(t, fleetsFixture)
		code, body := f.do(http.MethodPut, "/api/fleets/"+c.key, c.body)
		if code != c.status {
			t.Errorf("PUT with %s = %d %s, want %d", c.name, code, body, c.status)
			continue
		}
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(body), &e) != nil || (c.message && (e.Error == "" || e.Error == "not found")) {
			t.Errorf("PUT with %s answered %q, want a JSON error with the message", c.name, body)
		}
		if f.file() != fleetsFixture {
			t.Errorf("PUT with %s changed config.json", c.name)
		}
	}
}

func TestFleetSave_AnInvalidConfigFileAnswers409(t *testing.T) {
	f := newFleetFixture(t, `{"version":1,"fleets":{"work":{"name":"Work","color":"nope","claude":{}}}}`)
	before := f.file()
	code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{
		"search_roots": []string{"/srv/ok"}, "owners": []string{}, "clone_root": "", "color": "#445566",
	})
	if code != http.StatusConflict {
		t.Errorf("PUT on an invalid config file = %d %s, want 409", code, body)
	}
	if f.file() != before {
		t.Error("PUT on an invalid config file changed it")
	}
}

func TestConfigView_FleetsCarryOwnersAndTheStoredCloneRoot(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	w := f.fleet("work")
	if got := strings.Join(w.Owners, ","); got != "acme" {
		t.Errorf("work owners = %v, want [acme]", w.Owners)
	}
	if w.CloneRoot == nil || *w.CloneRoot != "/srv/work/clones" {
		t.Errorf("work clone_root = %v, want /srv/work/clones", w.CloneRoot)
	}
	h := f.fleet("home")
	if h.CloneRoot == nil || *h.CloneRoot != "" {
		t.Errorf("home clone_root = %v, want \"\" (stored value; no default applied)", h.CloneRoot)
	}
	code, body := f.do(http.MethodGet, "/api/config", nil)
	if code != http.StatusOK || !strings.Contains(body, `"owners":[]`) {
		t.Errorf("a fleet with no owners must show owners as [] not null: %s", body)
	}
}
