package tailscale

import (
	"encoding/json"
	"errors"
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

func TestSetupNeedsConfirmation(t *testing.T) {
	dir := fakeOnPath(t)
	mux, cur := settingsRoute(t, true)
	cur.Tailscale = false
	for _, body := range []string{`{}`, `{"confirm":false}`} {
		if rec := post(mux, "/api/remote/setup", body); rec.Code != http.StatusBadRequest {
			t.Errorf("Set up with %s: %d, want 400", body, rec.Code)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "calls")); err == nil {
		t.Error("Set up ran without confirmation")
	}
}

func TestLoginsWriteFailureDoesNotLeakThePath(t *testing.T) {
	fakeOnPath(t)
	mux := http.NewServeMux()
	(&Route{Port: "7420", Config: func() *config.Config { return &config.Config{} },
		Set: func(config.Remote) error { return errors.New("write /Users/x/.admiral/config.json: permission denied") }}).Routes(mux)
	rec := post(mux, "/api/remote/logins", `{"add":"a@example.com"}`)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "/Users") {
		t.Errorf("%d %s, want 500 without the OS error", rec.Code, rec.Body)
	}
}

func TestTurnOffNeedsConfirmationAndThisMac(t *testing.T) {
	dir := fakeOnPath(t)
	mux, cur := settingsRoute(t, true)
	for _, body := range []string{`{}`, `{"confirm":false}`, `nope`} {
		if rec := post(mux, "/api/remote/off", body); rec.Code != http.StatusBadRequest {
			t.Errorf("Turn off with %s: %d, want 400", body, rec.Code)
		}
	}
	if !cur.Tailscale {
		t.Error("an unconfirmed Turn off turned the mode off")
	}

	remoteMux, remoteCur := settingsRoute(t, false)
	if rec := post(remoteMux, "/api/remote/off", `{"confirm":true}`); rec.Code != http.StatusForbidden {
		t.Errorf("Turn off from another device: %d, want 403", rec.Code)
	}
	if !remoteCur.Tailscale {
		t.Error("a refused Turn off still turned the mode off")
	}
	if _, err := os.Stat(filepath.Join(dir, "calls")); err == nil {
		t.Error("Turn off ran tailscale")
	}
}

// Turn off writes the mode and nothing else: the logins stay and serve is left to the person.
func TestTurnOffWritesOnlyTheMode(t *testing.T) {
	dir := fakeOnPath(t)
	mux, cur := settingsRoute(t, true)
	rec := post(mux, "/api/remote/off", `{"confirm":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("Turn off: %d %s", rec.Code, rec.Body)
	}
	if cur.Tailscale || !reflect.DeepEqual(cur.AllowedLogins, []string{"alice@example.com", "bob@example.com"}) {
		t.Errorf("after Turn off: %+v, want the mode off and both logins kept", *cur)
	}
	var v struct {
		Tailscale bool `json:"tailscale"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.Tailscale {
		t.Errorf("answer %s, want the view with the mode off", rec.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "calls")); strings.Contains(string(b), "serve --") {
		t.Errorf("Turn off touched serve: %s", b)
	}
}

func TestTurnOffWriteFailureDoesNotLeakThePath(t *testing.T) {
	fakeOnPath(t)
	mux := http.NewServeMux()
	(&Route{Port: "7420", Local: func(*http.Request) bool { return true },
		Config: func() *config.Config { return &config.Config{Remote: config.Remote{Tailscale: true}} },
		Set:    func(config.Remote) error { return errors.New("write /Users/x/.admiral/config.json: permission denied") }}).Routes(mux)
	rec := post(mux, "/api/remote/off", `{"confirm":true}`)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "/Users") {
		t.Errorf("%d %s, want 500 without the OS error", rec.Code, rec.Body)
	}
}

// The view names the command that stops serve on the HTTPS port Admiral is served on: 8443 for a hand-set serve.
func TestViewNamesTheCommandThatStopsServe(t *testing.T) {
	dir := fakeOnPath(t)
	const host = "fake-mac.tailnet-fake.ts.net"
	put(t, dir, "status.json", `{"BackendState":"Running","Self":{"DNSName":"`+host+`.","UserID":1},"CurrentTailnet":{"MagicDNSEnabled":true},"CertDomains":["`+host+`"],"User":{"1":{"LoginName":"alice@example.com"}}}`)
	put(t, dir, "serve.json", `{"TCP":{"8443":{"HTTPS":true}},"Web":{"`+host+`:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7420"}}}}}`)
	mux, _ := settingsRoute(t, true)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/remote", nil))
	var v struct {
		ServeOff string `json:"serveOff"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &v) != nil || v.ServeOff != "tailscale serve --https=8443 off" {
		t.Errorf("GET /api/remote: %d %s, want serveOff %q", rec.Code, rec.Body, "tailscale serve --https=8443 off")
	}
}
