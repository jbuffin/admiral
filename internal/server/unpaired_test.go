package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/jbuffin/admiral/internal/auth"
	"github.com/jbuffin/admiral/internal/server"
)

type guarded struct{}

func (guarded) Routes(mux *http.ServeMux) {
	ok := func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("data")) }
	mux.HandleFunc("GET /api/ships/{session}/conversation", ok)
	mux.HandleFunc("GET /ws/bridge/{rest...}", ok)
}

func (guarded) Connected(*server.Client) {}

// An encoded slash stays inside one mux segment, so the auth exemption must not read the decoded path.
func TestAnUnpairedDeviceReachesOnlyTheStaticShell(t *testing.T) {
	a, err := auth.New(t.TempDir(), "7420")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{
		"index.html":     {Data: []byte("old")},
		"app/index.html": {Data: []byte("shell")},
		"sw.js":          {Data: []byte("sw")},
		"assets/x.js":    {Data: []byte("js")},
	}
	h := a.Middleware((&server.Server{UI: ui, Modules: []server.Module{guarded{}}}).Handler())
	get := func(target string) int {
		r := httptest.NewRequest("GET", target, nil)
		r.RemoteAddr = "192.168.1.20:5000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for _, p := range []string{"/app/", "/app/inbox", "/assets/x.js", "/sw.js"} {
		if got := get(p); got != 200 {
			t.Errorf("GET %s unpaired: %d, want 200", p, got)
		}
	}
	for _, p := range []string{
		"/api/ships/..%2F..%2F..%2Fapp%2Fx/conversation",
		"/api/v1/ships/..%2F..%2F..%2F..%2Fapp%2Fx/conversation",
		"/ws/bridge/..%2F..%2Fapp%2Fx",
		"/api%2F..%2Fapp/x",
	} {
		if got := get(p); got != 401 {
			t.Errorf("GET %s unpaired: %d, want 401", p, got)
		}
	}
}
