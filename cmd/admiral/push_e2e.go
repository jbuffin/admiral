//go:build e2e

package main

import (
	"crypto/x509"
	"errors"
	"net/http"
	"os"

	"github.com/jbuffin/admiral/internal/push"
)

// pushClient is the test build's way to a fake push service: ADMIRAL_PUSH_TEST_ROOTS names a PEM file whose
// certificates the sender trusts, and the refusal of local destinations is lifted. A release is built without this file.
func pushClient() (*http.Client, error) {
	path := os.Getenv("ADMIRAL_PUSH_TEST_ROOTS")
	if path == "" {
		return push.SafeClient(nil, false), nil
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("ADMIRAL_PUSH_TEST_ROOTS holds no certificate")
	}
	return push.SafeClient(pool, true), nil
}
