package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

func TestListenDefaultFollowsTailscaleMode(t *testing.T) {
	const fleets = `"version":1,"fleets":{"w":{"name":"W","claude":{}}}`
	parse := func(extra string) *config.Config {
		cfg, err := config.Parse([]byte(`{` + fleets + extra + `}`))
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	on := parse(`,"remote":{"tailscale":true}`)
	off := parse(`,"remote":{"tailscale":false}`)
	absent := parse(``)

	for _, c := range []struct {
		name       string
		explicit   string // the --listen value; empty when the flag is absent
		cfg        *config.Config
		unreadable bool // config.json failed to parse
		want       string
	}{
		{"mode on, no flag", "", on, false, "127.0.0.1:7420"},
		{"unreadable config, no flag", "", absent, true, "127.0.0.1:7420"},
		{"unreadable config, --listen given", "0.0.0.0:7420", absent, true, "0.0.0.0:7420"},
		{"mode off, no flag", "", off, false, "0.0.0.0:7420"},
		{"no remote section, no flag", "", absent, false, "0.0.0.0:7420"},
		{"mode on, --listen 0.0.0.0:7420", "0.0.0.0:7420", on, false, "0.0.0.0:7420"},
		{"mode on, --listen on another port", "127.0.0.1:7499", on, false, "127.0.0.1:7499"},
		{"mode off, --listen loopback", "127.0.0.1:7499", off, false, "127.0.0.1:7499"},
	} {
		if got := listenAddr(c.explicit, c.cfg, c.unreadable); got != c.want {
			t.Errorf("%s: listen = %q, want %q", c.name, got, c.want)
		}
	}
}

// Setup writes config.json while the manager is open; the start that follows binds loopback, not the LAN.
func TestFirstStartAfterSetupBindsLoopback(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	offer := func() (bool, error) {
		body := `{"version":1,"fleets":{"w":{"name":"W","claude":{}}},"remote":{"tailscale":true}}`
		return true, os.WriteFile(filepath.Join(dir, config.FileName), []byte(body), 0o600)
	}
	got, err := resolveListen(cfg, "", offer)
	if err != nil {
		t.Fatal(err)
	}
	if got != "127.0.0.1:7420" {
		t.Errorf("listen after setup turned the mode on = %q, want 127.0.0.1:7420", got)
	}
}
