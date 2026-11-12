package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Settings' two routes: the allowed-logins edit and Set up, against a fake tailscale CLI.

const (
	bobLogin = "bob@example.com"
	carol    = "carol@example.com"
)

type setupAnswer struct {
	OK     bool `json:"ok"`
	Checks []struct {
		ID     string `json:"id"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	} `json:"checks"`
}

// asTailnet is a request as tailscale serve forwards it: loopback peer, the user's login and the device's token.
func (p *tsProc) asTailnet(t *testing.T, method, path, login, token, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, p.base()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Tailscale-User-Login", login)
	req.Header.Set("X-Forwarded-For", "100.64.0.9")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := tsClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (p *tsProc) post(t *testing.T, path, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, p.base()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := tsClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// pairOverTailnet mints a code locally and redeems it as a tailnet device of the login.
func (p *tsProc) pairOverTailnet(t *testing.T, login string) string {
	t.Helper()
	code, body := p.call(t, http.MethodPost, "/api/v1/pairing-codes")
	var minted struct{ Code string }
	if code != http.StatusOK || json.Unmarshal(body, &minted) != nil {
		t.Fatalf("mint: %d %s", code, body)
	}
	code, body = p.asTailnet(t, http.MethodPost, "/api/v1/pair", login, "", `{"code":"`+minted.Code+`","name":"tail"}`)
	var paired struct{ Token string }
	if code != http.StatusOK || json.Unmarshal(body, &paired) != nil || paired.Token == "" {
		t.Fatalf("redeem over the tailnet: %d %s", code, body)
	}
	return paired.Token
}

func (r *tsRig) savedRemote() (mode bool, logins []string) {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.home, "config.json"))
	if err != nil {
		r.t.Fatal(err)
	}
	var cfg struct {
		Remote struct {
			Tailscale     bool     `json:"tailscale"`
			AllowedLogins []string `json:"allowedLogins"`
		} `json:"remote"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		r.t.Fatalf("config.json: %v", err)
	}
	return cfg.Remote.Tailscale, cfg.Remote.AllowedLogins
}

func (r *tsRig) serveCalls() []string {
	var out []string
	for _, l := range strings.Split(r.calls(), "\n") {
		if strings.HasPrefix(l, "serve --bg") {
			out = append(out, l)
		}
	}
	return out
}

// An edit is trimmed, refused when blank or already listed, and answers with the server's list; a removal leaves the
// others, edits made at once all land, and the file and GET /api/remote agree. /api/v1 and /api carry both routes.
func TestRemoteLoginsEditGoesThroughTheConfig(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	r.logins = []string{tsLogin, bobLogin}
	r.writeConfig(true)
	p := r.start(true, false)
	p.otherDevices(t)

	edit := func(body string) (int, remoteView) {
		code, raw := p.post(t, "/api/v1/remote/logins", body)
		var v remoteView
		_ = json.Unmarshal(raw, &v)
		return code, v
	}

	code, v := edit(`{"add":"  ` + carol + ` "}`)
	if code != http.StatusOK || !reflect.DeepEqual(v.AllowedLogins, []string{tsLogin, bobLogin, carol}) {
		t.Fatalf("add carol (padded): %d %+v, want 200 and the three logins in order", code, v)
	}
	code, v = edit(`{"remove":"` + bobLogin + `"}`)
	if code != http.StatusOK || !reflect.DeepEqual(v.AllowedLogins, []string{tsLogin, carol}) {
		t.Fatalf("remove bob: %d %+v, want 200 and the others kept", code, v)
	}
	if mode, got := r.savedRemote(); !mode || !reflect.DeepEqual(got, []string{tsLogin, carol}) {
		t.Errorf("config.json remote = %v %v, want the mode still on and alice and carol", mode, got)
	}
	if got := p.remote(t, "/api/remote").AllowedLogins; !reflect.DeepEqual(got, []string{tsLogin, carol}) {
		t.Errorf("GET /api/remote logins %v, want alice and carol", got)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if code, _ := p.post(t, "/api/remote/logins", fmt.Sprintf(`{"add":"user%d@example.com"}`, i)); code != http.StatusOK {
				t.Errorf("concurrent add %d: %d", i, code)
			}
		}(i)
	}
	wg.Wait()
	if _, got := r.savedRemote(); len(got) != 2+8 {
		t.Errorf("after eight concurrent adds the file holds %d logins, want 10: %v", len(got), got)
	}
}

