package e2e

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// sameAnswer requires two responses to agree on status, the headers that
// matter and the body.
func sameAnswer(t *testing.T, what string, old, v1 *http.Response, oldBody, v1Body string) {
	t.Helper()
	if old.StatusCode != v1.StatusCode {
		t.Errorf("%s: v1 status = %d, unversioned = %d", what, v1.StatusCode, old.StatusCode)
	}
	for _, h := range []string{"Content-Type", "Cache-Control", "X-Content-Type-Options", "Allow"} {
		if v1.Header.Get(h) != old.Header.Get(h) {
			t.Errorf("%s: v1 %s = %q, unversioned = %q", what, h, v1.Header.Get(h), old.Header.Get(h))
		}
	}
	if v1Body != oldBody {
		t.Errorf("%s: v1 body = %q, unversioned = %q", what, v1Body, oldBody)
	}
}

// The page socket answers on /ws/v1 (the exact path, no trailing slash)
// as it does on /ws, any method included, and the Origin rule carries over.
func TestAPIv1_PageSocketAndOrigin(t *testing.T) {
	t.Parallel()
	in := startAdmiral(t, newHome(t), "127.0.0.1:0")

	t.Run("/ws/v1 sends hello", func(t *testing.T) {
		c, resp, err := wsDialAt(t, in.LocalURL, "/ws/v1", "", nil, "")
		if err != nil {
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			t.Fatalf("/ws/v1 should connect, got status %d: %v", status, err)
		}
		var h struct {
			Type    string `json:"type"`
			Version string `json:"version"`
		}
		readJSON(t, c, &h)
		if h.Type != "hello" || h.Version == "" {
			t.Fatalf("first message = %+v, want {type:hello, version:<non-empty>}", h)
		}
	})
	t.Run("a matching Origin connects, a foreign one is refused", func(t *testing.T) {
		if _, resp, err := wsDialAt(t, in.LocalURL, "/ws/v1", "", http.Header{"Origin": {in.LocalURL}}, ""); err != nil {
			t.Fatalf("/ws/v1 with the server's own Origin should connect (status %v): %v", resp, err)
		}
		_, resp, err := wsDialAt(t, in.LocalURL, "/ws/v1", "", http.Header{"Origin": {"http://evil.example"}}, "")
		if err == nil || resp == nil || resp.StatusCode != 403 {
			t.Fatalf("/ws/v1 with a foreign Origin = %v (err %v), want 403", resp, err)
		}
	})
	t.Run("a v1 POST with a foreign Origin is refused", func(t *testing.T) {
		resp, _ := do(t, http.MethodPost, in.LocalURL+"/api/v1/ships", map[string]string{"Origin": "http://evil.example"})
		if resp.StatusCode != 403 {
			t.Fatalf("POST /api/v1/ships with a foreign Origin = %d, want 403", resp.StatusCode)
		}
	})
	t.Run("any method on /ws/v1 answers as on /ws", func(t *testing.T) {
		old, oldBody := do(t, http.MethodPut, in.LocalURL+"/ws", nil)
		v1, v1Body := do(t, http.MethodPut, in.LocalURL+"/ws/v1", nil)
		if old.StatusCode != 426 {
			t.Fatalf("setup: PUT /ws = %d, want 426", old.StatusCode)
		}
		sameAnswer(t, "PUT /ws/v1", old, v1, oldBody, v1Body)
	})
}

var semver = regexp.MustCompile(`^v?\d+\.\d+\.\d+`)

