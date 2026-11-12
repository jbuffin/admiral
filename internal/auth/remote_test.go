package auth_test

import (
	"net/http"
	"testing"
)

const (
	alice = "alice@example.com"
	bob   = "bob@example.com"
)

// proxiedNoLogin is a tailscale serve lookalike that carries no login: Funnel, a tagged device or another proxy.
func proxiedNoLogin(token string) req {
	return req{remote: loopback, host: tsHost, target: "/x", hdr: map[string]string{"X-Forwarded-For": "100.64.0.9", "Authorization": "Bearer " + token}}
}

func TestAuthDecisionAcrossPaths(t *testing.T) {
	g := newRig(t)
	g.allow(alice, bob)
	tail, onLAN := g.pairTail(alice), g.pairLAN()

	if rec := g.local("GET", "/x", ""); rec.Code != 200 {
		t.Errorf("local: %d, want 200", rec.Code)
	}
	if c := g.lanStatus(onLAN); c != 200 {
		t.Errorf("LAN with its token: %d, want 200", c)
	}
	if c := g.tailStatus(tail, alice); c != 200 {
		t.Errorf("tailnet, allowed login, its bound token: %d, want 200", c)
	}
	if c := g.tailStatus(tail, bob); c != 401 {
		t.Errorf("tailnet, another allowed login than the token's: %d, want 401", c)
	}
	if rec := g.do(proxiedNoLogin(tail)); rec.Code != 401 {
		t.Errorf("proxied with no Tailscale-User-Login: %d, want 401", rec.Code)
	}
	if rec, _ := g.redeem(tailReq("carol@example.com"), g.mint(), ""); rec.Code != http.StatusForbidden {
		t.Errorf("tailnet pairing with a login not on the list: %d, want 403", rec.Code)
	}
}

func TestTailnetNeedsTheModeOn(t *testing.T) {
	g := newRig(t)
	g.allow(alice)
	tail, onLAN := g.pairTail(alice), g.pairLAN()

	g.setMode(false) // the allowlist still names alice
	if c := g.tailStatus(tail, alice); c != 401 {
		t.Errorf("tailnet with the mode off: %d, want 401", c)
	}
	if c := g.lanStatus(onLAN); c != 200 {
		t.Errorf("LAN with the mode off: %d, want 200", c)
	}
	if rec := g.local("GET", "/x", ""); rec.Code != 200 {
		t.Errorf("local with the mode off: %d, want 200", rec.Code)
	}
	rec, out := g.redeem(tailReq(alice), g.mint(), "")
	if rec.Code != http.StatusForbidden || out.Token != "" {
		t.Errorf("pairing over the tailnet with the mode off: %d %s, want 403", rec.Code, rec.Body)
	}

	g.setMode(true)
	if c := g.tailStatus(tail, alice); c != 200 {
		t.Errorf("tailnet after the mode is back on: %d, want 200", c)
	}
}

func TestRemovingALoginDisablesItsDevices(t *testing.T) {
	g := newRig(t)
	g.allow(alice, bob)
	forAlice, forBob := g.pairTail(alice), g.pairTail(bob)

	g.allow(bob)
	if c := g.tailStatus(forAlice, alice); c != 401 {
		t.Errorf("a device whose login was removed: %d, want 401", c)
	}
	if c := g.tailStatus(forBob, bob); c != 200 {
		t.Errorf("a device whose login stays: %d, want 200", c)
	}

	g.allow(alice, bob)
	if c := g.tailStatus(forAlice, alice); c != 200 {
		t.Errorf("after the login is added back: %d, want 200", c)
	}
}

func TestTailnetPairingNeedsAnAllowedLogin(t *testing.T) {
	g := newRig(t)
	g.allow(alice)
	code := g.mint()
	for name, login := range map[string]string{"a login not on the list": bob, "a different case": "Alice@example.com"} {
		rec, out := g.redeem(tailReq(login), code, "")
		if rec.Code != http.StatusForbidden || out.Token != "" {
			t.Errorf("pairing with %s: %d %s, want 403", name, rec.Code, rec.Body)
		}
	}
	if rec, _ := g.redeem(lanReq(nil), code, ""); rec.Code != 200 {
		t.Errorf("a refused tailnet pairing burned the code: the LAN redeem answered %d", rec.Code)
	}
}

func TestLoginIsTrimmedOnceForEveryUse(t *testing.T) {
	g := newRig(t)
	g.allow(alice)
	token := g.pairTail("  " + alice + " ")
	if c := g.tailStatus(token, alice); c != 200 {
		t.Errorf("a device paired under a padded login, used with the plain one: %d, want 200", c)
	}
	if c := g.tailStatus(token, alice+"\t"); c != 200 {
		t.Errorf("the same device with a padded login: %d, want 200", c)
	}
	var seen string
	for _, d := range g.list(req{remote: loopback, host: "127.0.0.1:7420"}) {
		seen = d.Login
	}
	if seen != alice {
		t.Errorf("the recorded login is %q, want %q", seen, alice)
	}
}
