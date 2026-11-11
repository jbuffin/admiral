package launcher

// Starting a Copilot ship with --ui-server: the binary gate and the launch flags.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// fakeCopilot is a binary that answers --version, and exits with code for --ui-server --port N --version; it logs every run.
func fakeCopilot(t *testing.T, dir, version string, code int) (bin, log string) {
	t.Helper()
	bin, log = filepath.Join(dir, "copilot"), filepath.Join(dir, "runs")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" +
		"if [ \"$*\" = --version ]; then echo '" + version + "'; exit 0; fi\n" +
		"if [ \"$1\" = --ui-server ] && [ \"$2\" = --port ] && [ \"$4\" = --version ]; then echo '" + version + "'; exit " + string(rune('0'+code)) + "; fi\n" +
		"echo 'unknown option' >&2; exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// gateRuns lists the runs that tried --ui-server.
func gateRuns(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.Contains(l, "--ui-server") {
			out = append(out, l)
		}
	}
	return out
}

func TestUIServerGateFollowsWhatTheBinaryAccepts(t *testing.T) {
	bin, log := fakeCopilot(t, t.TempDir(), "GitHub Copilot CLI 1.0.109", 0)
	if !uiServerSupported(bin) {
		t.Fatal("a binary that exits 0 for --ui-server --port N --version was refused")
	}
	got := gateRuns(t, log)
	if len(got) != 1 || !strings.HasPrefix(got[0], "--ui-server --port ") || !strings.HasSuffix(got[0], " --version") {
		t.Errorf("the gate ran %q, want one run of --ui-server --port <n> --version", got)
	}

	old, _ := fakeCopilot(t, t.TempDir(), "GitHub Copilot CLI 1.0.100", 1)
	if uiServerSupported(old) {
		t.Error("a binary that exits 1 was accepted")
	}
	if uiServerSupported(filepath.Join(t.TempDir(), "missing")) {
		t.Error("a missing binary was accepted")
	}
}

func TestUIServerGateAsksOncePerBinaryAndVersion(t *testing.T) {
	dir := t.TempDir()
	bin, log := fakeCopilot(t, dir, "GitHub Copilot CLI 1.0.109", 0)
	for i := 0; i < 3; i++ {
		if !uiServerSupported(bin) {
			t.Fatalf("ask %d: refused", i)
		}
	}
	if got := gateRuns(t, log); len(got) != 1 {
		t.Errorf("three launches ran the gate %d times, want once", len(got))
	}
	// An upgraded binary at the same path is asked again.
	fakeCopilot(t, dir, "GitHub Copilot CLI 1.0.120 with a longer banner", 1)
	if uiServerSupported(bin) {
		t.Error("the upgraded binary was answered from the old binary's cache")
	}
}

func TestUIServerFlags(t *testing.T) {
	const port = 54321
	for _, c := range []struct {
		name    string
		harness string
		dock    []string
		extra   []string
		want    bool
	}{
		{"a plain copilot launch", "copilot", nil, nil, true},
		{"an order after --", "copilot", nil, []string{"--", "--port", "1"}, true},
		{"claude has no ui server", "claude", nil, nil, false},
		{"--server", "copilot", nil, []string{"--server"}, false},
		{"--headless", "copilot", nil, []string{"--headless"}, false},
		{"--stdio", "copilot", nil, []string{"--stdio"}, false},
		{"--connect", "copilot", []string{"--connect"}, nil, false},
		{"--host", "copilot", nil, []string{"--host", "0.0.0.0"}, false},
		{"--host=x", "copilot", nil, []string{"--host=0.0.0.0"}, false},
		{"--port", "copilot", nil, []string{"--port", "9000"}, false},
		{"--port=9000", "copilot", []string{"--port=9000"}, nil, false},
		{"--ui-server already", "copilot", nil, []string{"--ui-server"}, false},
		{"--resume names a session", "copilot", nil, []string{"--resume", "abc1234"}, false},
		{"--session-id names a session", "copilot", []string{"--session-id", "x"}, nil, false},
		{"--continue", "copilot", nil, []string{"--continue"}, false},
	} {
		got := uiServerFlags(Request{Harness: c.harness, Dir: "/work/a", ExtraArgs: c.extra}, config.Harness{Args: c.dock}, port)
		if !c.want {
			if got != nil {
				t.Errorf("%s: flags = %q, want none", c.name, got)
			}
			continue
		}
		if want := []string{"--ui-server", "--port", "54321"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: flags = %q, want %q", c.name, got, want)
		}
		for _, f := range got {
			if strings.HasPrefix(f, "--host") {
				t.Errorf("%s: flags carry %q; the server must stay on loopback", c.name, f)
			}
		}
	}
}
