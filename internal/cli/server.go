package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// defaultPort reads ADMIRAL_PORT, which Admiral sets in the chat's environment.
func defaultPort() string {
	if p := os.Getenv("ADMIRAL_PORT"); p != "" {
		if _, err := parsePort(p); err == nil {
			return p
		}
	}
	return DefaultPort
}

// parsePort accepts only a decimal port: it is joined into a URL and AdmiralBot's
// commands take it from a model, so `1@evil.example` must never name another host.
func parsePort(s string) (int, error) {
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%q is not a port: want a number from 1 to 65535", s)
		}
	}
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("%q is not a port: want a number from 1 to 65535", s)
	}
	return p, nil
}

func loopbackURL(port string) (string, error) {
	p, err := parsePort(port)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(p))}
	return u.String(), nil
}

// serverFor fails when another Admiral's server holds the port.
func serverFor(port, dir string) (string, shipsBody, error) {
	base, err := loopbackURL(port)
	if err != nil {
		return "", shipsBody{}, err
	}
	body, ok := probe(port, dir)
	if !ok {
		return "", shipsBody{}, fmt.Errorf("no running Admiral for this home on port %s", port)
	}
	return base, body, nil
}

func getJSON(base, path string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(base + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s: %s", path, resp.Status, errorText(b))
	}
	return b, nil
}

// sendJSON is for the calls getJSON can't make: any 2xx is success.
func sendJSON(method, base, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, errorText(b))
	}
	return b, nil
}

func errorText(b []byte) string {
	var m struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &m) == nil && m.Error != "" {
		return m.Error
	}
	return string(b)
}
