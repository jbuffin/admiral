package tailscale

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

func get(t *testing.T, mode bool) (view struct {
	Tailscale     bool     `json:"tailscale"`
	AllowedLogins []string `json:"allowedLogins"`
	URL           string   `json:"url"`
	Checks        []Check  `json:"checks"`
}) {
	t.Helper()
	mux := http.NewServeMux()
	rt := &Route{Port: "7420", Config: func() *config.Config {
		return &config.Config{Remote: config.Remote{Tailscale: mode, AllowedLogins: []string{"alice@example.com"}}}
	}}
	rt.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/remote", nil))
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &view) != nil {
		t.Fatalf("GET /api/remote: %d %s", rec.Code, rec.Body)
	}
	return view
}

func TestRemoteRouteModeOffRunsNothing(t *testing.T) {
	dir := fakeOnPath(t)
	v := get(t, false)
	if v.Tailscale || v.URL != "" || len(v.Checks) != 0 || !reflect.DeepEqual(v.AllowedLogins, []string{"alice@example.com"}) {
		t.Errorf("view %+v, want the logins only", v)
	}
	if _, err := os.Stat(filepath.Join(dir, "calls")); err == nil {
		t.Error("tailscale ran with the mode off")
	}
}

// The route reads the CLI on every call, so a Funnel turned on after the first answer shows in the next.
func TestRemoteRouteReadsTheCLILive(t *testing.T) {
	dir := fakeOnPath(t)
	put(t, dir, "status.json", okStatus)
	put(t, dir, "serve.json", `{`+web("443", "http://127.0.0.1:7420")+`}`)
	v := get(t, true)
	if !v.Tailscale || v.URL != "https://"+host+"/" || len(v.Checks) != 4 {
		t.Fatalf("view %+v", v)
	}
	for _, c := range v.Checks {
		if !c.OK || c.Detail == "" {
			t.Errorf("check %+v, want ok with a detail", c)
		}
	}
	put(t, dir, "serve.json", `{`+web("443", "http://127.0.0.1:7420")+`,"AllowFunnel":{"`+host+`:443":true}}`)
	v = get(t, true)
	if f := v.Checks[3]; f.ID != CheckFunnel || f.OK {
		t.Errorf("funnel check after the flip: %+v", f)
	}
}

func TestRemoteRouteWhenTailscaledIsDown(t *testing.T) {
	dir := fakeOnPath(t)
	put(t, dir, "status.exit", "1")
	put(t, dir, "serve.exit", "1")
	v := get(t, true)
	if v.URL != "" || len(v.Checks) != 4 {
		t.Fatalf("view %+v", v)
	}
	for _, c := range v.Checks {
		if c.OK {
			t.Errorf("check %+v ok with tailscaled down", c)
		}
	}
}

// settingsRoute keeps the remote section in memory; Set is what the config manager's SetRemote is in the server.
func settingsRoute(t *testing.T, local bool) (*http.ServeMux, *config.Remote) {
	t.Helper()
	cur := &config.Remote{Tailscale: true, AllowedLogins: []string{"alice@example.com", "bob@example.com"}}
	mux := http.NewServeMux()
	rt := &Route{Port: "7420", Local: func(*http.Request) bool { return local },
		Config: func() *config.Config { return &config.Config{Remote: *cur} },
		Set:    func(r config.Remote) error { *cur = r; return nil }}
	rt.Routes(mux)
	return mux, cur
}

func post(mux *http.ServeMux, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

func TestLoginsEditKeepsTheOthersAndRefusesBadInput(t *testing.T) {
	fakeOnPath(t)
	mux, cur := settingsRoute(t, true)
	if rec := post(mux, "/api/remote/logins", `{"remove":"bob@example.com"}`); rec.Code != 200 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if !reflect.DeepEqual(cur.AllowedLogins, []string{"alice@example.com"}) || !cur.Tailscale {
		t.Errorf("after removing bob: %+v, want alice only and the mode still on", *cur)
	}
	for _, body := range []string{`{"add":" "}`, `{"add":"alice@example.com"}`, `{}`, `{"add":"a","remove":"b"}`, `nope`} {
		if rec := post(mux, "/api/remote/logins", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, rec.Code)
		}
	}
	if len(cur.AllowedLogins) != 1 {
		t.Errorf("a refused edit changed the logins: %v", cur.AllowedLogins)
	}
}

func TestSetupRefusesARequestNotFromThisMac(t *testing.T) {
	dir := fakeOnPath(t)
	mux, cur := settingsRoute(t, false)
	cur.Tailscale = false
	if rec := post(mux, "/api/remote/setup", `{"confirm":true}`); rec.Code != http.StatusForbidden {
		t.Errorf("Set up from elsewhere: %d, want 403", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "calls")); err == nil || cur.Tailscale {
		t.Error("Set up ran or wrote for a request from elsewhere")
	}
}