// Removing a login ends that login's open tailnet socket, and the same device's next request is refused.
func TestRemovingALoginCutsItsSocket(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	r.logins = []string{tsLogin, bobLogin}
	r.writeConfig(true)
	p := r.start(true, false)
	p.otherDevices(t)
	token := p.pairOverTailnet(t, bobLogin)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+p.Port+"/ws/v1", &websocket.DialOptions{HTTPHeader: http.Header{
		"Authorization": {"Bearer " + token}, "Tailscale-User-Login": {bobLogin}, "X-Forwarded-For": {"100.64.0.9"}}})
	if err != nil {
		t.Fatalf("dial as bob: %v", err)
	}
	defer conn.CloseNow()

	if code, body := p.post(t, "/api/v1/remote/logins", `{"remove":"`+bobLogin+`"}`); code != http.StatusOK {
		t.Fatalf("remove bob: %d %s", code, body)
	}
	readCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	for {
		if _, _, err := conn.Read(readCtx); err != nil {
			if readCtx.Err() != nil {
				t.Fatalf("bob's socket stayed open after his login was removed")
			}
			break
		}
	}
	if code, _ := p.asTailnet(t, http.MethodGet, "/api/v1/devices", bobLogin, token, ""); code != http.StatusUnauthorized {
		t.Errorf("bob's next request: %d, want 401", code)
	}
}

func (p *tsProc) setup(t *testing.T, body string) (int, setupAnswer) {
	t.Helper()
	code, raw := p.post(t, "/api/v1/remote/setup", body)
	var a setupAnswer
	_ = json.Unmarshal(raw, &a)
	return code, a
}

// Set up asks for confirmation first: without it nothing runs and nothing is written. Confirmed, it runs serve once with
// Admiral's own port, turns the mode on, adds the Mac's login beside the others, and the page's next read shows the checks.
func TestRemoteSetupTurnsTheModeOn(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	r.writeServe(map[string]any{})
	r.logins = []string{carol}
	r.writeConfig(false)
	p := r.start(false, false)
	p.otherDevices(t)

	code, a := p.setup(t, `{"confirm":true}`)
	if code != http.StatusOK || !a.OK || len(a.Checks) < 3 {
		t.Fatalf("Set up: %d %+v, want 200, ok and a row per check", code, a)
	}
	for _, c := range a.Checks {
		if !c.OK || c.Detail == "" {
			t.Errorf("row %+v, want passed with a detail", c)
		}
	}
	want := "serve --bg --https=443 http://127.0.0.1:" + r.port
	if got := r.serveCalls(); len(got) != 1 || got[0] != want {
		t.Errorf("serve calls %q, want exactly [%q]: Admiral's live port, not the default", got, want)
	}
	if mode, got := r.savedRemote(); !mode || !reflect.DeepEqual(got, []string{carol, tsLogin}) {
		t.Errorf("config.json remote = %v %v, want the mode on, carol kept and the Mac's login added", mode, got)
	}
	v := p.remote(t, "/api/remote")
	if !v.Tailscale || v.URL != "https://"+tsName+"/" || len(v.Checks) != 4 || len(v.failed()) != 0 {
		t.Errorf("GET /api/remote after Set up = %+v, want the mode on, the URL and four ok checks", v)
	}
}

