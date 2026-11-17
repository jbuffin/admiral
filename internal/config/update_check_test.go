package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateCheckIsOnUnlessTheFileTurnsItOff(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       bool
	}{
		{"absent", `{` + remoteFleets + `}`, true},
		{"true", `{` + remoteFleets + `,"update_check":true}`, true},
		{"false", `{` + remoteFleets + `,"update_check":false}`, false},
	} {
		cfg, err := Parse([]byte(c.body))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if cfg.UpdateCheck != c.want {
			t.Errorf("%s: UpdateCheck = %v, want %v", c.name, cfg.UpdateCheck, c.want)
		}
	}
}

func TestUpdateCheckThatIsNotABooleanIsRefusedByName(t *testing.T) {
	for _, v := range []string{`"no"`, `0`, `null`, `{}`} {
		_, err := Parse([]byte(`{` + remoteFleets + `,"update_check":` + v + `}`))
		if err == nil || !strings.Contains(err.Error(), "update_check") {
			t.Errorf("update_check %s: error %v, want one naming update_check", v, err)
		}
	}
}

func TestAnEditedUpdateCheckShowsInTheLiveConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{` + remoteFleets + `,"update_check":false}`)
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.Current().UpdateCheck {
		t.Fatal("update_check false in the file, on in the loaded config")
	}
	write(`{` + remoteFleets + `}`)
	m.Reload()
	if !m.Current().UpdateCheck {
		t.Error("after the key was removed the check is still off")
	}
}
