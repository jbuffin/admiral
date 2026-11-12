package push_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/push/pushtest"
)

func TestKeyIsMadeOnFirstUseAs0600FileAndReusedAcrossRestarts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, err := push.LoadKey(dir)
	if err != nil {
		t.Fatalf("LoadKey on an empty home: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first.Public())
	if err != nil || len(raw) != 65 || raw[0] != 0x04 {
		t.Fatalf("Public() %q: want base64url of a 65-byte uncompressed P-256 point (err %v, %d bytes)", first.Public(), err, len(raw))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("home holds %d entries after first use, want only the key file (no temp file left)", len(entries))
	}
	if fi, _ := entries[0].Info(); fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v, want 0600", fi.Mode().Perm())
	}
	again, err := push.LoadKey(dir)
	if err != nil || again.Public() != first.Public() {
		t.Fatalf("second LoadKey: %v, public %q vs %q, want the same key", err, again.Public(), first.Public())
	}
	other, err := push.LoadKey(t.TempDir())
	if err != nil || other.Public() == first.Public() {
		t.Fatalf("a different home must get its own key (err %v)", err)
	}
}

func TestACorruptKeyFileIsAnErrorAndIsNotReplaced(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := push.LoadKey(dir); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	path := filepath.Join(dir, entries[0].Name())
	garbage := []byte("{not a key")
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	if k, err := push.LoadKey(dir); err == nil {
		t.Fatalf("LoadKey on a corrupt file returned a key (%q) instead of an error", k.Public())
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, garbage) {
		t.Fatal("a corrupt key file was overwritten: every device would silently lose its subscription")
	}
}

func TestAnUnreadableKeyFileIsAnErrorNotANewKey(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	dir := t.TempDir()
	if _, err := push.LoadKey(dir); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	path := filepath.Join(dir, entries[0].Name())
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, err := push.LoadKey(dir); err == nil {
		t.Fatal("LoadKey on an unreadable key file succeeded")
	}
}

func TestAuthHeaderCarriesAnES256JWTThatTheKeyVerifies(t *testing.T) {
	t.Parallel()
	key, err := push.LoadKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	hdr, err := push.AuthHeader(key, "https://web.push.example:8443/send/abc", now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pushtest.VerifyVAPID(hdr)
	if err != nil {
		t.Fatalf("%v in %q", err, hdr)
	}
	if got.Key != key.Public() {
		t.Errorf("k=%q, want the public key %q", got.Key, key.Public())
	}
	if got.Aud != "https://web.push.example:8443" {
		t.Errorf("aud=%q, want the endpoint's origin", got.Aud)
	}
	if got.Sub != "https://github.com/jbuffin/admiral" {
		t.Errorf("sub=%q, want the project URL (a valid URI that names no one)", got.Sub)
	}
	if !got.Exp.After(now) || got.Exp.After(now.Add(24*time.Hour)) {
		t.Errorf("exp=%v, want within 24 h after %v", got.Exp, now)
	}
}

func TestAuthHeaderAudFollowsEachEndpointsOrigin(t *testing.T) {
	t.Parallel()
	key, _ := push.LoadKey(t.TempDir())
	for endpoint, want := range map[string]string{
		"https://fcm.googleapis.com/fcm/send/x":  "https://fcm.googleapis.com",
		"https://updates.push.services.example/": "https://updates.push.services.example",
	} {
		hdr, err := push.AuthHeader(key, endpoint, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if got, err := pushtest.VerifyVAPID(hdr); err != nil || got.Aud != want {
			t.Errorf("%s: aud %q err %v, want %q", endpoint, got.Aud, err, want)
		}
	}
}
