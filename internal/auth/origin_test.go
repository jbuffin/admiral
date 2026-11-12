package auth_test

import "testing"

func TestOriginAndHostRules(t *testing.T) {
	g := newRig(t)
	tok := g.pairLAN()
	cookie := map[string]string{"Cookie": cookieKey + "=" + tok}
	with := func(h map[string]string, extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range h {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	if c := g.do(req{method: "POST", target: "/api/x", remote: lan, host: "192.168.1.10:7420", hdr: with(cookie, map[string]string{"Origin": "http://evil.example"})}).Code; c != 403 {
		t.Errorf("cookie with a foreign Origin: %d, want 403", c)
	}
	if c := g.do(req{method: "POST", target: "/api/x", remote: lan, host: "192.168.1.10:7420", hdr: with(bearer(tok), map[string]string{"Origin": "http://evil.example"})}).Code; c != 200 {
		t.Errorf("Bearer with a foreign Origin: %d, want 200", c)
	}
	if c := g.do(req{target: "/ws", remote: lan, host: "admiral.example", hdr: with(cookie, map[string]string{"Upgrade": "websocket", "Origin": "https://admiral.example"})}).Code; c != 200 {
		t.Errorf("ws from an https page behind a TLS proxy: %d, want 200", c)
	}
	if c := g.do(req{target: "/x", remote: lan, host: "vpn.example:7420", hdr: cookie}).Code; c != 200 {
		t.Errorf("LAN device, unknown Host: %d, want 200", c)
	}
	for _, host := range []string{"localhost:7420", "[::1]:7420", "127.0.0.1:7420"} {
		if c := g.do(req{target: "/x", remote: loopback, host: host}).Code; c != 200 {
			t.Errorf("local request, Host %s: %d, want 200", host, c)
		}
	}
	if c := g.do(req{target: "/x", remote: loopback, host: "evil.example:7420"}).Code; c != 401 {
		t.Errorf("loopback peer, Host evil.example: %d, want 401", c)
	}
}
