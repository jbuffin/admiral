package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/auth"
)

const (
	lan       = "192.168.1.50:51000"
	loopback  = "127.0.0.1:51000"
	tsHost    = "mac.tailnet.ts.net"
	cookieKey = "admiral_token_7420"
	safariMac = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15"
	chromeAnd = "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Mobile Safari/537.36"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

// remoteCfg stands in for the live config: auth reads it on every request.
type remoteCfg struct {
	mu     sync.Mutex
	on     bool
	logins []string
}

func (c *remoteCfg) get() (bool, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.on, append([]string(nil), c.logins...)
}

type rig struct {
	t      *testing.T
	home   string
	clk    *clock
	a      *auth.Auth
	h      http.Handler
	remote *remoteCfg
}

// allow turns Tailscale mode on with exactly these logins allowed.
func (g *rig) allow(logins ...string) {
	g.remote.mu.Lock()
	defer g.remote.mu.Unlock()
	g.remote.on, g.remote.logins = true, logins
}

func (g *rig) setMode(on bool) {
	g.remote.mu.Lock()
	defer g.remote.mu.Unlock()
	g.remote.on = on
}

// The next handler answers 200 "next"; the device API is registered the way the server's mux does it, so its paths are the unversioned ones (the server strips /v1 after the guard).
func newRig(t *testing.T) *rig {
	return openRig(t, t.TempDir(), &clock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)})
}

func openRig(t *testing.T, home string, clk *clock) *rig {
	t.Helper()
	rc := &remoteCfg{}
	a, err := auth.New(home, "7420", auth.WithClock(clk.Now), auth.WithBaseURL(func() string { return "http://192.168.1.10:7420" }), auth.WithRemote(rc.get))
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	mux := http.NewServeMux()
	a.Routes(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("next")) })
	return &rig{t: t, home: home, clk: clk, a: a, h: a.Middleware(mux), remote: rc}
}

type req struct {
	method, target, remote, host string
	hdr                          map[string]string
	body                         string
}

func (g *rig) do(r req) *httptest.ResponseRecorder {
	g.t.Helper()
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	if r.method == "" {
		r.method = "GET"
	}
	rq := httptest.NewRequest(r.method, r.target, body)
	rq.RemoteAddr = r.remote
	if r.host != "" {
		rq.Host = r.host
	}
	for k, v := range r.hdr {
		rq.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, rq)
	return rec
}

func (g *rig) local(method, target, body string) *httptest.ResponseRecorder {
	hdr := map[string]string{}
	if body != "" {
		hdr["Content-Type"] = "application/json"
	}
	return g.do(req{method: method, target: target, remote: loopback, host: "127.0.0.1:7420", hdr: hdr, body: body})
}

var crockford = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)

