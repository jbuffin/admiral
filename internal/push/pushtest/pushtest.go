// Package pushtest is the subscriber and push service side of Web Push for tests: it decrypts per RFC 8291, verifies a VAPID header per RFC 8292 and plays a push service over TLS. It uses only the standard library, never the sender under test.
package pushtest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var b64 = base64.RawURLEncoding

// Subscriber is a browser's side of a push subscription: an ECDH key pair and an auth secret.
type Subscriber struct {
	priv *ecdh.PrivateKey
	auth []byte
}

func NewSubscriber(t testing.TB) *Subscriber {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return &Subscriber{priv: priv, auth: auth}
}

// SubscriberFrom builds a subscriber from the base64url private key and auth secret.
func SubscriberFrom(privB64, authB64 string) (*Subscriber, error) {
	raw, err := b64.DecodeString(privB64)
	if err != nil {
		return nil, err
	}
	priv, err := ecdh.P256().NewPrivateKey(raw)
	if err != nil {
		return nil, err
	}
	auth, err := b64.DecodeString(authB64)
	if err != nil {
		return nil, err
	}
	return &Subscriber{priv: priv, auth: auth}, nil
}

// P256dh is the subscription's public key, base64url of the 65-byte uncompressed point.
func (s *Subscriber) P256dh() string { return b64.EncodeToString(s.priv.PublicKey().Bytes()) }

// Auth is the subscription's auth secret, base64url.
func (s *Subscriber) Auth() string { return b64.EncodeToString(s.auth) }

// JSON is the body of a browser's PushSubscription for endpoint.
func (s *Subscriber) JSON(endpoint string) string {
	b, _ := json.Marshal(map[string]any{
		"endpoint": endpoint, "expirationTime": nil,
		"keys": map[string]string{"p256dh": s.P256dh(), "auth": s.Auth()},
	})
	return string(b)
}

// Decrypt opens an aes128gcm push body (RFC 8188 framing, RFC 8291 key derivation) and returns the plaintext without its padding.
func (s *Subscriber) Decrypt(body []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("body shorter than the aes128gcm header")
	}
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if rs < 18 {
		return nil, fmt.Errorf("record size %d is below 18", rs)
	}
	if idlen != 65 || len(body) < 21+idlen {
		return nil, fmt.Errorf("keyid is %d bytes, want the 65-byte application server key", idlen)
	}
	asPub := body[21 : 21+idlen]
	record := body[21+idlen:]
	if len(record) > int(rs) {
		return nil, errors.New("more than one record")
	}
	peer, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		return nil, fmt.Errorf("keyid is not a P-256 point: %w", err)
	}
	secret, err := s.priv.ECDH(peer)
	if err != nil {
		return nil, err
	}
	return open(secret, s.auth, salt, s.priv.PublicKey().Bytes(), asPub, record)
}

func open(secret, auth, salt, uaPub, asPub, record []byte) ([]byte, error) {
	prkKey, err := hkdf.Extract(sha256.New, secret, auth)
	if err != nil {
		return nil, err
	}
	keyInfo := append(append([]byte("WebPush: info\x00"), uaPub...), asPub...)
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, record, nil)
	if err != nil {
		return nil, err
	}
	i := bytes.LastIndexFunc(plain, func(r rune) bool { return r != 0 })
	if i < 0 || plain[i] != 0x02 {
		return nil, errors.New("the last record has no 0x02 padding delimiter")
	}
	return plain[:i], nil
}

// VAPID is what an Authorization header proves.
type VAPID struct {
	Key string // k=, base64url of the 65-byte public key
	Aud string
	Sub string
	Exp time.Time
}

// VerifyVAPID checks `vapid t=<jwt>, k=<key>`: an ES256 JWT with a raw r||s signature that the key verifies.
func VerifyVAPID(header string) (VAPID, error) {
	rest, ok := strings.CutPrefix(header, "vapid ")
	if !ok {
		return VAPID{}, errors.New(`Authorization does not start with "vapid "`)
	}
	var tok, key string
	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "t="):
			tok = part[2:]
		case strings.HasPrefix(part, "k="):
			key = part[2:]
		}
	}
	segs := strings.Split(tok, ".")
	if len(segs) != 3 || key == "" {
		return VAPID{}, errors.New("want t=<header.payload.signature>, k=<key>")
	}
	var hdr struct{ Typ, Alg string }
	var claims struct {
		Aud, Sub string
		Exp      int64
	}
	for i, dst := range []any{&hdr, &claims} {
		raw, err := b64.DecodeString(segs[i])
		if err != nil {
			return VAPID{}, fmt.Errorf("segment %d is not base64url: %w", i, err)
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return VAPID{}, err
		}
	}
	if hdr.Alg != "ES256" || hdr.Typ != "JWT" {
		return VAPID{}, fmt.Errorf("header is %+v, want typ JWT and alg ES256", hdr)
	}
	rawKey, err := b64.DecodeString(key)
	if err != nil {
		return VAPID{}, err
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), rawKey)
	if err != nil {
		return VAPID{}, err
	}
	sig, err := b64.DecodeString(segs[2])
	if err != nil || len(sig) != 64 {
		return VAPID{}, fmt.Errorf("signature is %d bytes, want the raw 64-byte r||s (err %v)", len(sig), err)
	}
	sum := sha256.Sum256([]byte(segs[0] + "." + segs[1]))
	if !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return VAPID{}, errors.New("the signature does not verify with k")
	}
	return VAPID{Key: key, Aud: claims.Aud, Sub: claims.Sub, Exp: time.Unix(claims.Exp, 0)}, nil
}

// Request is one push the service received.
type Request struct {
	Path   string
	Header http.Header
	Body   []byte
}

// Service is a fake push service over TLS (httptest's certificate covers 127.0.0.1).
type Service struct {
	*httptest.Server
	reqs chan Request

	mu      sync.Mutex
	status  map[string]int
	blocked map[string]chan struct{}
	seen    int
}

func NewService(t testing.TB) *Service {
	t.Helper()
	s := &Service{reqs: make(chan Request, 256), status: map[string]int{}, blocked: map[string]chan struct{}{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.seen++
		code, gate := s.status[r.URL.Path], s.blocked[r.URL.Path]
		s.mu.Unlock()
		s.reqs <- Request{Path: r.URL.Path, Header: r.Header.Clone(), Body: body}
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
			}
		}
		if code == 0 {
			code = http.StatusCreated
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(func() {
		s.mu.Lock()
		for p, g := range s.blocked {
			close(g)
			delete(s.blocked, p)
		}
		s.mu.Unlock()
		s.Server.Close()
	})
	return s
}

// Endpoint is a subscription endpoint on this service.
func (s *Service) Endpoint(path string) string { return s.URL + path }

// CertPEM is the service's certificate, for a sender's trust roots.
func (s *Service) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
}

func (s *Service) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(s.Certificate())
	return p
}

// SetStatus makes later requests to path answer code (the default is 201).
func (s *Service) SetStatus(path string, code int) {
	s.mu.Lock()
	s.status[path] = code
	s.mu.Unlock()
}

// Block holds every request to path until the returned release runs.
func (s *Service) Block(path string) (release func()) {
	g := make(chan struct{})
	s.mu.Lock()
	s.blocked[path] = g
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.blocked, path)
			s.mu.Unlock()
			close(g)
		})
	}
}

// Next waits up to d for the next request, in arrival order.
func (s *Service) Next(d time.Duration) (Request, bool) {
	select {
	case r := <-s.reqs:
		return r, true
	case <-time.After(d):
		return Request{}, false
	}
}

// Count is how many requests have arrived, read or not.
func (s *Service) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen
}
