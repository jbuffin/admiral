package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// Subscription is a browser's push subscription: where to send, and the keys that seal what is sent.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

const (
	recordSize = 4096
	tagSize    = 16
	headerSize = 16 + 4 + 1 + 65
)

// Encrypt seals plaintext for the subscriber as one aes128gcm record (RFC 8291 over the RFC 8188 framing), with a fresh salt and ephemeral key.
func Encrypt(sub Subscription, plaintext []byte) ([]byte, error) {
	if len(plaintext)+1+tagSize > recordSize {
		return nil, errors.New("payload does not fit one push record")
	}
	uaRaw, err := b64.DecodeString(strings.TrimRight(sub.P256dh, "="))
	if err != nil {
		return nil, fmt.Errorf("p256dh: %w", err)
	}
	uaPub, err := ecdh.P256().NewPublicKey(uaRaw)
	if err != nil {
		return nil, fmt.Errorf("p256dh is not a P-256 point: %w", err)
	}
	auth, err := b64.DecodeString(strings.TrimRight(sub.Auth, "="))
	if err != nil || len(auth) == 0 {
		return nil, errors.New("auth secret is missing or not base64url")
	}
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	secret, err := eph.ECDH(uaPub)
	if err != nil {
		return nil, err
	}
	asPub := eph.PublicKey().Bytes()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	prkKey, err := hkdf.Extract(sha256.New, secret, auth)
	if err != nil {
		return nil, err
	}
	info := append(append([]byte("WebPush: info\x00"), uaRaw...), asPub...)
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(info), 32)
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

	body := make([]byte, 0, headerSize+len(plaintext)+1+tagSize)
	body = append(body, salt...)
	body = binary.BigEndian.AppendUint32(body, recordSize)
	body = append(body, byte(len(asPub)))
	body = append(body, asPub...)
	// The last (here only) record ends in the 0x02 delimiter.
	return gcm.Seal(body, nonce, append(append([]byte{}, plaintext...), 0x02), nil), nil
}
