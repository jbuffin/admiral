package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// defaultPort is where a command looks for the server when it has no --port:
// ADMIRAL_PORT, which Admiral sets in the chat's environment, else 7420.
func defaultPort() string {
	if p := os.Getenv("ADMIRAL_PORT"); p != "" {
		return p
	}
	return DefaultPort
}

// serverFor finds the server on port when it serves this home (see probe), and
// returns its base URL and its ships. Another Admiral holding the port, or none,
// is an error saying so.
func serverFor(port, dir string) (string, shipsBody, error) {
	body, ok := probe(port, dir)
	if !ok {
		return "", shipsBody{}, fmt.Errorf("no running Admiral for this home on port %s", port)
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port), body, nil
}

// getJSON reads a path of the server, which must answer 200.
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

// errorText is the `error` field of an error body, else the body.
func errorText(b []byte) string {
	var m struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &m) == nil && m.Error != "" {
		return m.Error
	}
	return string(b)
}
