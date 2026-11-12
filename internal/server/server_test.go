package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbuffin/admiral/internal/server"
)

const (
	oldIndex = "<!doctype html><title>OLD-UI-INDEX</title>"
	appIndex = "<!doctype html><title>NEW-SHELL-INDEX</title>"
)

func uiServer() *server.Server {
	return &server.Server{UI: fstest.MapFS{
		"index.html":     {Data: []byte(oldIndex)},
		"app/index.html": {Data: []byte(appIndex)},
		"assets/x.js":    {Data: []byte("console.log('x')")},
	}}
}

func get(t *testing.T, h http.Handler, target string) (int, http.Header, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	res := rec.Result()
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(b)
}

func TestShellServedUnderApp_OldUIUnchanged(t *testing.T) {
	h := uiServer().Handler()
	cases := []struct {
		path string
		want string // body, or "" for 404
	}{
		{"/classic", oldIndex},
		{"/classic/", oldIndex},
		{"/classic/x", oldIndex},
		{"/ships/x", ""}, // an extensionless path outside /app and /classic is no UI route
		{"/application", ""},
		{"/app", appIndex},
		{"/app/", appIndex},
		{"/app/ships/x", appIndex},
		{"/app/missing.js", ""}, // a file that isn't there is a 404, not the shell
		{"/assets/x.js", "console.log('x')"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			code, _, body := get(t, h, c.path)
			if c.want == "" {
				if code != http.StatusNotFound {
					t.Fatalf("GET %s = %d, want 404", c.path, code)
				}
				return
			}
			if code != http.StatusOK || body != c.want {
				t.Fatalf("GET %s = %d %q, want 200 %q", c.path, code, body, c.want)
			}
		})
	}
}

func TestRootRedirectsToTheNewShell(t *testing.T) {
	h := uiServer().Handler()
	cases := []struct{ method, target, want string }{
		{http.MethodGet, "/", "/app/"},
		{http.MethodHead, "/", "/app/"},
		{http.MethodGet, "/?fleet=x", "/app/?fleet=x"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(c.method, c.target, nil))
			// A permanent redirect would outlive a rollback in every browser that saw it.
			if rec.Body.Len() != 0 {
				t.Fatalf("%s %s body = %q, want none", c.method, c.target, rec.Body.String())
			}
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != c.want {
				t.Fatalf("%s %s = %d to %q, want 302 to %q", c.method, c.target, rec.Code, rec.Header().Get("Location"), c.want)
			}
		})
	}
}

func TestClassicOldUIKeepsItsAbsoluteAssetsAndAPI(t *testing.T) {
	h := uiServer().Handler()
	for _, p := range []string{"/classic/", "/classic"} {
		if code, _, body := get(t, h, p); code != http.StatusOK || body != oldIndex {
			t.Errorf("GET %s = %d %q, want the old index", p, code, body)
		}
	}
	if code, _, body := get(t, h, "/assets/x.js"); code != http.StatusOK || body != "console.log('x')" {
		t.Errorf("GET /assets/x.js = %d %q, want the asset the old index references", code, body)
	}
}

const claudeSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><path d="M0 0h1v1z"/></svg>`

func seedLogos(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ADMIRAL_HOME", home)
	logos := filepath.Join(home, "logos")
	if err := os.MkdirAll(logos, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"claude.svg":  claudeSVG,
		"unknown.svg": "<svg>unknown</svg>",
	} {
		if err := os.WriteFile(filepath.Join(logos, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A file next to the logos folder that no request may reach.
	if err := os.WriteFile(filepath.Join(home, "secret.svg"), []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestLogos_ServesRuntimeFileWithSafeHeaders(t *testing.T) {
	seedLogos(t)
	h := uiServer().Handler()
	for _, prefix := range []string{"/api/v1/logos/claude"} {
		code, hdr, body := get(t, h, prefix)
		if code != http.StatusOK || body != claudeSVG {
			t.Fatalf("GET %s = %d %q, want 200 with the file", prefix, code, body)
		}
		if ct := hdr.Get("Content-Type"); ct != "image/svg+xml" {
			t.Errorf("Content-Type = %q, want image/svg+xml", ct)
		}
		if v := hdr.Get("X-Content-Type-Options"); v != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", v)
		}
		csp := hdr.Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") && !strings.Contains(csp, "script-src 'none'") {
			t.Errorf("Content-Security-Policy = %q, want a policy that allows no script", csp)
		}
	}
}

func TestLogos_OnlyHarnessNamesAndNothingOutsideTheFolder(t *testing.T) {
	seedLogos(t)
	h := uiServer().Handler()

	// Positive control: the 404s below come from the refusals, not a missing route.
	if code, _, _ := get(t, h, "/api/v1/logos/claude"); code != http.StatusOK {
		t.Fatalf("control GET /api/v1/logos/claude = %d, want 200", code)
	}

	for _, c := range []struct{ name, path string }{
		{"harness without a file", "/api/v1/logos/codex"},
		{"unknown", "/api/v1/logos/unknown"}, // the file exists; only harness names count
		{"with extension", "/api/v1/logos/claude.svg"},
		{"encoded dotdot", "/api/v1/logos/%2e%2e%2fsecret"},
		{"double encoded", "/api/v1/logos/%252e%252e%252fsecret"},
		{"backslash", `/api/v1/logos/..%5Csecret`},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, _, body := get(t, h, c.path)
			if code != http.StatusNotFound {
				t.Fatalf("GET %s = %d, want 404", c.path, code)
			}
			if strings.Contains(body, "TOP-SECRET") || strings.Contains(body, "<svg>") {
				t.Fatalf("GET %s leaked file content: %q", c.path, body)
			}
		})
	}

	// A literal .. is cleaned by the mux and must not reach the home either.
	code, _, body := get(t, h, "/api/v1/logos/../../secret.svg")
	if code == http.StatusOK || strings.Contains(body, "TOP-SECRET") {
		t.Fatalf("literal traversal = %d %q, must not serve a file outside logos/", code, body)
	}
}

func installServer() *server.Server {
	return &server.Server{Version: "1.2.3", Commit: "abc1234", UI: fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><head></head><title>OLD-UI-INDEX</title>")},
		"app/index.html":       {Data: []byte("<!doctype html><head></head><title>NEW-SHELL-INDEX</title>")},
		"manifest.webmanifest": {Data: []byte(`{"name":"Admiral"}`)},
		"sw.js":                {Data: []byte("self.addEventListener('fetch',()=>{})")},
		"icons/icon-192.png":   {Data: []byte("png")},
		"assets/x.js":          {Data: []byte("console.log('x')")},
	}}
}

func TestManifestAndWorkerAreServedAsRevalidatedFiles(t *testing.T) {
	h := installServer().Handler()
	cases := []struct {
		path, contentType string
	}{
		{"/manifest.webmanifest", "application/manifest+json"},
		{"/sw.js", "javascript"},
		{"/icons/icon-192.png", "image/png"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			code, hdr, _ := get(t, h, c.path)
			if code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", c.path, code)
			}
			if ct := hdr.Get("Content-Type"); !strings.Contains(ct, c.contentType) {
				t.Errorf("Content-Type = %q, want it to hold %q", ct, c.contentType)
			}
			// A browser must see a new worker or manifest on its next check, so neither takes the immutable header.
			if cc := hdr.Get("Cache-Control"); cc != "no-cache" {
				t.Errorf("Cache-Control = %q, want no-cache", cc)
			}
		})
	}
}

func TestWorkerNeedsNoScopeWidening(t *testing.T) {
	_, hdr, _ := get(t, installServer().Handler(), "/sw.js")
	if v := hdr.Get("Service-Worker-Allowed"); v != "" {
		t.Errorf("Service-Worker-Allowed = %q, want none: the worker registers with scope /app/", v)
	}
}

func TestShellIsStampedWithTheServersBuild(t *testing.T) {
	h := installServer().Handler()

	_, hdr, body := get(t, h, "/app/")
	if !strings.Contains(body, `<meta name="admiral-build" content="1.2.3 abc1234">`) {
		t.Errorf("GET /app/ = %q, want the build stamp", body)
	}
	if hdr.Get("Cache-Control") != "no-cache" {
		t.Errorf("stamped shell Cache-Control = %q, want no-cache", hdr.Get("Cache-Control"))
	}
	_, _, deep := get(t, h, "/app/ships/x")
	if !strings.Contains(deep, `name="admiral-build"`) {
		t.Errorf("a deep link into the shell = %q, want the build stamp too", deep)
	}
	if _, _, old := get(t, h, "/classic/"); strings.Contains(old, "admiral-build") {
		t.Errorf("the old UI = %q, want no stamp", old)
	}
}
