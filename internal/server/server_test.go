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
		{"/", oldIndex},
		{"/ships/x", oldIndex}, // the old UI's own client-side route
		{"/application", oldIndex},
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
