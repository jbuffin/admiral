package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const unauthorizedPage = "This device isn't paired with Admiral"

// The binary starts, prints both URLs with the real port, and serves the
// embedded UI with its assets.
func TestS1_StartsPrintsURLsAndServesUI(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	in := startAdmiral(t, home, "127.0.0.1:0")

	if in.Port == "0" || in.Port == "" {
		t.Fatalf("first line must show the real port, got %q", in.LocalURL)
	}
	if !strings.Contains(in.LANLine, ":"+in.Port+"/") {
		t.Fatalf("second line must show the same real port %s, got %q", in.Port, in.LANLine)
	}

	fi, err := os.Stat(home)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("ADMIRAL_HOME must be created with mode 0700, got %v (%v)", fi, err)
	}
	if _, err := os.Stat(filepath.Join(home, "token")); err == nil {
		t.Fatal("the shared token file must not exist")
	}

	resp, html := get(t, in.LocalURL+"/", nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET / = %d %s, want 200 text/html", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	assets := regexp.MustCompile(`(?:src|href)="(/[^"]+\.(?:js|css))"`).FindAllStringSubmatch(html, -1)
	if len(assets) < 2 {
		t.Fatalf("index.html should reference a JS and a CSS asset, found %v in:\n%s", assets, html)
	}
	var css string
	for _, a := range assets {
		r, body := get(t, in.LocalURL+a[1], nil)
		if r.StatusCode != 200 {
			t.Fatalf("GET %s = %d", a[1], r.StatusCode)
		}
		if strings.HasSuffix(a[1], ".css") {
			css += body
		}
	}
	fonts := regexp.MustCompile(`url\(["']?(/[^)"']+\.woff2)["']?\)`).FindAllStringSubmatch(css, -1)
	if len(fonts) == 0 {
		t.Fatalf("built CSS should reference bundled .woff2 fonts served from the binary")
	}
	for _, f := range fonts {
		if r, _ := get(t, in.LocalURL+f[1], nil); r.StatusCode != 200 {
			t.Fatalf("GET %s = %d", f[1], r.StatusCode)
		}
	}
	if strings.Contains(css+html, "fonts.googleapis.com") || strings.Contains(css+html, "fonts.gstatic.com") {
		t.Fatal("the UI must not reference Google Fonts")
	}
}

// Non-local requests need a device token; pairing by URL sets the cookie and redirects.
func TestS2_NonLocalNeedsToken(t *testing.T) {
	t.Parallel()
	in := startLAN(t)
	base := in.lanURL()

	t.Run("no token", func(t *testing.T) {
		resp, body := get(t, base+"/", nil)
		if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") ||
			!strings.Contains(body, unauthorizedPage) {
			t.Fatalf("GET / = %d %s %q, want 401 text/html containing %q", resp.StatusCode, resp.Header.Get("Content-Type"), body, unauthorizedPage)
		}
	})
	t.Run("wrong token", func(t *testing.T) {
		resp, body := get(t, base+"/?token=nope", nil)
		if resp.StatusCode != 401 || !strings.Contains(body, unauthorizedPage) {
			t.Fatalf("wrong token: %d %q, want 401 with %q", resp.StatusCode, body, unauthorizedPage)
		}
		resp, _ = get(t, base+"/", map[string]string{"Cookie": in.cookieName() + "=nope"})
		if resp.StatusCode != 401 {
			t.Fatalf("wrong cookie: %d, want 401", resp.StatusCode)
		}
	})
	t.Run("api without token", func(t *testing.T) {
		resp, body := get(t, base+"/api/anything", nil)
		if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("Content-Type"), "application/json") ||
			strings.TrimSpace(body) != `{"error":"unauthorized"}` {
			t.Fatalf("GET /api/anything = %d %s %q, want 401 JSON {\"error\":\"unauthorized\"}", resp.StatusCode, resp.Header.Get("Content-Type"), body)
		}
	})
	t.Run("pairing sets the cookie and redirects", func(t *testing.T) {
		var minted struct{ Code string }
		resp, body := do(t, http.MethodPost, in.LocalURL+"/api/v1/pairing-codes", nil)
		if err := json.Unmarshal([]byte(body), &minted); err != nil || resp.StatusCode != 200 {
			t.Fatalf("mint a code: %d %s", resp.StatusCode, body)
		}
		resp, _ = get(t, base+"/pair?code="+minted.Code, nil)
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
			t.Fatalf("GET /pair?code= = %d to %q, want 302 to /", resp.StatusCode, resp.Header.Get("Location"))
		}
		var cookie *http.Cookie
		for _, c := range resp.Cookies() {
			if c.Name == in.cookieName() {
				cookie = c
			}
		}
		if cookie == nil {
			t.Fatalf("no %s cookie in %v", in.cookieName(), resp.Header["Set-Cookie"])
		}
		if cookie.Value == "" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode ||
			cookie.Path != "/" || cookie.MaxAge != 400*24*3600 {
			t.Fatalf("cookie = %+v, want a token, HttpOnly, SameSite=Strict, Path=/, Max-Age 400 days", cookie)
		}
		resp, _ = get(t, base+"/", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value})
		if resp.StatusCode != 200 {
			t.Fatalf("cookie alone: GET / = %d, want 200", resp.StatusCode)
		}
		resp, _ = get(t, base+"/api/anything", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value})
		if resp.StatusCode == 401 {
			t.Fatalf("cookie alone must also pass /api/*, got 401")
		}
		resp, _ = get(t, base+"/pair?code="+minted.Code, nil)
		if resp.StatusCode == http.StatusFound || len(resp.Cookies()) != 0 {
			t.Fatalf("a used code = %d with cookies %v, want the entry page and no cookie", resp.StatusCode, resp.Cookies())
		}
	})
}

// Local requests need no token.
func TestS3_LocalNeedsNoToken(t *testing.T) {
	t.Parallel()
	in := startAdmiral(t, newHome(t), "127.0.0.1:0")
	if resp, _ := get(t, in.LocalURL+"/", nil); resp.StatusCode != 200 {
		t.Fatalf("local GET / = %d, want 200", resp.StatusCode)
	}
	if resp, _ := get(t, in.LocalURL+"/api/anything", nil); resp.StatusCode == 401 {
		t.Fatalf("local /api/anything must not need a token, got 401")
	}
}

// The port survives a restart, and no shared token file appears.
func TestS4_RestartKeepsThePort(t *testing.T) {
	t.Parallel()
	in := startAdmiral(t, newHome(t), "127.0.0.1:"+freePort(t))
	again := in.Restart(t)
	if again.Port != in.Port {
		t.Fatalf("after restart port = %s, want %s", again.Port, in.Port)
	}
	if _, err := os.Stat(filepath.Join(in.Home, "token")); err == nil {
		t.Fatal("the shared token file appeared")
	}
	if resp, _ := get(t, again.LocalURL+"/", nil); resp.StatusCode != 200 {
		t.Fatalf("GET / after restart = %d", resp.StatusCode)
	}
}

// Over the LAN address, the old cookie keeps working after a restart.
func TestS4_CookieSurvivesRestart(t *testing.T) {
	t.Parallel()
	in := startLAN(t)
	cookie := map[string]string{"Cookie": in.cookieName() + "=" + in.Token}
	if resp, _ := get(t, in.lanURL()+"/", cookie); resp.StatusCode != 200 {
		t.Fatalf("cookie before restart: %d, want 200", resp.StatusCode)
	}
	again := in.Restart(t)
	requireLAN(t, again)
	if again.Port != in.Port {
		t.Fatalf("after restart port = %s, want %s", again.Port, in.Port)
	}
	if resp, _ := get(t, again.lanURL()+"/", cookie); resp.StatusCode != 200 {
		t.Fatalf("old cookie after restart: %d, want 200", resp.StatusCode)
	}
}

// The WebSocket at /ws follows the same access rules plus the Origin and
// Host hardening.
func TestS5_WebSocketAccess(t *testing.T) {
	t.Parallel()
	in := startAdmiral(t, newHome(t), "0.0.0.0:"+freePort(t))
	type hello struct {
		Type    string `json:"type"`
		Version string `json:"version"`
	}
	expectHello := func(t *testing.T, base, query string, hdr http.Header, host string) {
		t.Helper()
		c, resp, err := wsDial(t, base, query, hdr, host)
		if err != nil {
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			t.Fatalf("websocket should connect, got status %d: %v", status, err)
		}
		var h hello
		readJSON(t, c, &h)
		if h.Type != "hello" || h.Version == "" {
			t.Fatalf("first message = %+v, want {type:hello, version:<non-empty>}", h)
		}
	}
	expectRefused := func(t *testing.T, base, query string, hdr http.Header, host string, want int) {
		t.Helper()
		_, resp, err := wsDial(t, base, query, hdr, host)
		if err == nil {
			t.Fatalf("websocket should be refused with %d, but connected", want)
		}
		if resp == nil || resp.StatusCode != want {
			t.Fatalf("websocket refusal = %v (err %v), want status %d", resp, err, want)
		}
	}

	t.Run("local gets hello, with or without a matching Origin", func(t *testing.T) {
		expectHello(t, in.LocalURL, "", nil, "")
		expectHello(t, in.LocalURL, "", http.Header{"Origin": {in.LocalURL}}, "")
	})
	t.Run("non-local without token is refused", func(t *testing.T) {
		requireLAN(t, in)
		expectRefused(t, in.lanURL(), "", nil, "", 401)
		expectRefused(t, in.lanURL(), "token=nope", nil, "", 401)
	})
	t.Run("non-local with cookie or Bearer connects, ?token= does not", func(t *testing.T) {
		requireLAN(t, in)
		expectHello(t, in.lanURL(), "", http.Header{"Cookie": {in.cookieName() + "=" + in.Token}}, "")
		expectHello(t, in.lanURL(), "", http.Header{"Authorization": {"Bearer " + in.Token}}, "")
		expectRefused(t, in.lanURL(), "token="+url.QueryEscape(in.Token), nil, "", 401)
	})
	t.Run("foreign Origin is refused even from a local peer", func(t *testing.T) {
		expectRefused(t, in.LocalURL, "", http.Header{"Origin": {"http://evil.example"}}, "", 403)
		resp, _ := do(t, http.MethodPost, in.LocalURL+"/api/anything", map[string]string{"Origin": "http://evil.example"})
		if resp.StatusCode != 403 {
			t.Fatalf("POST with foreign Origin = %d, want 403", resp.StatusCode)
		}
	})
	t.Run("loopback peer with unknown Host needs a device token", func(t *testing.T) {
		expectRefused(t, in.LocalURL, "", nil, "evil.example:"+in.Port, 401)
		if resp, _ := get(t, in.LocalURL+"/", map[string]string{"Host": "evil.example:" + in.Port}); resp.StatusCode != 401 {
			t.Fatalf("GET / with Host evil.example = %d, want 401", resp.StatusCode)
		}
	})
	t.Run("non-local with unknown Host and valid token is accepted", func(t *testing.T) {
		requireLAN(t, in)
		expectHello(t, in.lanURL(), "", http.Header{"Authorization": {"Bearer " + in.Token}}, "vpn.example:"+in.Port)
	})
}

// Two instances side by side keep separate device lists.
func TestS6_TwoInstancesSideBySide(t *testing.T) {
	t.Parallel()
	a := startLAN(t)
	b := startLAN(t)
	if a.Port == b.Port {
		t.Fatalf("both instances report port %s", a.Port)
	}
	for _, in := range []*instance{a, b} {
		if resp, _ := get(t, in.LocalURL+"/", nil); resp.StatusCode != 200 {
			t.Fatalf("instance on %s: GET / = %d", in.Port, resp.StatusCode)
		}
	}
	if resp, _ := get(t, b.lanURL()+"/", map[string]string{"Authorization": "Bearer " + a.Token}); resp.StatusCode != 401 {
		t.Fatalf("a's device token on b: %d, want 401", resp.StatusCode)
	}
}
