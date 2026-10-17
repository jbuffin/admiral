package e2e

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const unauthorizedPage = "Open the URL Admiral printed"

var tokenFormat = regexp.MustCompile(`^[A-Za-z0-9_-]{22,}$`) // >= 128 bits, URL-safe

// S1: the binary starts, prints both URLs with the real port, and serves the
// embedded UI with its assets.
func TestS1_StartsPrintsURLsAndServesUI(t *testing.T) {
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
	tokenFile := filepath.Join(home, "token")
	fi, err = os.Stat(tokenFile)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("$ADMIRAL_HOME/token must exist with mode 0600, got %v (%v)", fi, err)
	}
	b, _ := os.ReadFile(tokenFile)
	if !tokenFormat.MatchString(strings.TrimSpace(string(b))) {
		t.Fatalf("token %q is not URL-safe with at least 128 bits", b)
	}
	if in.LANIP != "" && strings.TrimSpace(string(b)) != in.Token {
		t.Fatalf("token file %q differs from printed token %q", b, in.Token)
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

// S2: non-local requests need the token; ?token= sets the cookie and redirects.
func TestS2_NonLocalNeedsToken(t *testing.T) {
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
	t.Run("token sets cookie and redirects", func(t *testing.T) {
		resp, _ := get(t, base+"/?foo=1&token="+in.Token, nil)
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("GET /?token=<right> = %d, want 302", resp.StatusCode)
		}
		loc, err := resp.Request.URL.Parse(resp.Header.Get("Location"))
		if err != nil || loc.Path != "/" || loc.Query().Has("token") || loc.Query().Get("foo") != "1" {
			t.Fatalf("Location %q should be the same URL without token", resp.Header.Get("Location"))
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
		if cookie.Value != in.Token || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode ||
			cookie.Path != "/" || cookie.MaxAge != 400*24*3600 {
			t.Fatalf("cookie = %+v, want token value, HttpOnly, SameSite=Strict, Path=/, Max-Age 400 days", cookie)
		}
		resp, _ = get(t, base+"/", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value})
		if resp.StatusCode != 200 {
			t.Fatalf("cookie alone: GET / = %d, want 200", resp.StatusCode)
		}
		resp, _ = get(t, base+"/api/anything", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value})
		if resp.StatusCode == 401 {
			t.Fatalf("cookie alone must also pass /api/*, got 401")
		}
	})
}

// S3: local requests need no token.
func TestS3_LocalNeedsNoToken(t *testing.T) {
	in := startAdmiral(t, newHome(t), "127.0.0.1:0")
	if resp, _ := get(t, in.LocalURL+"/", nil); resp.StatusCode != 200 {
		t.Fatalf("local GET / = %d, want 200", resp.StatusCode)
	}
	if resp, _ := get(t, in.LocalURL+"/api/anything", nil); resp.StatusCode == 401 {
		t.Fatalf("local /api/anything must not need a token, got 401")
	}
}

// S4: the token survives a restart. The file half needs no LAN address.
func TestS4_TokenFileSurvivesRestart(t *testing.T) {
	in := startAdmiral(t, newHome(t), "127.0.0.1:"+freePort(t))
	read := func() string {
		b, err := os.ReadFile(filepath.Join(in.Home, "token"))
		if err != nil {
			t.Fatalf("token file: %v", err)
		}
		return strings.TrimSpace(string(b))
	}
	before := read()
	again := in.Restart(t)
	if after := read(); after != before || again.Port != in.Port {
		t.Fatalf("after restart token/port = %q/%s, want %q/%s", after, again.Port, before, in.Port)
	}
	if resp, _ := get(t, again.LocalURL+"/", nil); resp.StatusCode != 200 {
		t.Fatalf("GET / after restart = %d", resp.StatusCode)
	}
}

// S4: over the LAN address, the old cookie keeps working after a restart.
func TestS4_CookieSurvivesRestart(t *testing.T) {
	in := startLAN(t)
	cookie := map[string]string{"Cookie": in.cookieName() + "=" + in.Token}
	if resp, _ := get(t, in.lanURL()+"/", cookie); resp.StatusCode != 200 {
		t.Fatalf("cookie before restart: %d, want 200", resp.StatusCode)
	}
	again := in.Restart(t)
	requireLAN(t, again)
	if again.Token != in.Token || again.Port != in.Port {
		t.Fatalf("after restart token/port = %q/%s, want %q/%s", again.Token, again.Port, in.Token, in.Port)
	}
	if resp, _ := get(t, again.lanURL()+"/", cookie); resp.StatusCode != 200 {
		t.Fatalf("old cookie after restart: %d, want 200", resp.StatusCode)
	}
}

// S5: the WebSocket at /ws follows the same access rules plus the Origin and
// Host hardening.
func TestS5_WebSocketAccess(t *testing.T) {
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
	t.Run("non-local with cookie or ?token= connects", func(t *testing.T) {
		requireLAN(t, in)
		expectHello(t, in.lanURL(), "", http.Header{"Cookie": {in.cookieName() + "=" + in.Token}}, "")
		expectHello(t, in.lanURL(), "token="+url.QueryEscape(in.Token), nil, "")
	})
	t.Run("foreign Origin is refused even from a local peer", func(t *testing.T) {
		expectRefused(t, in.LocalURL, "", http.Header{"Origin": {"http://evil.example"}}, "", 403)
		resp, _ := do(t, http.MethodPost, in.LocalURL+"/api/anything", map[string]string{"Origin": "http://evil.example"})
		if resp.StatusCode != 403 {
			t.Fatalf("POST with foreign Origin = %d, want 403", resp.StatusCode)
		}
	})
	t.Run("local request with unknown Host is refused", func(t *testing.T) {
		expectRefused(t, in.LocalURL, "", nil, "evil.example:"+in.Port, 403)
		if resp, _ := get(t, in.LocalURL+"/", map[string]string{"Host": "evil.example:" + in.Port}); resp.StatusCode != 403 {
			t.Fatalf("GET / with Host evil.example = %d, want 403", resp.StatusCode)
		}
	})
	t.Run("non-local with unknown Host and valid token is accepted", func(t *testing.T) {
		requireLAN(t, in)
		expectHello(t, in.lanURL(), "token="+url.QueryEscape(in.Token), nil, "vpn.example:"+in.Port)
	})
}

// S6: two instances side by side keep separate tokens.
func TestS6_TwoInstancesSideBySide(t *testing.T) {
	a := startAdmiral(t, newHome(t), "127.0.0.1:0")
	b := startAdmiral(t, newHome(t), "127.0.0.1:0")
	if a.Port == b.Port {
		t.Fatalf("both instances report port %s", a.Port)
	}
	read := func(in *instance) string {
		b, err := os.ReadFile(filepath.Join(in.Home, "token"))
		if err != nil {
			t.Fatalf("token file: %v", err)
		}
		return strings.TrimSpace(string(b))
	}
	ta, tb := read(a), read(b)
	if ta == "" || ta == tb {
		t.Fatalf("each instance needs its own token file, got %q and %q", ta, tb)
	}
	for _, in := range []*instance{a, b} {
		if resp, _ := get(t, in.LocalURL+"/", nil); resp.StatusCode != 200 {
			t.Fatalf("instance on %s: GET / = %d", in.Port, resp.StatusCode)
		}
	}
}