// A failed check stops Set up with its row and writes nothing; so does 443 held by something else, with the 8443 command;
// a serve failure shows the CLI's message. Already serving Admiral is left alone and still turns the mode on.
func TestRemoteSetupStopsAndWritesNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(r *tsRig)
		row   string // text one failed row's detail holds, lowercased
		serve int    // serve --bg calls expected
	}{
		{"logged out", func(r *tsRig) {
			r.writeStatus(tsStatus(func(m map[string]any) { m["BackendState"] = "NeedsLogin" }))
		}, "logged out", 0},
		{"https off", func(r *tsRig) {
			r.writeStatus(tsStatus(func(m map[string]any) { m["CertDomains"] = []string{} }))
		}, "https", 0},
		{"funnel on", func(r *tsRig) { r.writeServe(r.serve(8443, true)) }, "funnel", 0},
		{"443 held", func(r *tsRig) {
			r.writeServe(map[string]any{
				"TCP": map[string]any{"443": map[string]any{"HTTPS": true}},
				"Web": map[string]any{tsHostPort(443): map[string]any{"Handlers": map[string]any{"/": map[string]any{"Proxy": "http://127.0.0.1:9999"}}}},
			})
		}, "tailscale serve --bg --https=8443 http://127.0.0.1:", 0},
		{"serve fails", func(r *tsRig) {
			r.writeServe(map[string]any{})
			r.put("servebg.exit", "1")
			r.put("servebg.err", "Serve is not enabled on your tailnet.\n")
		}, "serve is not enabled on your tailnet", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newTSRig(t)
			r.writeServe(map[string]any{})
			c.setup(r)
			r.logins = []string{carol}
			r.writeConfig(false)
			p := r.start(false, false)
			p.otherDevices(t)
			code, a := p.setup(t, `{"confirm":true}`)
			if code != http.StatusOK || a.OK {
				t.Fatalf("Set up: %d %+v, want 200 with ok false", code, a)
			}
			found := false
			for _, row := range a.Checks {
				if !row.OK && strings.Contains(strings.ToLower(row.Detail), c.row) {
					found = true
				}
			}
			if !found {
				t.Errorf("rows %+v, want a failed one mentioning %q", a.Checks, c.row)
			}
			if got := r.serveCalls(); len(got) != c.serve {
				t.Errorf("serve calls %q, want %d", got, c.serve)
			}
			if mode, got := r.savedRemote(); mode || !reflect.DeepEqual(got, []string{carol}) {
				t.Errorf("config.json remote = %v %v, want nothing written", mode, got)
			}
		})
	}

	t.Run("already serving", func(t *testing.T) {
		t.Parallel()
		r := newTSRig(t)
		r.logins = []string{carol}
		r.writeConfig(false)
		p := r.start(false, false) // newTSRig's serve config already proxies 443 to this Admiral
		p.otherDevices(t)
		code, a := p.setup(t, `{"confirm":true}`)
		if code != http.StatusOK || !a.OK {
			t.Fatalf("Set up: %d %+v, want 200 and ok", code, a)
		}
		if got := r.serveCalls(); len(got) != 0 {
			t.Errorf("serve ran although it already proxies to Admiral: %q", got)
		}
		if mode, got := r.savedRemote(); !mode || !reflect.DeepEqual(got, []string{carol, tsLogin}) {
			t.Errorf("config.json remote = %v %v, want the mode on and the Mac's login added", mode, got)
		}
	})
}

// Two Set ups at once run serve once.
func TestRemoteSetupDoubleClickRunsOnce(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	r.writeServe(map[string]any{})
	r.put("servebg.sleep", "1")
	p := r.start(false, false)
	p.otherDevices(t)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.setup(t, `{"confirm":true}`)
		}()
	}
	wg.Wait()
	if got := r.serveCalls(); len(got) != 1 {
		t.Errorf("serve ran %d times for two clicks: %q", len(got), got)
	}
}

// Set up runs a command on the Mac, so only a request from the Mac may run it: a paired tailnet device is refused and
// nothing runs.
func TestRemoteSetupIsLocalOnly(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	r.writeServe(map[string]any{})
	r.logins = []string{tsLogin}
	r.writeConfig(true)
	p := r.start(true, false)
	p.otherDevices(t)
	token := p.pairOverTailnet(t, tsLogin)
	code, body := p.asTailnet(t, http.MethodPost, "/api/v1/remote/setup", tsLogin, token, `{"confirm":true}`)
	if code != http.StatusForbidden {
		t.Errorf("Set up from a tailnet device: %d %s, want 403", code, bytes.TrimSpace(body))
	}
	if got := r.serveCalls(); len(got) != 0 {
		t.Errorf("serve ran for a tailnet request: %q", got)
	}
}
