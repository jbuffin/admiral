package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/push/pushtest"
)

const pushingCue = `event PermissionRequest {"hook_event_name":"PermissionRequest","tool_name":"Bash"}`

// pushRig is the real binary on a LAN listener, a fake push service it trusts, and three subscribers: two paired devices and This Mac.
type pushRig struct {
	t      *testing.T
	w      *world
	in     *fleetInst
	svc    *pushtest.Service
	tokens map[string]string
	subs   map[string]*pushtest.Subscriber // by endpoint path
	repo   int
}

func newPushRig(t *testing.T) *pushRig {
	t.Helper()
	svc := pushtest.NewService(t)
	w := newWorld(t)
	w.cueFleets(map[string][]string{
		"f": {"hook working", "sleep 1", pushingCue, "wait go"},
		"q": {"hook working", "sleep 1", pushingCue, "wait go"},
	})
	roots := filepath.Join(w.root, "push-roots.pem")
	if err := os.WriteFile(roots, svc.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	w.extra = append(w.extra, "ADMIRAL_PUSH_TEST_ROOTS="+roots)
	in := w.startLAN(t)
	return &pushRig{t: t, w: w, in: in, svc: svc, tokens: map[string]string{}, subs: map[string]*pushtest.Subscriber{}}
}

func (r *pushRig) put(base, token, method, path string, body any) (int, string) {
	r.t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(method, base+path, bytes.NewReader(b))
	if err != nil {
		r.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client().Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

// subscribe registers a fresh browser at the fake service as this device: a paired one by name, or This Mac over loopback.
func (r *pushRig) subscribe(device string) {
	r.t.Helper()
	sub := pushtest.NewSubscriber(r.t)
	path := "/" + device
	r.subs[path] = sub
	var body map[string]any
	_ = json.Unmarshal([]byte(sub.JSON(r.svc.Endpoint(path))), &body)
	base, token := r.in.LocalURL, ""
	if device != "mac" {
		if r.tokens[device] == "" {
			r.tokens[device] = r.in.pair(r.t)
		}
		base, token = r.in.lanURL(), r.tokens[device]
	}
	if code, out := r.put(base, token, http.MethodPut, "/api/v1/push/subscription", body); code/100 != 2 {
		r.t.Fatalf("subscribe %s: %d %s", device, code, out)
	}
}

type pushed struct {
	path    string
	payload map[string]any
	body    []byte
}

// next waits for one push and decrypts it with the subscriber the endpoint belongs to.
func (r *pushRig) next(d time.Duration) (pushed, bool) {
	r.t.Helper()
	got, ok := r.svc.Next(d)
	if !ok {
		return pushed{}, false
	}
	sub := r.subs[got.Path]
	if sub == nil {
		r.t.Fatalf("a push reached %s, which no subscriber holds", got.Path)
	}
	plain, err := sub.Decrypt(got.Body)
	if err != nil {
		r.t.Fatalf("decrypt the push to %s: %v", got.Path, err)
	}
	var p map[string]any
	if err := json.Unmarshal(plain, &p); err != nil {
		r.t.Fatalf("payload %q: %v", plain, err)
	}
	return pushed{path: got.Path, payload: p, body: got.Body}, true
}

func (r *pushRig) collect(n int) []pushed {
	r.t.Helper()
	var out []pushed
	for len(out) < n {
		p, ok := r.next(15 * time.Second)
		if !ok {
			r.t.Fatalf("got %d of %d pushes", len(out), n)
		}
		out = append(out, p)
	}
	return out
}

func (r *pushRig) launch(fleet string) shipJSON {
	r.t.Helper()
	r.repo++
	return r.w.launchIn(fleet, "claude", "push"+string(rune('a'+r.repo)))
}

func (r *pushRig) notifications(token string) map[string]bool {
	r.t.Helper()
	base, tok := r.in.LocalURL, ""
	if token != "" {
		base, tok = r.in.lanURL(), token
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/devices", nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client().Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Devices []struct {
			Name          string `json:"name"`
			Current       bool   `json:"current"`
			Notifications bool   `json:"notifications"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		r.t.Fatal(err)
	}
	byName := map[string]bool{}
	for _, d := range out.Devices {
		byName[d.Name] = d.Notifications
		if d.Current {
			byName["(current)"] = d.Notifications
		}
	}
	return byName
}

func TestPushReachesEveryDeviceEncryptedAndDecryptsToAPayloadNamingTheShip(t *testing.T) {
	t.Parallel()
	r := newPushRig(t)
	for _, d := range []string{"a", "b", "mac"} {
		r.subscribe(d)
	}
	if got := r.notifications(""); !got["This Mac"] || !got["(current)"] {
		t.Fatalf("This Mac on the Local route: %v, want a device with notifications on, marked current", got)
	}
	ship := r.launch("f")
	got := r.collect(3)

	seen := map[string]bool{}
	for _, p := range got {
		seen[p.path] = true
		if p.payload["name"] != ship.Name || p.payload["session"] != ship.Session || p.payload["fleet"] != "f" {
			t.Errorf("%s: payload %v, want ship %q session %q fleet f", p.path, p.payload, ship.Name, ship.Session)
		}
		if bytes.Contains(p.body, []byte(ship.Name)) {
			t.Errorf("%s: the body carries the ship name unencrypted", p.path)
		}
		for k := range p.payload {
			switch k {
			case "fleet", "fleet_name", "name", "line", "tag", "target", "session", "item", "crew", "merge":
			default:
				t.Errorf("%s: payload field %q is outside the payload contract", p.path, k)
			}
		}
	}
	if len(seen) != 3 {
		t.Fatalf("pushes reached %v, want a, b and mac", seen)
	}
	if code, body := send(t, http.MethodGet, r.in.LocalURL+"/api/v1/push/key", nil); code != 200 || !strings.Contains(body, `"key"`) {
		t.Fatalf("GET /api/v1/push/key: %d %s", code, body)
	}
}

func TestPushIsHeldBackWhileAWindowIsWatching(t *testing.T) {
	t.Parallel()
	r := newPushRig(t)
	for _, d := range []string{"a", "mac"} {
		r.subscribe(d)
	}
	ws, _, err := wsDialAt(t, r.in.LocalURL, "/ws/v1", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"type":"presence","visible":true,"idle_ms":0}`)); err != nil {
		t.Fatal(err)
	}
	ship := r.launch("f")
	r.in.waitStatusList(t, 15*time.Second, ship.Session, "needs_you")
	holdThat(t, "a push went out while a window was watching", func() bool { return r.svc.Count() == 0 })

	// Control: once the window stops watching, the next ship pushes, so the hold above was not a sender that was never wired.
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"type":"presence","visible":false,"idle_ms":0}`)); err != nil {
		t.Fatal(err)
	}
	next := r.launch("f")
	for _, p := range r.collect(2) {
		if p.payload["session"] != next.Session {
			t.Errorf("payload %v, want the ship launched after the window stopped watching (%s)", p.payload, next.Session)
		}
	}
}

func TestAFleetWithNotifyOffPushesNothingWhileAnotherFleetStillDoes(t *testing.T) {
	t.Parallel()
	r := newPushRig(t)
	for _, d := range []string{"a", "mac"} {
		r.subscribe(d)
	}
	if code, body := r.put(r.in.LocalURL, "", http.MethodPut, "/api/v1/fleets/q", map[string]any{"notify": false}); code != 200 {
		t.Fatalf("turn Notify off: %d %s", code, body)
	}
	quiet := r.launch("q")
	loud := r.launch("f")
	got := r.collect(2)
	for _, p := range got {
		if p.payload["session"] == quiet.Session {
			t.Fatalf("a fleet with Notify off pushed: %v", p.payload)
		}
		if p.payload["session"] != loud.Session {
			t.Errorf("payload %v, want the other fleet's ship %s", p.payload, loud.Session)
		}
	}
	holdThat(t, "a third push arrived", func() bool { return r.svc.Count() == 2 })
}

func TestA410DropsThatDevicesSubscriptionAndItsNotificationsShowOff(t *testing.T) {
	t.Parallel()
	r := newPushRig(t)
	for _, d := range []string{"a", "b", "mac"} {
		r.subscribe(d)
	}
	r.svc.SetStatus("/a", http.StatusGone)
	first := r.launch("f")
	r.collect(3)

	deadline := time.Now().Add(10 * time.Second)
	for r.notifications(r.tokens["b"])["(current)"] && time.Now().Before(deadline) {
		if got := r.notifications(r.tokens["a"]); !got["(current)"] {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if r.notifications(r.tokens["a"])["(current)"] {
		t.Fatal("device a still shows notifications on after the push service answered 410")
	}
	if !r.notifications(r.tokens["b"])["(current)"] || !r.notifications("")["(current)"] {
		t.Fatal("a 410 for device a turned another device's notifications off")
	}

	second := r.launch("f")
	got := r.collect(2)
	for _, p := range got {
		if p.path == "/a" {
			t.Fatalf("a dropped subscription got a push for %s", second.Session)
		}
		if p.payload["session"] != second.Session || p.payload["session"] == first.Session {
			t.Errorf("payload %v, want the second ship %s", p.payload, second.Session)
		}
	}
}
