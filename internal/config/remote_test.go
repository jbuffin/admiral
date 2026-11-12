package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const remoteFleets = `"version":1,"fleets":{"w":{"name":"W","claude":{}}}`

func TestParseRemote(t *testing.T) {
	cfg, err := Parse([]byte(`{` + remoteFleets + `}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Remote.Tailscale || len(cfg.Remote.AllowedLogins) != 0 {
		t.Errorf("a config with no remote section: %+v, want the mode off and no logins", cfg.Remote)
	}

	cfg, err = Parse([]byte(`{` + remoteFleets + `,"remote":{"tailscale":true,"allowedLogins":["alice@example.com","bob@example.com"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Remote.Tailscale || !slices.Equal(cfg.Remote.AllowedLogins, []string{"alice@example.com", "bob@example.com"}) {
		t.Errorf("remote = %+v", cfg.Remote)
	}
}

func TestSetRemoteWritesTheSectionAndLeavesTheRest(t *testing.T) {
	const orig = `{` + remoteFleets + `,"max_ships":7}`
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	want := Remote{Tailscale: true, AllowedLogins: []string{"alice@example.com"}}
	if err := m.SetRemote(want); err != nil {
		t.Fatal(err)
	}
	cur := m.Current()
	if !cur.Remote.Tailscale || !slices.Equal(cur.Remote.AllowedLogins, want.AllowedLogins) {
		t.Errorf("Current after SetRemote = %+v, want %+v", cur.Remote, want)
	}
	if cur.MaxShips != 7 || len(cur.Fleets) != 1 {
		t.Errorf("SetRemote changed other settings: max_ships %d, %d fleets", cur.MaxShips, len(cur.Fleets))
	}

	var onDisk struct {
		Remote map[string]json.RawMessage `json:"remote"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &onDisk); err != nil {
		t.Fatal(err)
	}
	var logins []string
	_ = json.Unmarshal(onDisk.Remote["allowedLogins"], &logins)
	if string(onDisk.Remote["tailscale"]) != "true" || !slices.Equal(logins, []string{"alice@example.com"}) {
		t.Errorf("remote in config.json = %s, want tailscale and camelCase allowedLogins", b)
	}
}

func TestCurrentDoesNotShareTheLoginList(t *testing.T) {
	dir := t.TempDir()
	const body = `{` + remoteFleets + `,"remote":{"tailscale":true,"allowedLogins":["alice@example.com"]}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	m.Current().Remote.AllowedLogins[0] = "mallory@example.com"
	if got := m.Current().Remote.AllowedLogins; !slices.Equal(got, []string{"alice@example.com"}) {
		t.Errorf("a caller's edit of its copy reached the manager: %v", got)
	}
}
