package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

// A ship name is reused, so a ship never inherits the report or inbox of an
// earlier ship of that name, and the contract's variables beat the fleet's.
func TestShipContractClearsWhatANameLeft(t *testing.T) {
	home := t.TempDir()
	stale := map[string]string{
		filepath.Join(home, "reports", "dory.json"):        "old report",
		filepath.Join(home, "inbox", "dory", "msg-1.json"): "old message",
	}
	for p, body := range stale {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"ADMIRAL_SHIP": "fleet"}
	if err := (&Launcher{Home: home}).shipContract(env, "dory"); err != nil {
		t.Fatal(err)
	}
	for p := range stale {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived the launch (%v)", p, err)
		}
	}
	if st, err := os.Stat(filepath.Join(home, "inbox", "dory")); err != nil || !st.IsDir() {
		t.Errorf("the inbox was not recreated: %v", err)
	}
	if env["ADMIRAL_SHIP"] != "dory" || env["ADMIRAL_REPORT"] != filepath.Join(home, "reports", "dory.json") {
		t.Errorf("env = %v", env)
	}
}
