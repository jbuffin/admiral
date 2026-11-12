package pushtest_test

import (
	"encoding/base64"
	"testing"

	"github.com/jbuffin/admiral/internal/push/pushtest"
)

// The RFC 8291 section 5 example, so the decrypting side the other tests lean on is checked against the RFC and not against the sender.
func TestDecryptOpensTheRFC8291Example(t *testing.T) {
	t.Parallel()
	const (
		uaScalar = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
		second   = "BTBZMqHH6r4Tts7J_aSIgg"
		header   = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"
		records  = "8pfeW0KbunFT06SuDKoJH9Ql87S1QUrdirN6GcG7sFz1y1sqLgVi1VhjVkHsUoEsbI_0LpXMuGvnzQ"
	)
	sub, err := pushtest.SubscriberFrom(uaScalar, second)
	if err != nil {
		t.Fatal(err)
	}
	h, err := base64.RawURLEncoding.DecodeString(header)
	if err != nil {
		t.Fatal(err)
	}
	r, err := base64.RawURLEncoding.DecodeString(records)
	if err != nil {
		t.Fatal(err)
	}
	raw := append(h, r...)
	got, err := sub.Decrypt(raw)
	if err != nil || string(got) != "When I grow up, I want to be a watermelon" {
		t.Fatalf("got %q, %v", got, err)
	}
}
