//go:build !e2e

package main

import (
	"net/http"

	"github.com/jbuffin/admiral/internal/push"
)

func pushClient() (*http.Client, error) { return push.SafeClient(nil, false), nil }
