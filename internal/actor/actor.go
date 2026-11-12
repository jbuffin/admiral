// Package actor is AdmiralBot's identity on the server: a fleet's actor token, the request header that carries it,
// and the guard that holds an actor request to its fleet and to the routes AdmiralBot may call.
package actor

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// Header carries a fleet's actor token on a loopback request.
const Header = "X-Admiral-Actor"

// Via is the value a write made by AdmiralBot is stamped with.
const Via = "admiralbot"

const secretFile = "actor-secret"

// Minter derives a fleet's actor token from the home's secret key.
type Minter struct{ secret []byte }

// NewMinter opens the secret key under home, creating its 0600 file on first use.
func NewMinter(home string) (*Minter, error) {
	path := filepath.Join(home, secretFile)
	if b, err := os.ReadFile(path); err == nil {
		if secret, err := hex.DecodeString(string(b)); err == nil && len(secret) == 32 {
			return &Minter{secret: secret}, nil
		}
		return nil, errors.New("actor: " + path + " is not a 32-byte hex key")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	// O_EXCL: a second Admiral starting at once reads the winner's key instead of replacing it.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return NewMinter(home)
	}
	if err != nil {
		return nil, err
	}
	_, werr := f.WriteString(hex.EncodeToString(secret))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return nil, werr
	}
	return &Minter{secret: secret}, nil
}

// Token is the fleet's token for one chat session's nonce.
func (m *Minter) Token(fleet, nonce string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(fleet + "\n" + nonce))
	return hex.EncodeToString(mac.Sum(nil))
}

// NewNonce is a fresh value for a chat session; it is not a secret.
func NewNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Lookup is the fleet whose current token is given, comparing against every fleet in constant time.
// current returns "" for a fleet with no chat session.
func Lookup(given string, fleets []string, current func(fleet string) string) (string, bool) {
	found, ok := "", false
	for _, f := range fleets {
		tok := current(f)
		if tok != "" && subtle.ConstantTimeCompare([]byte(given), []byte(tok)) == 1 {
			found, ok = f, true
		}
	}
	return found, ok
}

type ctxKey struct{}

// WithFleet marks ctx as the request of AdmiralBot acting for fleet.
func WithFleet(ctx context.Context, fleet string) context.Context {
	return context.WithValue(ctx, ctxKey{}, fleet)
}

// FleetOf is the fleet AdmiralBot acts for in ctx, and false for the engineer's own request.
func FleetOf(ctx context.Context) (string, bool) {
	f, ok := ctx.Value(ctxKey{}).(string)
	return f, ok
}
