package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Setup writes config.json while a Manager is open; Reload shows the new file at once, before the watcher's delay.
func TestManagerReloadSeesAFileWrittenBehindItsBack(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.Current().Remote.Tailscale {
		t.Fatal("the mode is on before anything was written")
	}
	body := `{` + remoteFleets + `,"remote":{"tailscale":true}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Reload()
	if !m.Current().Remote.Tailscale {
		t.Errorf("after Reload the mode is still off: %+v", m.Current().Remote)
	}
}
