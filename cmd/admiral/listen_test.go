package main

import (
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
		name     string
		explicit string // the --listen value; empty when the flag is absent
		cfg      *config.Config
		want     string
	}{
		{"mode on, no flag", "", on, "127.0.0.1:7420"},
		{"mode off, no flag", "", off, "0.0.0.0:7420"},
		{"no remote section, no flag", "", absent, "0.0.0.0:7420"},
		{"mode on, --listen 0.0.0.0:7420", "0.0.0.0:7420", on, "0.0.0.0:7420"},
		{"mode on, --listen on another port", "127.0.0.1:7499", on, "127.0.0.1:7499"},
		{"mode off, --listen loopback", "127.0.0.1:7499", off, "127.0.0.1:7499"},
	} {
		if got := listenAddr(c.explicit, c.cfg); got != c.want {
			t.Errorf("%s: listen = %q, want %q", c.name, got, c.want)
		}
	}
}
