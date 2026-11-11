package sources

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
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/state"
)

// serviceFixture serves the sources API over a config file with no plugin
// running: what the routes do with config.json alone.
type serviceFixture struct {
	t     *testing.T
	home  string
	cfg   *config.Manager
	srv   *httptest.Server
	State *state.Store
}

func newServiceFixture(t *testing.T, cfg string) *serviceFixture {
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
	if e := m.View().Error; e != nil {
		t.Fatalf("the fixture's config does not load: %s", e.Message)
	}
	st, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Home: home, Config: m, Sup: &Supervisor{Home: home, Config: m}, State: st, Ships: func() []ships.Ship { return nil }}
	mux := http.NewServeMux()
	s.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &serviceFixture{t: t, home: home, cfg: m, srv: srv, State: st}
}

func (f *serviceFixture) do(method, path string, body any) (int, string) {
	f.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, f.srv.URL+"/api/sources"+path, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (f *serviceFixture) file() string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.home, config.FileName))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// stored is the source's settings in config.json, compacted.
func (f *serviceFixture) stored(key string) string {
	f.t.Helper()
	var cfg struct {
		Sources map[string]struct {
			Settings json.RawMessage `json:"settings"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(f.file()), &cfg); err != nil {
		f.t.Fatal(err)
	}
	var b bytes.Buffer
	_ = json.Compact(&b, cfg.Sources[key].Settings)
	return b.String()
}

const settingsConfig = `{"version":1,"fleets":{"work":{"name":"Work","claude":{}}},
  "sources":{"pw":{"plugin":"notinstalled","fleet":"work","name":"Passwords",
    "settings":{"url":"https://example.com","token":"s3cret","accounts":["s3cretA","s3cretB"],"auth":{"pin":1234}}}}}`

// With no schema known (the plugin isn't running), every stored value is
// hidden; saving the marker back, at any depth and inside a list, keeps it.
func TestSettingsHideStoredSecretsAndKeepThemOnSave(t *testing.T) {
	f := newServiceFixture(t, settingsConfig)
	code, body := f.do(http.MethodGet, "/pw/settings", nil)
	if code != http.StatusOK || strings.Contains(body, "s3cret") || strings.Contains(body, "1234") || !strings.Contains(body, `"name":"Passwords"`) {
		t.Fatalf("GET settings = %d %s, want 200 with the name and every stored value masked", code, body)
	}
	var view struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}

	// The form sends back what it got, with one list entry and the url changed.
	back := view.Settings
	back["url"] = "https://example.com/new"
	back["accounts"] = []any{unchangedMarker, "n3w"}
	if code, body := f.do(http.MethodPut, "/pw", map[string]any{"settings": back}); code != http.StatusOK {
		t.Fatalf("PUT with the markers = %d %s, want 200", code, body)
	}
	want := `{"accounts":["s3cretA","n3w"],"auth":{"pin":1234},"token":"s3cret","url":"https://example.com/new"}`
	if got := f.stored("pw"); got != want {
		t.Errorf("stored settings after the save = %s, want %s", got, want)
	}

	before := f.file()
	for name, settings := range map[string]map[string]any{
		"text added to the marker":          {"token": unchangedMarker + "n3w"},
		"a marker past the end of the list": {"accounts": []any{"a", "b", unchangedMarker}},
	} {
		if code, _ := f.do(http.MethodPut, "/pw", map[string]any{"settings": settings}); code != http.StatusBadRequest {
			t.Errorf("PUT with %s = %d, want 400", name, code)
		}
	}
	if f.file() != before {
		t.Error("a refused save changed config.json")
	}
	if code, _ := f.do(http.MethodGet, "/nosuch/settings", nil); code != http.StatusNotFound {
		t.Errorf("GET settings of an unknown source = %d, want 404", code)
	}
}

func TestMaskSecretsShowsOnlyWhatTheSchemaDeclaresPlain(t *testing.T) {
	obj := func(props map[string]any, extra ...any) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		for i := 0; i+1 < len(extra); i += 2 {
			s[extra[i].(string)] = extra[i+1]
		}
		return s
	}
	str := map[string]any{"type": "string"}
	pw := map[string]any{"type": "string", "format": "password"}
	for _, c := range []struct {
		name     string
		schema   map[string]any
		settings string
		shown    []string
		hidden   []string
	}{
		{"a declared string, a password and an undeclared key", obj(map[string]any{"url": str, "token": pw}),
			`{"url":"pubvisible","token":"s3cretA","other":"s3cretB"}`, []string{"pubvisible"}, []string{"s3cretA", "s3cretB"}},
		{"a password nested in an object", obj(map[string]any{"auth": obj(map[string]any{"user": str, "token": pw})}),
			`{"auth":{"user":"me","token":"s3cretC"}}`, []string{`"me"`}, []string{"s3cretC"}},
		{"passwords in a list", obj(map[string]any{"accounts": map[string]any{"type": "array", "items": pw}}),
			`{"accounts":["s3cretD"]}`, nil, []string{"s3cretD"}},
		{"an enum with no type", obj(map[string]any{"mode": map[string]any{"enum": []any{"fast", "slow"}}}),
			`{"mode":"slow"}`, []string{`"slow"`}, nil},
		{"writeOnly", obj(map[string]any{"token": map[string]any{"type": "string", "writeOnly": true}}),
			`{"token":"s3cretE"}`, nil, []string{"s3cretE"}},
		{"a number not declared a number", obj(map[string]any{"pin": str}),
			`{"pin":987654}`, nil, []string{"987654"}},
		{"a keyword the walk can't follow", obj(map[string]any{"token": str}, "dependentSchemas", map[string]any{"a": map[string]any{}}),
			`{"token":"s3cretF"}`, nil, []string{"s3cretF"}},
		{"prefixItems", obj(map[string]any{"accounts": map[string]any{"type": "array", "prefixItems": []any{str}}}),
			`{"accounts":["s3cretG"]}`, nil, []string{"s3cretG"}},
		{"properties that aren't an object", map[string]any{"properties": "x"},
			`{"token":"s3cretH"}`, nil, []string{"s3cretH"}},
		{"no schema", nil, `{"token":"s3cretI","on":true,"empty":""}`, []string{`"on":true`, `"empty":""`}, []string{"s3cretI"}},
	} {
		out, _ := json.Marshal(maskSecrets(decodeValue(json.RawMessage(c.settings)), c.schema))
		for _, s := range c.shown {
			if !strings.Contains(string(out), s) {
				t.Errorf("%s: %s, want %s shown", c.name, out, s)
			}
		}
		for _, s := range c.hidden {
			if strings.Contains(string(out), s) {
				t.Errorf("%s: %s, want %s hidden", c.name, out, s)
			}
		}
	}
}
