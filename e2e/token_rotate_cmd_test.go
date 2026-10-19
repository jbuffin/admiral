package e2e

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// u12 S3: admiral token rotate. A loopback peer needs no token, so the
// signed-out half runs from the Mac's LAN address and skips locally when it
// doesn't answer (it must run on CI).

func TestU12_S3_TokenRotateSignsEveryDeviceOut(t *testing.T) {
	w := newWorld(t)
	in := w.startLAN(t)
	old := in.Token
	oldCookie := map[string]string{"Cookie": in.cookieName() + "=" + old}

	ship := w.launch(w.order("work", "claude", "alpha", noSkill, "hello"))
	if resp, _ := get(t, in.lanURL()+"/", oldCookie); resp.StatusCode != 200 {
		t.Fatalf("the old cookie before rotating: %d, want 200", resp.StatusCode)
	}
	hdr := http.Header{"Cookie": {in.cookieName() + "=" + old}}
	dash, _, err := wsDial(t, in.lanURL(), "", hdr, "")
	if err != nil {
		t.Fatalf("dashboard websocket from the LAN with the old cookie: %v", err)
	}
	var hello struct{ Type string }
	readJSON(t, dash, &hello)
	bridge, _, err := bridgeDial(t, in.lanURL(), ship.Session, "", hdr, "")
	if err != nil {
		t.Fatalf("bridge websocket from the LAN with the old cookie: %v", err)
	}
	bridge.send(map[string]any{"type": "join", "cols": 80, "rows": 24})
	bridge.waitJoin(5*time.Second, 1)
	local, _, err := wsDial(t, in.LocalURL, "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	readJSON(t, local, &hello)

	r := w.admiral("token", "rotate", "--port", in.Port)
	w.expectExit(r, 0, "token rotate")
	var fresh string
	for _, l := range strings.Split(r.stdout, "\n") {
		if m := lanLine.FindStringSubmatch(l); m != nil {
			fresh = m[3]
		}
	}
	if fresh == "" || fresh == old {
		t.Fatalf("token rotate should print `On other devices: http://<ip>:%s/?token=<new>` with a new token, got:\n%s", in.Port, r.stdout)
	}
	b, _ := os.ReadFile(filepath.Join(w.home, "token"))
	if strings.TrimSpace(string(b)) != fresh || !tokenFormat.MatchString(fresh) {
		t.Fatalf("token file = %q, printed token = %q", b, fresh)
	}

	// The running server watches the file: no restart.
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, _ := get(t, in.lanURL()+"/", oldCookie)
		if resp.StatusCode == 401 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 5s the old cookie was still accepted (%d) by the running server", resp.StatusCode)
		}
		time.Sleep(100 * time.Millisecond)
	}
	resp, _ := get(t, in.lanURL()+"/?token="+fresh, nil)
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == in.cookieName() {
			cookie = c
		}
	}
	if resp.StatusCode != http.StatusFound || cookie == nil || cookie.Value != fresh {
		t.Fatalf("the new token should set the cookie and redirect: %d %v", resp.StatusCode, resp.Header["Set-Cookie"])
	}
	if resp, _ := get(t, in.lanURL()+"/", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value}); resp.StatusCode != 200 {
		t.Fatalf("the new cookie: %d, want 200", resp.StatusCode)
	}

	// Every non-local WebSocket of the old token is closed, dashboard and bridge.
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, err := dash.Read(ctx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("within 5s the dashboard websocket of the old token was not closed")
			}
			break
		}
	}
	bridge.waitFor(5*time.Second, "the bridge websocket of the old token to be closed", func() bool {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		return bridge.err != nil
	})
	// A local one has no token to lose.
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _, err := local.Read(ctx)
		cancel()
		if err != nil {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("the loopback websocket was closed: %v (%v)", err, websocket.CloseStatus(err))
			}
			break
		}
	}
}

func TestU12_S3_TokenRotateWithoutAServer(t *testing.T) {
	w := newWorld(t)
	tokenFile := filepath.Join(w.home, "token")
	w.writeRaw(tokenFile, "oldoldoldoldoldoldoldold\n", 0o600)

	w.expectExit(w.admiral("token", "rotate", "--port", freePort(t)), 0, "token rotate with no server running")
	b, _ := os.ReadFile(tokenFile)
	if got := strings.TrimSpace(string(b)); got == "oldoldoldoldoldoldoldold" || !tokenFormat.MatchString(got) {
		t.Fatalf("token file = %q, want a new URL-safe token", b)
	}
	if fi, err := os.Stat(tokenFile); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v (%v), want 0600", fi, err)
	}
}
