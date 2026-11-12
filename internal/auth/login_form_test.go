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