// GET /api/version names the build, the API versions and the server
// features, and is never cached.
func TestAPIVersion_NamesBuildAndFeatures(t *testing.T) {
	t.Parallel()
	in := startAdmiral(t, newHome(t), "127.0.0.1:0")
	resp, body := get(t, in.LocalURL+"/api/version", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/version = %d %q, want 200", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var v struct {
		Version      string   `json:"version"`
		Commit       *string  `json:"commit"`
		APIVersions  []int    `json:"api_versions"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("body %q is not the documented shape: %v", body, err)
	}
	if v.Version == "" || semver.MatchString(v.Version) {
		t.Errorf("version = %q, want the binary's non-empty build version (not an API semver)", v.Version)
	}
	if v.Commit == nil {
		t.Errorf("body %q has no commit field", body)
	}
	if len(v.APIVersions) != 1 || v.APIVersions[0] != 1 {
		t.Errorf("api_versions = %v, want [1]", v.APIVersions)
	}
	if len(v.Capabilities) == 0 {
		t.Errorf("capabilities = %v, want a non-empty array of server-feature names", v.Capabilities)
	}
	for _, c := range v.Capabilities {
		if c == "" {
			t.Errorf("capabilities %v holds an empty name", v.Capabilities)
		}
	}
	// The page socket's hello already names the same build.
	c, _, err := wsDial(t, in.LocalURL, "", nil, "")
	if err != nil {
		t.Fatalf("setup: /ws: %v", err)
	}
	var h struct{ Version string }
	readJSON(t, c, &h)
	if v.Version != h.Version {
		t.Errorf("/api/version version = %q, the hello's version = %q, want the same binary version", v.Version, h.Version)
	}
}

// Over the LAN address, /api/version and /api/v1/… need exactly
// the access /api/… needs.
func TestAPIv1_AccessParityOverTheLAN(t *testing.T) {
	t.Parallel()
	in := startLAN(t)
	base := in.lanURL()
	cookie := map[string]string{"Cookie": in.cookieName() + "=" + in.Token}

	for _, p := range []string{"/api/version", "/api/v1/ships", "/api/ships"} {
		resp, body := get(t, base+p, nil)
		if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("Content-Type"), "application/json") ||
			strings.TrimSpace(body) != `{"error":"unauthorized"}` {
			t.Errorf("GET %s without a token = %d %s %q, want 401 JSON {\"error\":\"unauthorized\"}",
				p, resp.StatusCode, resp.Header.Get("Content-Type"), body)
		}
	}
	for _, p := range []string{"/api/version", "/api/v1/ships", "/api/ships"} {
		if resp, body := get(t, base+p, cookie); resp.StatusCode != 200 {
			t.Errorf("GET %s with the cookie = %d %q, want 200", p, resp.StatusCode, body)
		}
	}
	if _, resp, err := wsDialAt(t, base, "/ws/v1", "", nil, ""); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Errorf("/ws/v1 without a token = %v (err %v), want 401", resp, err)
	}
	if _, resp, err := wsDialAt(t, base, "/ws/v1", "", http.Header{"Cookie": {cookie["Cookie"]}}, ""); err != nil {
		t.Errorf("/ws/v1 with the cookie should connect (response %v): %v", resp, err)
	}
}

// A bridge socket joined on /ws/v1/bridge/<session> gets the join reply
// with "api":1 and with no api; an unsupported or wrong-typed api gets an
// error frame and a 1008 close, on either path and on every join.
func TestBridgeJoin_APIVersion(t *testing.T) {
	t.Parallel()
	w := liveFleet(t, "lines 3")
	w.start()
	s := w.launchIn("f", "claude", "apiv1")
	w.waitFile(s.Session, "cues-done")
	w.in.waitStatusList(t, 3*time.Second, s.Session, "underway")

	join := func(extra map[string]any) map[string]any {
		m := map[string]any{"type": "join", "cols": 100, "rows": 30}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for _, c := range []struct {
		name   string
		prefix string
		extra  map[string]any
	}{
		{"v1 path, api 1", "/ws/v1", map[string]any{"api": 1}},
		{"v1 path, no api means 1", "/ws/v1", nil},
		{"old path, api 1", "/ws", map[string]any{"api": 1}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := w.at(t)
			b := w.dialBridgeAt(t, c.prefix, s.Session)
			b.send(join(c.extra))
			j := b.waitJoin(5*time.Second, 1)
			if j.Size.Cols != 100 || j.Ship.Status != "underway" {
				t.Fatalf("join reply = %+v, want the ship's usual join payload", j)
			}
		})
	}

	for _, c := range []struct {
		name   string
		prefix string
		api    any
		exact  string // the full error text when the value is unambiguous
	}{
		{"v1 path, api 2", "/ws/v1", 2, "unsupported api version 2"},
		{"old path, api 2", "/ws", 2, "unsupported api version 2"},
		{"v1 path, api 0", "/ws/v1", 0, "unsupported api version 0"},
		{"v1 path, api as string", "/ws/v1", "1", ""},
		{"v1 path, api as float", "/ws/v1", 1.5, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := w.at(t)
			b := w.dialBridgeAt(t, c.prefix, s.Session)
			b.send(join(map[string]any{"api": c.api}))
			b.waitFor(5*time.Second, "an `error` frame", func() bool { return len(b.texts(0, "error")) > 0 })
			var e struct{ Error string }
			if err := json.Unmarshal(b.texts(0, "error")[0], &e); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(e.Error, "unsupported api version") || (c.exact != "" && e.Error != c.exact) {
				t.Errorf("error = %q, want %q", e.Error, firstNonEmpty(c.exact, "unsupported api version <value>"))
			}
			if n := len(b.texts(0, "join")); n != 0 {
				t.Errorf("an unsupported join still got %d join replies", n)
			}
			code, reason := b.waitClosed(5 * time.Second)
			if code != websocket.StatusPolicyViolation || reason != e.Error {
				t.Errorf("close = %d %q, want 1008 %q", code, reason, e.Error)
			}
		})
	}

	t.Run("a later join on an open socket is checked too", func(t *testing.T) {
		w := w.at(t)
		b := w.dialBridgeAt(t, "/ws/v1", s.Session)
		b.send(join(map[string]any{"api": 1}))
		b.waitJoin(5*time.Second, 1)
		b.send(join(map[string]any{"api": 2}))
		b.waitFor(5*time.Second, "an `error` frame", func() bool { return len(b.texts(0, "error")) > 0 })
		if code, _ := b.waitClosed(5 * time.Second); code != websocket.StatusPolicyViolation {
			t.Errorf("close code = %d, want 1008", code)
		}
	})
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
