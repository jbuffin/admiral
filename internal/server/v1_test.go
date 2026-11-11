package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jbuffin/admiral/internal/server"
)

type echo struct{}

func (echo) Routes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/things/{key}/order", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, r.URL.Path+" "+r.PathValue("key"))
	})
}

func (echo) Connected(*server.Client) {}

func do(t *testing.T, h http.Handler, method, target string) (int, http.Header, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	res := rec.Result()
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(b)
}

func TestAPIv1MirrorsTheUnversionedRoutes(t *testing.T) {
	h := (&server.Server{Modules: []server.Module{echo{}}}).Handler()
	for _, c := range []struct {
		method, path string
		status       int
		body         string
	}{
		{"PUT", "/things/k%2Fx/order", 200, "/api/things/k/x/order k/x"}, // an encoded slash stays one segment
		{"GET", "/things/k/order", 404, `{"error":"not found"}` + "\n"},  // a wrong method lands in the JSON catch-all
		{"GET", "/does-not-exist", 404, `{"error":"not found"}` + "\n"},
	} {
		oldCode, oldHdr, oldBody := do(t, h, c.method, "/api"+c.path)
		v1Code, v1Hdr, v1Body := do(t, h, c.method, "/api/v1"+c.path)
		if oldCode != c.status || oldBody != c.body {
			t.Errorf("%s /api%s = %d %q, want %d %q", c.method, c.path, oldCode, oldBody, c.status, c.body)
		}
		if v1Code != oldCode || v1Body != oldBody || v1Hdr.Get("Content-Type") != oldHdr.Get("Content-Type") || v1Hdr.Get("Cache-Control") != oldHdr.Get("Cache-Control") {
			t.Errorf("%s /api/v1%s = %d %q %v, want what /api answers: %d %q %v", c.method, c.path, v1Code, v1Body, v1Hdr, oldCode, oldBody, oldHdr)
		}
	}
	if code, _, body := do(t, h, "PUT", "/api/v1things/k/order"); code != 404 || body != `{"error":"not found"}`+"\n" {
		t.Errorf("/api/v1things is not a v1 path, got %d %q", code, body)
	}
}
