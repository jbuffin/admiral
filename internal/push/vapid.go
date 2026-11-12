package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const (
	keyFile = "vapid.json"
	// The subject is a contact URI push services may use to reach the sender; it names the project, never the engineer.
	vapidSubject = "https://github.com/jbuffin/admiral"
	jwtLifetime  = 12 * time.Hour
)

var b64 = base64.RawURLEncoding

// Key is Admiral's one VAPID key pair. Devices subscribe against its public half, so it is never replaced while its file exists.
type Key struct {
	priv *ecdsa.PrivateKey
	pub  string
}

// LoadKey reads the key pair in home, making and saving one only when there is no file. An unreadable or corrupt file is an error.
func LoadKey(home string) (*Key, error) {
	path := filepath.Join(home, keyFile)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			return parseKey(b)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("vapid key: %w", err)
		}
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		raw, err := priv.Bytes()
		if err != nil {
			return nil, err
		}
		out, _ := json.Marshal(map[string]string{"private": b64.EncodeToString(raw)})
		if err := writeNew(path, out); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue // another start made it first; read theirs
			}
			return nil, fmt.Errorf("vapid key: %w", err)
		}
	}
}

// writeNew writes path through a temp file and a hard link, so it never replaces a file that appeared meanwhile.
func writeNew(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vapid.*") // created 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmp.Name(), path)
}

func parseKey(b []byte) (*Key, error) {
	var f struct{ Private string }
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("vapid key file is corrupt: %w", err)
	}
	raw, err := b64.DecodeString(f.Private)
	if err != nil {
		return nil, fmt.Errorf("vapid key file is corrupt: %w", err)
	}
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return nil, fmt.Errorf("vapid key file is corrupt: %w", err)
	}
	return &Key{priv: priv, pub: b64.EncodeToString(mustPublic(priv))}, nil
}

func mustPublic(priv *ecdsa.PrivateKey) []byte {
	b, err := priv.PublicKey.Bytes()
	if err != nil {
		panic(err)
	}
	return b
}

// Public is the application server key a browser subscribes with: base64url of the uncompressed point.
func (k *Key) Public() string { return k.pub }

// AuthHeader is the Authorization header for a push to endpoint (RFC 8292): an ES256 JWT for the endpoint's origin, and the public key.
func AuthHeader(k *Key, endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("endpoint %q has no origin", endpoint)
	}
	head, _ := json.Marshal(map[string]string{"typ": "JWT", "alg": "ES256"})
	claims, _ := json.Marshal(map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": now.Add(jwtLifetime).Unix(), "sub": vapidSubject})
	signed := b64.EncodeToString(head) + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signed + "." + b64.EncodeToString(sig) + ", k=" + k.pub, nil
}