func (g *rig) mint() string {
	g.t.Helper()
	rec := g.local("POST", "/api/pairing-codes", "")
	if rec.Code != 200 && rec.Code != 201 {
		g.t.Fatalf("mint a pairing code locally: %d %s", rec.Code, rec.Body)
	}
	var out struct{ Code string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Code == "" {
		g.t.Fatalf("mint response: %v %s", err, rec.Body)
	}
	return out.Code
}

type pairResult struct {
	Token  string
	Device struct{ ID, Name string }
}

// redeem pairs over the JSON API from the given path.
func (g *rig) redeem(r req, code, name string) (*httptest.ResponseRecorder, pairResult) {
	g.t.Helper()
	b, _ := json.Marshal(map[string]string{"code": code, "name": name})
	if name == "" {
		b, _ = json.Marshal(map[string]string{"code": code})
	}
	r.method, r.target, r.body = "POST", "/api/v1/pair", string(b)
	if r.hdr == nil {
		r.hdr = map[string]string{}
	}
	r.hdr["Content-Type"] = "application/json"
	rec := g.do(r)
	var out pairResult
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func lanReq(hdr map[string]string) req { return req{remote: lan, host: "192.168.1.10:7420", hdr: hdr} }

func tailReq(login string) req {
	return req{remote: loopback, host: tsHost, hdr: map[string]string{"Tailscale-User-Login": login, "X-Forwarded-For": "100.64.0.9"}}
}

// pairLAN mints a code and redeems it as a LAN client; the token is returned.
func (g *rig) pairLAN() string {
	g.t.Helper()
	rec, out := g.redeem(lanReq(nil), g.mint(), "")
	if rec.Code != 200 || out.Token == "" {
		g.t.Fatalf("pair on the LAN: %d %s", rec.Code, rec.Body)
	}
	return out.Token
}

func (g *rig) pairTail(login string) string {
	g.t.Helper()
	rec, out := g.redeem(tailReq(login), g.mint(), "")
	if rec.Code != 200 || out.Token == "" {
		g.t.Fatalf("pair on the tailnet path: %d %s", rec.Code, rec.Body)
	}
	return out.Token
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func (g *rig) lanStatus(token string) int {
	r := lanReq(bearer(token))
	r.target = "/x"
	return g.do(r).Code
}

func (g *rig) tailStatus(token, login string) int {
	r := tailReq(login)
	r.hdr["Authorization"] = "Bearer " + token
	r.target = "/x"
	return g.do(r).Code
}

func TestIsLocal(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		host   string
		hdr    map[string]string
		want   bool
	}{
		{"loopback, allowed Host", loopback, "localhost:7420", nil, true},
		{"loopback IPv6", "[::1]:51000", "[::1]:7420", nil, true},
		{"LAN peer", lan, "192.168.1.10:7420", nil, false},
		{"X-Forwarded-For", loopback, "localhost:7420", map[string]string{"X-Forwarded-For": "100.64.0.9"}, false},
		{"Forwarded", loopback, "localhost:7420", map[string]string{"Forwarded": "for=100.64.0.9"}, false},
		{"Tailscale-User-Login", loopback, "localhost:7420", map[string]string{"Tailscale-User-Login": "fake@example.com"}, false},
		{"Tailscale-User-Name", loopback, "localhost:7420", map[string]string{"Tailscale-User-Name": "Fake"}, false},
		{"tailscale serve Host", loopback, tsHost, nil, false},
		{"rebinding Host", loopback, "evil.example:7420", nil, false},
	}
	for _, c := range cases {
		rq := httptest.NewRequest("GET", "/", nil)
		rq.RemoteAddr, rq.Host = c.remote, c.host
		for k, v := range c.hdr {
			rq.Header.Set(k, v)
		}
		if got := auth.IsLocal(rq); got != c.want {
			t.Errorf("%s: IsLocal = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestProxiedLoopbackGoesToTokenAuth(t *testing.T) {
	g := newRig(t)
	if rec := g.local("GET", "/x", ""); rec.Code != 200 {
		t.Fatalf("a local request: %d", rec.Code)
	}
	for name, r := range map[string]req{
		"tailscale serve Host": {remote: loopback, host: tsHost},
		"X-Forwarded-For":      {remote: loopback, host: "localhost:7420", hdr: map[string]string{"X-Forwarded-For": "100.64.0.9"}},
		"Tailscale-User-Login": {remote: loopback, host: "localhost:7420", hdr: map[string]string{"Tailscale-User-Login": "fake@example.com"}},
		"DNS-rebinding page":   {remote: loopback, host: "evil.example:7420"},
	} {
		r.target = "/api/config"
		if rec := g.do(r); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with no device token: %d, want 401", name, rec.Code)
		}
	}
}

func TestPairByURLSetsCookieAndRedirects(t *testing.T) {
	g := newRig(t)
	code := g.mint()
	if !crockford.MatchString(code) {
		t.Fatalf("code %q is not XXXX-XXXX of Crockford base32", code)
	}
	r := lanReq(map[string]string{"User-Agent": safariMac})
	r.target = "/pair?code=" + url.QueryEscape(code)
	rec := g.do(r)
	if rec.Code != 302 || rec.Header().Get("Location") != "/" {
		t.Fatalf("redeem: %d to %q, want 302 to /", rec.Code, rec.Header().Get("Location"))
	}
	cs := rec.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != cookieKey || !cs[0].HttpOnly || cs[0].SameSite != http.SameSiteStrictMode || cs[0].Path != "/" || cs[0].Value == "" {
		t.Fatalf("cookie: %+v", cs)
	}
	if strings.Contains(rec.Header().Get("Location"), cs[0].Value) {
		t.Fatal("the token is in the redirect URL")
	}
	r = lanReq(map[string]string{"Cookie": cookieKey + "=" + cs[0].Value})
	r.target = "/x"
	if rec := g.do(r); rec.Code != 200 {
		t.Errorf("the cookie does not authenticate: %d", rec.Code)
	}

	// Used: the entry page with an error, no cookie.
	r = lanReq(nil)
	r.target = "/pair?code=" + url.QueryEscape(code)
	rec = g.do(r)
	if rec.Code == 302 || len(rec.Result().Cookies()) != 0 || !strings.Contains(rec.Body.String(), "<form") {
		t.Errorf("a used code: %d, cookies %v, body %q; want the entry page and no cookie", rec.Code, rec.Result().Cookies(), rec.Body)
	}
}

func TestTypedCodeAcceptsLowercaseWithoutHyphen(t *testing.T) {
	g := newRig(t)
	code := g.mint()
	typed := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	rec, out := g.redeem(lanReq(nil), typed, "")
	if rec.Code != 200 || out.Token == "" {
		t.Fatalf("typed %q for %q: %d %s", typed, code, rec.Code, rec.Body)
	}
}

func TestEntryPageAndFormPost(t *testing.T) {
	g := newRig(t)
	r := lanReq(nil)
	r.target = "/pair"
	rec := g.do(r)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(body, "<form") || !strings.Contains(strings.ToLower(body), `method="post"`) || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("entry page: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), body)
	}

	form := url.Values{"code": {g.mint()}}.Encode()
	r = lanReq(map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	r.method, r.target, r.body = "POST", "/pair", form
	rec = g.do(r)
	if rec.Code != 302 || rec.Header().Get("Location") != "/" || len(rec.Result().Cookies()) != 1 {
		t.Fatalf("form post: %d to %q, cookies %v", rec.Code, rec.Header().Get("Location"), rec.Result().Cookies())
	}
}

func TestPairOverJSONNamesTheDevice(t *testing.T) {
	g := newRig(t)
	rec, out := g.redeem(lanReq(map[string]string{"User-Agent": safariMac}), g.mint(), "")
	if rec.Code != 200 || out.Token == "" || out.Device.ID == "" || out.Device.Name != "Safari on Mac" {
		t.Errorf("guessed name: %d %s, want device name \"Safari on Mac\"", rec.Code, rec.Body)
	}
	_, out = g.redeem(lanReq(map[string]string{"User-Agent": chromeAnd}), g.mint(), "")
	if out.Device.Name != "Chrome on Android" {
		t.Errorf("guessed name %q, want \"Chrome on Android\"", out.Device.Name)
	}
	_, out = g.redeem(lanReq(map[string]string{"User-Agent": safariMac}), g.mint(), "Jason's tablet")
	if out.Device.Name != "Jason's tablet" {
		t.Errorf("a name in the POST: %q", out.Device.Name)
	}
}

func TestMintedCode(t *testing.T) {
	g := newRig(t)
	rec := g.local("POST", "/api/pairing-codes", "")
	var out struct {
		Code    string
		URL     string
		Expires time.Time
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if out.URL != "http://192.168.1.10:7420/pair?code="+out.Code {
		t.Errorf("url %q for code %q", out.URL, out.Code)
	}
	if want := g.clk.now.Add(5 * time.Minute); !out.Expires.Equal(want) {
		t.Errorf("expires %v, want %v", out.Expires, want)
	}
	r := lanReq(nil)
	r.method, r.target = "POST", "/api/pairing-codes"
	if rec := g.do(r); rec.Code != 401 {
		t.Errorf("minting a code with no token from the LAN: %d, want 401", rec.Code)
	}
}

func TestCodeExpiresAfterFiveMinutes(t *testing.T) {
	g := newRig(t)
	code := g.mint()
	g.clk.advance(5*time.Minute + time.Second)
	if rec, _ := g.redeem(lanReq(nil), code, ""); rec.Code == 200 {
		t.Fatal("a code redeemed after 5 minutes")
	}
	code = g.mint()
	g.clk.advance(4*time.Minute + 59*time.Second)
	if rec, _ := g.redeem(lanReq(nil), code, ""); rec.Code != 200 {
		t.Fatalf("a code redeemed at 4:59: %d", rec.Code)
	}
}

func TestOneLiveCode(t *testing.T) {
	g := newRig(t)
	first, second := g.mint(), g.mint()
	if rec, _ := g.redeem(lanReq(nil), first, ""); rec.Code == 200 {
		t.Fatal("the earlier code still redeems after a new one was minted")
	}
	if rec, _ := g.redeem(lanReq(nil), second, ""); rec.Code != 200 {
		t.Fatalf("the newest code: %d", rec.Code)
	}
}

func TestRedemptionCap(t *testing.T) {
	g := newRig(t)
	code := g.mint()
	for i := 0; i < 10; i++ {
		if rec, _ := g.redeem(lanReq(nil), "ZZZZ-ZZZZ", ""); rec.Code == 200 || rec.Code == 429 {
			t.Fatalf("failed redemption %d: %d, want a refusal other than 429 yet", i+1, rec.Code)
		}
	}
	if rec, _ := g.redeem(lanReq(nil), code, ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a valid code after 10 failures: %d, want 429", rec.Code)
	}
	r := lanReq(nil)
	r.target = "/pair?code=" + code
	if rec := g.do(r); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a valid code by URL after 10 failures: %d, want 429", rec.Code)
	}
	g.clk.advance(5*time.Minute + time.Second)
	if rec, _ := g.redeem(lanReq(nil), g.mint(), ""); rec.Code != 200 {
		t.Fatalf("after the window: %d, want 200", rec.Code)
	}
}

func TestOnlyPairIsReachableWithoutAToken(t *testing.T) {
	g := newRig(t)
	for _, p := range []string{"/api/pair", "/api/v1/devices", "/api/devices", "/api/v1/pairing-codes", "/ws"} {
		r := lanReq(nil)
		r.target = p
		if rec := g.do(r); rec.Code != 401 {
			t.Errorf("%s with no token: %d, want 401", p, rec.Code)
		}
	}
	r := lanReq(nil)
	r.method, r.target, r.body = "POST", "/api/pair", `{"code":"ZZZZ-ZZZZ"}`
	if rec := g.do(r); rec.Code != 401 {
		t.Errorf("unversioned POST /api/pair: %d, want 401", rec.Code)
	}
}

func TestQueryTokenIsGone(t *testing.T) {
	g := newRig(t)
	token := g.pairLAN()
	r := lanReq(nil)
	r.target = "/x?token=" + token
	if rec := g.do(r); rec.Code != 401 {
		t.Errorf("?token= on a GET: %d, want 401", rec.Code)
	}
	r = lanReq(map[string]string{"Upgrade": "websocket", "Connection": "Upgrade"})
	r.target = "/ws?token=" + token
	if rec := g.do(r); rec.Code != 401 {
		t.Errorf("?token= on a WebSocket: %d, want 401", rec.Code)
	}
}

func TestBearerSkipsOriginCookieKeepsIt(t *testing.T) {
	g := newRig(t)
	rec, out := g.redeem(lanReq(nil), g.mint(), "")
	if rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	evil := map[string]string{"Origin": "http://evil.example"}
	with := func(extra map[string]string) req {
		h := map[string]string{}
		for k, v := range evil {
			h[k] = v
		}
		for k, v := range extra {
			h[k] = v
		}
		r := lanReq(h)
		r.method, r.target = "POST", "/x"
		return r
	}
	if rec := g.do(with(bearer(out.Token))); rec.Code != 200 {
		t.Errorf("Bearer with a foreign Origin: %d, want 200", rec.Code)
	}
	if rec := g.do(with(map[string]string{"Cookie": cookieKey + "=" + out.Token})); rec.Code != 403 {
		t.Errorf("cookie with a foreign Origin: %d, want 403", rec.Code)
	}
	r := lanReq(map[string]string{"Authorization": "Bearer fake-token-wrong"})
	r.target = "/x"
	if rec := g.do(r); rec.Code != 401 {
		t.Errorf("a wrong Bearer: %d, want 401", rec.Code)
	}
}

func TestBearerOnWebSocketUpgrade(t *testing.T) {
	g := newRig(t)
	token := g.pairLAN()
	r := lanReq(map[string]string{"Upgrade": "websocket", "Connection": "Upgrade", "Authorization": "Bearer " + token})
	r.target = "/ws"
	if rec := g.do(r); rec.Code != 200 {
		t.Errorf("WebSocket upgrade with Bearer: %d, want 200", rec.Code)
	}
}

func TestCookieIsReissuedButNotOnEveryRequest(t *testing.T) {
	g := newRig(t)
	token := g.pairLAN()
	get := func() *httptest.ResponseRecorder {
		r := lanReq(map[string]string{"Cookie": cookieKey + "=" + token})
		r.target = "/x"
		return g.do(r)
	}
	reissued := func(rec *httptest.ResponseRecorder) bool {
		for _, c := range rec.Result().Cookies() {
			if c.Name == cookieKey && c.Value == token && c.MaxAge == 400*24*3600 && c.HttpOnly {
				return true
			}
		}
		return false
	}
	n := 0
	for i := 0; i < 50; i++ {
		if reissued(get()) {
			n++
		}
	}
	if n > 1 {
		t.Errorf("the cookie was re-issued on %d of 50 back-to-back requests", n)
	}
	g.clk.advance(40 * 24 * time.Hour)
	if !reissued(get()) {
		t.Error("40 days on, a request with a good cookie did not re-issue it")
	}
}

type device struct {
	ID       string
	Name     string
	Login    string
	PairedAt time.Time
	LastSeen time.Time
	Current  bool
}

func (g *rig) list(r req) []device {
	g.t.Helper()
	r.target = "/api/devices"
	rec := g.do(r)
	var out struct{ Devices []device }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		g.t.Fatalf("list devices: %d %s", rec.Code, rec.Body)
	}
	return out.Devices
}

func TestDeviceAPI(t *testing.T) {
	g := newRig(t)
	rec, a := g.redeem(lanReq(map[string]string{"User-Agent": safariMac}), g.mint(), "")
	if rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	_, b := g.redeem(lanReq(map[string]string{"User-Agent": chromeAnd}), g.mint(), "")

	list := g.list(lanReq(bearer(a.Token)))
	if len(list) != 2 {
		t.Fatalf("devices: %+v", list)
	}
	byID := map[string]device{}
	for _, d := range list {
		byID[d.ID] = d
	}
	if d := byID[a.Device.ID]; !d.Current || d.Name != "Safari on Mac" || d.Login != "" || !d.PairedAt.Equal(g.clk.now) || d.LastSeen.IsZero() {
		t.Errorf("the requesting device: %+v", d)
	}
	if d := byID[b.Device.ID]; d.Current || d.Name != "Chrome on Android" {
		t.Errorf("the other device: %+v", d)
	}
	for _, d := range g.list(lanReq(bearer(b.Token))) {
		if d.Current != (d.ID == b.Device.ID) {
			t.Errorf("current flag for %s seen from b: %v", d.ID, d.Current)
		}
	}
	for _, d := range g.list(req{remote: loopback, host: "127.0.0.1:7420"}) {
		if d.Current {
			t.Errorf("a local request has no current device, got %+v", d)
		}
	}

	if rec := g.local("PATCH", "/api/devices/"+a.Device.ID, `{"name":"Desk iPad"}`); rec.Code != 200 && rec.Code != 204 {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	if d := g.list(lanReq(bearer(b.Token))); !hasName(d, "Desk iPad") {
		t.Errorf("rename not listed: %+v", d)
	}

	if rec := g.local("DELETE", "/api/devices/"+a.Device.ID, ""); rec.Code != 200 && rec.Code != 204 {
		t.Fatalf("revoke one: %d %s", rec.Code, rec.Body)
	}
	if c := g.lanStatus(a.Token); c != 401 {
		t.Errorf("revoked device: %d, want 401", c)
	}
	if c := g.lanStatus(b.Token); c != 200 {
		t.Errorf("the other device after a revoke: %d, want 200", c)
	}
	if rec := g.local("DELETE", "/api/devices", ""); rec.Code != 200 && rec.Code != 204 {
		t.Fatalf("revoke all: %d %s", rec.Code, rec.Body)
	}
	if c := g.lanStatus(b.Token); c != 401 {
		t.Errorf("after revoke all: %d, want 401", c)
	}
}

func hasName(ds []device, name string) bool {
	for _, d := range ds {
		if d.Name == name {
			return true
		}
	}
	return false
}

func TestLoginBinding(t *testing.T) {
	g := newRig(t)
	const alice, bob = "alice@example.com", "bob@example.com"
	g.allow(alice, bob)
	tail, onLAN := g.pairTail(alice), g.pairLAN()

	if c := g.tailStatus(tail, alice); c != 200 {
		t.Errorf("tailnet token on the tailnet path with its login: %d", c)
	}
	if c := g.tailStatus(tail, bob); c != 401 {
		t.Errorf("tailnet token with another login: %d, want 401", c)
	}
	if c := g.lanStatus(tail); c != 401 {
		t.Errorf("tailnet token on the LAN path: %d, want 401", c)
	}
	var seen device
	for _, d := range g.list(req{remote: loopback, host: "127.0.0.1:7420"}) {
		if d.Login == alice {
			seen = d
		}
	}
	if seen.ID == "" {
		t.Errorf("no device lists login %s", alice)
	}

	if c := g.lanStatus(onLAN); c != 200 {
		t.Errorf("LAN token on the LAN path: %d", c)
	}
	if c := g.tailStatus(onLAN, alice); c != 401 {
		t.Errorf("LAN token on the tailnet path: %d, want 401", c)
	}
	r := lanReq(map[string]string{"Authorization": "Bearer " + onLAN, "X-Forwarded-For": "100.64.0.9"})
	r.target = "/x"
	if rec := g.do(r); rec.Code != 401 {
		t.Errorf("LAN token from a non-loopback peer with proxy headers: %d, want 401", rec.Code)
	}
	r = req{remote: loopback, host: tsHost, hdr: map[string]string{"Authorization": "Bearer " + tail}, target: "/x"}
	if rec := g.do(r); rec.Code != 401 {
		t.Errorf("proxied loopback without Tailscale-User-Login: %d, want 401", rec.Code)
	}
}

func TestCannotPairOverAnOtherPath(t *testing.T) {
	g := newRig(t)
	for name, r := range map[string]req{
		"proxied loopback without a login": {remote: loopback, host: tsHost, hdr: map[string]string{"X-Forwarded-For": "100.64.0.9"}},
		"LAN peer with proxy headers":      lanReq(map[string]string{"X-Forwarded-For": "100.64.0.9"}),
	} {
		code := g.mint()
		rec, out := g.redeem(r, code, "")
		if rec.Code == 200 || out.Token != "" {
			t.Errorf("%s paired: %d %s", name, rec.Code, rec.Body)
		}
	}
	// A refused attempt does not burn the code.
	if rec, _ := g.redeem(lanReq(nil), g.mint(), ""); rec.Code != 200 {
		t.Errorf("a normal pairing afterwards: %d", rec.Code)
	}
}

func TestIdleDeviceDropsAfter90Days(t *testing.T) {
	home := t.TempDir()
	clk := &clock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	g := openRig(t, home, clk)
	idle, busy := g.pairLAN(), g.pairLAN()

	clk.advance(80 * 24 * time.Hour)
	if c := g.lanStatus(busy); c != 200 {
		t.Fatalf("day 80: %d", c)
	}
	clk.advance(11 * 24 * time.Hour) // idle: unseen 91 days; busy: unseen 11
	if c := g.lanStatus(idle); c != 401 {
		t.Errorf("a device unseen for 91 days: %d, want 401", c)
	}
	if c := g.lanStatus(busy); c != 200 {
		t.Errorf("a device seen at day 80: %d, want 200", c)
	}
	if n := len(g.list(req{remote: loopback, host: "127.0.0.1:7420"})); n != 1 {
		t.Errorf("the dropped device is still listed (%d devices)", n)
	}

	// Dropped for good: a restart on the same home does not bring it back.
	clk.advance(100 * 24 * time.Hour)
	g2 := openRig(t, home, clk)
	if c := g2.lanStatus(idle); c != 401 {
		t.Errorf("after a restart: %d, want 401", c)
	}
	if c := g2.lanStatus(busy); c != 401 {
		t.Errorf("a device unseen for 100 days after a restart: %d, want 401", c)
	}
}

func TestDevicesSurviveARestart(t *testing.T) {
	home := t.TempDir()
	clk := &clock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	token := openRig(t, home, clk).pairLAN()
	if c := openRig(t, home, clk).lanStatus(token); c != 200 {
		t.Errorf("after a restart: %d, want 200", c)
	}
}

func TestTokenIsHashedAtRestAndFilesAreOwnerOnly(t *testing.T) {
	g := newRig(t)
	token := g.pairLAN()
	var files int
	err := filepath.Walk(g.home, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		files++
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s has mode %v, want owner-only", p, info.Mode().Perm())
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), token) {
			t.Errorf("%s holds the token in clear", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Error("pairing wrote nothing under the home")
	}
}

func TestSharedTokenFileIsRetired(t *testing.T) {
	home := t.TempDir()
	const old = "fake-token-legacy"
	if err := os.WriteFile(filepath.Join(home, "token"), []byte(old+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := openRig(t, home, &clock{now: time.Now()})
	if _, err := os.Stat(filepath.Join(home, "token")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the shared token file is still there: %v", err)
	}
	if c := g.lanStatus(old); c != 401 {
		t.Errorf("the old shared token as Bearer: %d, want 401", c)
	}
	r := lanReq(map[string]string{"Cookie": cookieKey + "=" + old})
	r.target = "/x"
	if rec := g.do(r); rec.Code != 401 {
		t.Errorf("the old shared token as a cookie: %d, want 401", rec.Code)
	}
}

// The peer address is forged: the LAN address doesn't answer on every machine.
func TestRevokeClosesThatDevicesWebSockets(t *testing.T) {
	g := newRig(t)
	doomed, kept := g.pairLAN(), g.pairLAN()
	var doomedID string
	for _, d := range g.list(lanReq(bearer(doomed))) {
		if d.Current {
			doomedID = d.ID
		}
	}
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			if _, _, err := c.Read(r.Context()); err != nil {
				return
			}
		}
	})
	mux := http.NewServeMux()
	g.a.Routes(mux)
	mux.Handle("/ws", echo)
	guarded := g.a.Middleware(mux)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = lan
		guarded.ServeHTTP(w, r)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func(token string) *websocket.Conn {
		t.Helper()
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return c
	}
	a, b := dial(doomed), dial(kept)

	if rec := g.local("DELETE", "/api/devices/"+doomedID, ""); rec.Code != 200 && rec.Code != 204 {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	readCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	if _, _, err := a.Read(readCtx); err == nil || readCtx.Err() != nil {
		t.Fatalf("the revoked device's socket should close at once, got %v", err)
	}
	keptCtx, stopKept := context.WithTimeout(ctx, 300*time.Millisecond)
	defer stopKept()
	if _, _, err := b.Read(keptCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("another device's socket ended: %v", err)
	}

	if rec := g.local("DELETE", "/api/devices", ""); rec.Code != 200 && rec.Code != 204 {
		t.Fatalf("revoke all: %d", rec.Code)
	}
	allCtx, stopAll := context.WithTimeout(ctx, 3*time.Second)
	defer stopAll()
	if _, _, err := b.Read(allCtx); err == nil || allCtx.Err() != nil {
		t.Fatalf("revoke all should close every socket, got %v", err)
	}
}

func TestRevokeAnswers500AndKeepsTheDeviceWhenTheStoreCannotBeWritten(t *testing.T) {
	g := newRig(t)
	tok := g.pairLAN()
	g.pairLAN()
	if err := os.Chmod(g.home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(g.home, 0o700) })
	if _, err := os.Create(filepath.Join(g.home, "probe")); err == nil {
		t.Skip("the directory is writable despite 0500 (running as root?)")
	}
	var id string
	var list struct{ Devices []struct{ ID string } }
	if err := json.Unmarshal(g.local("GET", "/api/devices", "").Body.Bytes(), &list); err != nil || len(list.Devices) != 2 {
		t.Fatalf("list: %v %+v", err, list)
	}
	id = list.Devices[0].ID
	if c := g.local("DELETE", "/api/devices/"+id, "").Code; c != 500 {
		t.Errorf("revoke with an unwritable store: %d, want 500", c)
	}
	if c := g.local("PATCH", "/api/devices/"+id, `{"name":"x"}`).Code; c != 500 {
		t.Errorf("rename with an unwritable store: %d, want 500", c)
	}
	if c := g.local("DELETE", "/api/devices", "").Code; c != 500 {
		t.Errorf("revoke all with an unwritable store: %d, want 500", c)
	}
	list.Devices = nil
	_ = json.Unmarshal(g.local("GET", "/api/devices", "").Body.Bytes(), &list)
	if len(list.Devices) != 2 {
		t.Errorf("devices after failed revokes: %d, want 2", len(list.Devices))
	}
	if g.lanStatus(tok) != 200 {
		t.Error("a device still listed must still authenticate")
	}
}

// Browsers fetch the manifest link and icons without credentials, so a paired remote browser needs them without a token.
func TestOnlyTheInstallFilesAndTheStaticShellAreReachableWithoutAToken(t *testing.T) {
	g := newRig(t)
	for _, p := range []string{"/manifest.webmanifest", "/icons/icon-192.png", "/icons/icon-512.png", "/icons/icon-maskable-512.png", "/icons/apple-touch-icon.png", "/app/", "/app/ships", "/assets/x.js", "/assets/x.css", "/sw.js", "/"} {
		for _, m := range []string{"GET", "HEAD"} {
			// The manifest and icons are GET-only.
			if m == "HEAD" && !strings.HasPrefix(p, "/app") && !strings.HasPrefix(p, "/assets/") && p != "/sw.js" && p != "/" {
				continue
			}
			r := lanReq(nil)
			r.method, r.target = m, p
			if rec := g.do(r); rec.Code != 200 {
				t.Errorf("%s %s with no token: %d, want 200", m, p, rec.Code)
			}
		}
	}
	// Nothing else is exempt, and only a GET or HEAD is.
	for _, p := range []string{"/api/version", "/api/v1/version", "/api/v1/ships", "/ws/v1", "/icons/other.png", "/icons/", "/manifest.webmanifest/x", "/classic/", "/classic", "/assets/x", "/assets/", "/app/../api/v1/ships", "/app/%2e%2e/api/v1/ships", "/assets/../api/v1/ships", "/assets/..%2fapi/v1/ships"} {
		r := lanReq(nil)
		r.target = p
		if rec := g.do(r); rec.Code != 401 {
			t.Errorf("GET %s with no token: %d, want 401", p, rec.Code)
		}
	}
	for _, p := range []string{"/manifest.webmanifest", "/app/", "/assets/x.js", "/sw.js", "/"} {
		r := lanReq(nil)
		r.method, r.target = "POST", p
		if rec := g.do(r); rec.Code != 401 {
			t.Errorf("POST %s with no token: %d, want 401", p, rec.Code)
		}
	}
}

func TestPairWithCookieSignsTheDeviceInWithoutHandingJSTheToken(t *testing.T) {
	g := newRig(t)
	r := lanReq(nil)
	r.method, r.target = "POST", "/api/v1/pair"
	r.hdr = map[string]string{"Content-Type": "application/json"}
	r.body = `{"code":"` + g.mint() + `","cookie":true}`
	rec := g.do(r)
	if rec.Code != 200 {
		t.Fatalf("pair with cookie: %d %s", rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if _, has := out["token"]; has {
		t.Errorf("the token is in the JSON: %s", rec.Body)
	}
	if out["device"] == nil {
		t.Errorf("no device in the JSON: %s", rec.Body)
	}
	cs := rec.Result().Cookies()
	if len(cs) != 1 || !cs[0].HttpOnly || cs[0].Value == "" {
		t.Fatalf("want one HttpOnly device cookie, got %v", cs)
	}
	next := lanReq(nil)
	next.target = "/x"
	next.hdr = map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value}
	if got := g.do(next).Code; got != 200 {
		t.Errorf("the cookie does not sign the device in: %d", got)
	}
}

func TestPairWithoutCookieStillReturnsTheTokenAndSetsNone(t *testing.T) {
	g := newRig(t)
	rec, out := g.redeem(lanReq(nil), g.mint(), "")
	if rec.Code != 200 || out.Token == "" {
		t.Fatalf("pair: %d %s", rec.Code, rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("a native pair set a cookie: %v", rec.Result().Cookies())
	}
}

func TestPairWithCookieRefusesAWrongCodeWithoutACookie(t *testing.T) {
	g := newRig(t)
	g.mint()
	r := lanReq(nil)
	r.method, r.target = "POST", "/api/v1/pair"
	r.hdr = map[string]string{"Content-Type": "application/json"}
	r.body = `{"code":"ZZZZ-ZZZZ","cookie":true}`
	rec := g.do(r)
	if rec.Code != 400 || len(rec.Result().Cookies()) != 0 {
		t.Errorf("wrong code: %d, cookies %v", rec.Code, rec.Result().Cookies())
	}
}
