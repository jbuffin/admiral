package tailscale

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
