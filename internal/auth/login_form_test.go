package auth_test

import (
	"mime"
	"testing"
)

// Tailscale's serve Q-encodes a non-ASCII login; the allowlist, the stored login and the binding all use the plain form.
func TestTailnetLoginIsDecodedFromTheEncodedHeader(t *testing.T) {
	const plain = "jörg@example.com"
	encoded := mime.QEncoding.Encode("utf-8", plain)
	if encoded == plain {
		t.Fatalf("test setup: %q was not encoded", plain)
	}
	g := newRig(t)
	g.allow(plain)

	rec, out := g.redeem(tailReq(encoded), g.mint(), "")
	if rec.Code != 200 || out.Token == "" {
		t.Fatalf("pairing with the encoded login while the plain one is allowed: %d %s", rec.Code, rec.Body)
	}
	for name, login := range map[string]string{"encoded": encoded, "plain": plain} {
		if c := g.tailStatus(out.Token, login); c != 200 {
			t.Errorf("token bound at pairing, request with the %s login: %d, want 200", name, c)
		}
	}
	r := tailReq(plain)
	r.hdr["Authorization"] = "Bearer " + out.Token
	devices := g.list(r)
	if len(devices) != 1 || devices[0].Login != plain {
		t.Errorf("stored devices = %+v, want one bound to the plain login %q", devices, plain)
	}
}

// Tailscale sends an ASCII login as it is, so a login that only looks encoded must not collapse into another one.
func TestAnEncodedLookingASCIILoginStaysRaw(t *testing.T) {
	for _, raw := range []string{
		"=?utf-8?q?bob?=@example.com",
		"bob=?utf-8?q?=40?=example.com",
		"=?us-ascii?q?bob=40example.com?=",
		"=?utf-8?b?Ym9iQGV4YW1wbGUuY29t?=",
	} {
		t.Run(raw, func(t *testing.T) {
			g := newRig(t)
			g.allow("bob@example.com")
			if c := g.tailStatus("", raw); c == 200 {
				t.Errorf("login %q reached the app as an allowed login", raw)
			}
			rec, _ := g.redeem(tailReq(raw), g.mint(), "")
			if rec.Code == 200 {
				t.Errorf("pairing as %q was accepted", raw)
			}
		})
	}
}
