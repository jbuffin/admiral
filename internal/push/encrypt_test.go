package push_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/push/pushtest"
)

func target(s *pushtest.Subscriber) push.Subscription {
	return push.Subscription{Endpoint: "https://push.example/x", P256dh: s.P256dh(), Auth: s.Auth()}
}

func TestEncryptRoundTripsThroughTheSubscribersOwnDecryption(t *testing.T) {
	t.Parallel()
	sub := pushtest.NewSubscriber(t)
	for _, n := range []int{1, 200, 3500} {
		plain := bytes.Repeat([]byte("é"), n/2+1)[:n]
		body, err := push.Encrypt(target(sub), plain)
		if err != nil {
			t.Fatalf("%d bytes: %v", n, err)
		}
		got, err := sub.Decrypt(body)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%d bytes: decrypt err %v, equal %v", n, err, bytes.Equal(got, plain))
		}
	}
}

func TestEncryptWritesTheAES128GCMHeader(t *testing.T) {
	t.Parallel()
	sub := pushtest.NewSubscriber(t)
	body, err := push.Encrypt(target(sub), []byte("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) < 21+65 {
		t.Fatalf("body is %d bytes, shorter than the header", len(body))
	}
	if rs := binary.BigEndian.Uint32(body[16:20]); rs != 4096 {
		t.Errorf("record size %d, want 4096", rs)
	}
	if body[20] != 65 || body[21] != 0x04 {
		t.Errorf("keyid length %d first byte %#x, want a 65-byte uncompressed point", body[20], body[21])
	}
}

func TestEncryptUsesAFreshSaltAndEphemeralKeyEachTime(t *testing.T) {
	t.Parallel()
	sub := pushtest.NewSubscriber(t)
	a, _ := push.Encrypt(target(sub), []byte("same"))
	b, _ := push.Encrypt(target(sub), []byte("same"))
	if len(a) < 86 || len(b) < 86 {
		t.Fatal("bodies shorter than the header")
	}
	if bytes.Equal(a[:16], b[:16]) {
		t.Error("two messages share a salt")
	}
	if bytes.Equal(a[21:86], b[21:86]) {
		t.Error("two messages share an ephemeral key")
	}
	for _, body := range [][]byte{a, b} {
		if got, err := sub.Decrypt(body); err != nil || string(got) != "same" {
			t.Fatalf("decrypt: %q %v", got, err)
		}
	}
}

func TestEncryptMixesTheAuthSecretIn(t *testing.T) {
	t.Parallel()
	sub := pushtest.NewSubscriber(t)
	imposter := pushtest.NewSubscriber(t)
	s := target(sub)
	s.Auth = imposter.Auth()
	body, err := push.Encrypt(s, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := sub.Decrypt(body); err == nil {
		t.Fatalf("decrypted with the wrong auth secret: %q", got)
	}
}

func TestEncryptRefusesAKeyThatIsNotAP256Point(t *testing.T) {
	t.Parallel()
	sub := pushtest.NewSubscriber(t)
	for name, p := range map[string]string{
		"too short": "AAAA",
		"all zero":  strings.Repeat("A", 87),
		"empty":     "",
	} {
		s := target(sub)
		s.P256dh = p
		if _, err := push.Encrypt(s, []byte("x")); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestEncryptRefusesAPayloadThatDoesNotFitOneRecord(t *testing.T) {
	t.Parallel()
	if _, err := push.Encrypt(target(pushtest.NewSubscriber(t)), make([]byte, 4096)); err == nil {
		t.Fatal("a 4096-byte payload was accepted; push services take 4096 bytes in all")
	}
}
