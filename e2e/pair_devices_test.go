package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// admiralCLI runs bin/admiral as a command against this instance's home and port.
func (in *instance) admiralCLI(t *testing.T, args ...string) cliResult {
	t.Helper()
	return runAdmiralCLI(t, in.Home, in.Port, args...)
}

func runAdmiralCLI(t *testing.T, home, port string, args ...string) cliResult {
	t.Helper()
	ctx := testCtx(t)
	cmd := exec.CommandContext(ctx, binaryPath(t), args...)
	cmd.Env = append(hermeticEnv(t), "ADMIRAL_HOME="+home, "ADMIRAL_PORT="+port)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	res := cliResult{}
	err := cmd.Run()
	res.stdout, res.stderr = out.String(), errb.String()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee) && ctx.Err() == nil:
		res.code = ee.ExitCode()
	default:
		failRequest(t, fmt.Sprintf("admiral %v (stdout %q stderr %q)", args, res.stdout, res.stderr), err)
	}
	return res
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// A device paired with the code `admiral pair` prints reaches the API and the page socket with its Bearer, shows in
// `admiral devices`, and after `admiral devices revoke <name>` is refused and its open socket closes.
func TestPairedDeviceWorksUntilAdmiralDevicesRevokesIt(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	exe := quote(filepath.Join(fakeDir(t), "claude"))
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"version":1,"fleets":{"alpha":{"name":"Alpha","search_roots":["~/code"],"claude":{"executable":`+exe+`}},"bravo":{"name":"Bravo","search_roots":["~/code"],"claude":{"executable":`+exe+`,"env":{"CLAUDE_CONFIG_DIR":"~/.claude-b"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := startAdmiral(t, home, "0.0.0.0:0")
	requireLAN(t, in)
	const name = "fake-tablet"

	pair := in.admiralCLI(t, "pair")
	if pair.code != 0 {
		t.Fatalf("admiral pair exited %d: %s", pair.code, pair.both())
	}
	code := regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4}\b`).FindString(pair.stdout)
	if code == "" || !strings.Contains(pair.stdout, "/pair?code="+code) {
		t.Fatalf("admiral pair must print the code and its URL, got %q", pair.stdout)
	}

	req, _ := http.NewRequest(http.MethodPost, in.lanURL()+"/api/v1/pair", strings.NewReader(`{"code":"`+code+`","name":"`+name+`"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var paired struct {
		Token  string
		Device struct{ ID, Name string }
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&paired) != nil || paired.Token == "" {
		t.Fatalf("redeeming the code from the LAN address = %d, want 200 with a token", resp.StatusCode)
	}

	if r, body := get(t, in.lanURL()+"/api/v1/ships", bearer(paired.Token)); r.StatusCode != 200 {
		t.Fatalf("Bearer on an API route = %d %s, want 200", r.StatusCode, body)
	}
	setDefault := func(fleet string) int {
		req, _ := http.NewRequest(http.MethodPut, in.lanURL()+"/api/v1/default-fleet", strings.NewReader(`{"fleet":"`+fleet+`"}`))
		req.Header.Set("Authorization", "Bearer "+paired.Token)
		req.Header.Set("Content-Type", "application/json")
		pr, err := client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		pr.Body.Close()
		return pr.StatusCode
	}
	if code := setDefault("nope"); code != http.StatusUnprocessableEntity {
		t.Fatalf("a paired device's PUT /api/v1/default-fleet naming no fleet = %d, want 422", code)
	}
	if code := setDefault("bravo"); code != http.StatusOK {
		t.Fatalf("a paired device's PUT /api/v1/default-fleet = %d, want 200", code)
	}
	if r, body := get(t, in.lanURL()+"/api/v1/config", bearer(paired.Token)); r.StatusCode != 200 || !strings.Contains(body, `"default_fleet":"bravo"`) {
		t.Fatalf("GET /api/v1/config after the paired device set bravo = %d %s", r.StatusCode, body)
	}
	ws, wr, err := wsDialAt(t, in.lanURL(), "/ws/v1", "", http.Header{"Authorization": {"Bearer " + paired.Token}}, "")
	if err != nil {
		t.Fatalf("Bearer on /ws/v1: %v (response %v)", err, wr)
	}
	var hello struct{ Type string }
	readJSON(t, ws, &hello)
	if hello.Type != "hello" {
		t.Fatalf("first socket message = %+v, want hello", hello)
	}

	list := in.admiralCLI(t, "devices")
	var row []string
	for _, l := range strings.Split(strings.TrimSpace(list.stdout), "\n") {
		if cols := strings.Split(l, "\t"); len(cols) == 5 && cols[0] == name {
			row = cols
		}
	}
	if list.code != 0 || row == nil || row[1] != "" || row[4] != paired.Device.ID {
		t.Fatalf("admiral devices (exit %d) lacks the LAN-paired %q with an empty login and id %s:\n%s", list.code, name, paired.Device.ID, list.stdout)
	}

	revoke := in.admiralCLI(t, "devices", "revoke", name)
	if revoke.code != 0 {
		t.Fatalf("admiral devices revoke %s exited %d: %s", name, revoke.code, revoke.both())
	}
	for _, out := range []string{pair.both(), list.both(), revoke.both()} {
		if strings.Contains(out, paired.Token) {
			t.Fatal("the CLI printed a device token")
		}
	}

	holdThat(t, "the revoked token is refused", func() bool {
		r, _ := get(t, in.lanURL()+"/api/v1/ships", bearer(paired.Token))
		return r.StatusCode == http.StatusUnauthorized
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("the revoked device's socket did not close within 5s")
			}
			break
		}
	}
	if r, body := get(t, in.lanURL()+"/api/v1/ships", bearer(in.Token)); r.StatusCode != 200 {
		t.Errorf("another paired device after the revoke = %d %s, want 200", r.StatusCode, body)
	}
	if after := in.admiralCLI(t, "devices"); strings.Contains(after.stdout, name) {
		t.Errorf("admiral devices still lists the revoked device:\n%s", after.stdout)
	}
}

// Exit codes come out of the real binary: 1 with no server for this home, 2 for a malformed command.
func TestPairAndDevicesExitCodesThroughTheBinary(t *testing.T) {
	t.Parallel()
	home, port := newHome(t), freePort(t)
	for _, args := range [][]string{{"pair"}, {"devices"}, {"devices", "revoke", "x"}} {
		r := runAdmiralCLI(t, home, port, args...)
		if r.code != 1 || !strings.Contains(r.stderr, "no running Admiral for this home") {
			t.Errorf("admiral %v with no server = exit %d %q, want 1 and the no-running-Admiral message", args, r.code, r.stderr)
		}
	}
	for _, args := range [][]string{{"devices", "revoke"}, {"devices", "rename", "only-one"}, {"pair", "extra"}} {
		if r := runAdmiralCLI(t, home, port, args...); r.code != 2 {
			t.Errorf("admiral %v = exit %d %q, want 2", args, r.code, r.stderr)
		}
	}
}
