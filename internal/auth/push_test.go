package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/auth"
	"github.com/jbuffin/admiral/internal/push/pushtest"
)

const vapidPublic = "BFake-public-key-for-the-key-route"

func pushRigAt(t *testing.T, home string, clk *clock) *rig {
	t.Helper()
	rc := &remoteCfg{}
	a, err := auth.New(home, "7420", auth.WithClock(clk.Now), auth.WithBaseURL(func() string { return "http://192.168.1.10:7420" }),
		auth.WithRemote(rc.get), auth.WithPushKey(func() string { return vapidPublic }))
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	mux := http.NewServeMux()
	a.Routes(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("next")) })
	return &rig{t: t, home: home, clk: clk, a: a, h: a.Middleware(mux), remote: rc}
}

func pushRig(t *testing.T) *rig {
	return pushRigAt(t, t.TempDir(), &clock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)})
}

type pushDevice struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Current       bool   `json:"current"`
	Notifications bool   `json:"notifications"`
}

func (g *rig) devices(r req) []pushDevice {
	g.t.Helper()
	r.target = "/api/devices"
	rec := g.do(r)
	var out struct {
		Devices []pushDevice `json:"devices"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		g.t.Fatalf("list devices: %d %s", rec.Code, rec.Body)
	}
	return out.Devices
}

func (g *rig) deviceNamed(r req, name string) (pushDevice, bool) {
	for _, d := range g.devices(r) {
		if d.Name == name {
			return d, true
		}
	}
	return pushDevice{}, false
}

func (g *rig) lanPush(token, method, body string) *httptest.ResponseRecorder {
	g.t.Helper()
	h := bearer(token)
	h["Content-Type"] = "application/json"
	r := lanReq(h)
	r.method, r.target, r.body = method, "/api/push/subscription", body
	return g.do(r)
}

func (g *rig) localPush(method, body string) *httptest.ResponseRecorder {
	g.t.Helper()
	return g.local(method, "/api/push/subscription", body)
}

func endpointBody(e string) string { b, _ := json.Marshal(map[string]string{"endpoint": e}); return string(b) }

func ok(rec *httptest.ResponseRecorder) bool { return rec.Code >= 200 && rec.Code < 300 }

func (g *rig) subsOf(endpoint string) (devices []string) {
	for _, s := range g.a.Subscriptions() {
		if s.Endpoint == endpoint {
			devices = append(devices, s.Device)
		}
	}
	return devices
}

func TestPushKeyRouteAnswersTheVAPIDPublicKey(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	token := g.pairLAN()
	r := lanReq(bearer(token))
	r.target = "/api/push/key"
	rec := g.do(r)
	var out struct {
		Key string `json:"key"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Key != vapidPublic {
		t.Fatalf("GET /api/push/key: %d %s, want {\"key\":%q}", rec.Code, rec.Body, vapidPublic)
	}
}

func TestPushRoutesNeedADeviceOrTheLocalRoute(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	sub := pushtest.NewSubscriber(t).JSON("https://push.example/a")
	r := lanReq(map[string]string{"Content-Type": "application/json"})
	r.method, r.target, r.body = "PUT", "/api/push/subscription", sub
	if rec := g.do(r); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an unpaired LAN client subscribing: %d, want 401", rec.Code)
	}
}

func TestSubscribeStoresOnTheDeviceAndTheListSaysNotificationsOn(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	token := g.pairLAN()
	if d := g.devices(lanReq(bearer(token))); len(d) != 1 || d[0].Notifications {
		t.Fatalf("before subscribing: %+v, want one device with notifications off", d)
	}
	if rec := g.lanPush(token, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/a")); !ok(rec) {
		t.Fatalf("subscribe: %d %s", rec.Code, rec.Body)
	}
	if d := g.devices(lanReq(bearer(token))); len(d) != 1 || !d[0].Notifications {
		t.Fatalf("after subscribing: %+v, want notifications on", d)
	}
}

func TestSubscribeRefusesAnEndpointThatIsNotHTTPS(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	token := g.pairLAN()
	for _, e := range []string{"http://push.example/a", "ftp://push.example/a", "push.example/a", ""} {
		if rec := g.lanPush(token, "PUT", pushtest.NewSubscriber(t).JSON(e)); rec.Code != http.StatusBadRequest {
			t.Errorf("endpoint %q: %d, want 400", e, rec.Code)
		}
	}
	if n := len(g.a.Subscriptions()); n != 0 {
		t.Fatalf("%d subscriptions stored after refusals", n)
	}
}

func TestSubscribeAcceptsALoopbackHTTPSEndpointBecauseTheDialRefusesIt(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	token := g.pairLAN()
	if rec := g.lanPush(token, "PUT", pushtest.NewSubscriber(t).JSON("https://127.0.0.1:9/a")); !ok(rec) {
		t.Fatalf("an https endpoint on loopback: %d %s, want accepted (the policy is enforced at dial time)", rec.Code, rec.Body)
	}
}

func TestSubscribeRefusesKeysThatAreNotAP256PointAndAnAuthSecret(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	token := g.pairLAN()
	good := pushtest.NewSubscriber(t)
	for name, body := range map[string]string{
		"no keys":      `{"endpoint":"https://push.example/a"}`,
		"short p256dh": `{"endpoint":"https://push.example/a","keys":{"p256dh":"AAAA","auth":"` + good.Auth() + `"}}`,
		"no auth":      `{"endpoint":"https://push.example/a","keys":{"p256dh":"` + good.P256dh() + `","auth":""}}`,
		"not json":     `nope`,
	} {
		if rec := g.lanPush(token, "PUT", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, rec.Code)
		}
	}
}

func TestSubscribeUpsertsByEndpointAndMovesItToTheLatestDevice(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	a, b := g.pairLAN(), g.pairLAN()
	ep := "https://push.example/shared"
	for i := 0; i < 2; i++ {
		if rec := g.lanPush(a, "PUT", pushtest.NewSubscriber(t).JSON(ep)); !ok(rec) {
			t.Fatalf("subscribe: %d %s", rec.Code, rec.Body)
		}
	}
	if got := g.subsOf(ep); len(got) != 1 {
		t.Fatalf("the same endpoint twice: held by %v, want once", got)
	}
	first := g.subsOf(ep)[0]
	if rec := g.lanPush(b, "PUT", pushtest.NewSubscriber(t).JSON(ep)); !ok(rec) {
		t.Fatalf("subscribe on a second device: %d %s", rec.Code, rec.Body)
	}
	got := g.subsOf(ep)
	if len(got) != 1 || got[0] == first {
		t.Fatalf("after the second device took it: held by %v (first was %s), want only the second device", got, first)
	}
}

func TestSubscribeKeepsTheLatestKeysOnAnUpsert(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	token := g.pairLAN()
	ep := "https://push.example/rotating"
	g.lanPush(token, "PUT", pushtest.NewSubscriber(t).JSON(ep))
	latest := pushtest.NewSubscriber(t)
	g.lanPush(token, "PUT", latest.JSON(ep))
	for _, s := range g.a.Subscriptions() {
		if s.Endpoint == ep && (s.P256dh != latest.P256dh() || s.Auth != latest.Auth()) {
			t.Fatalf("stored keys %q/%q, want the latest %q/%q", s.P256dh, s.Auth, latest.P256dh(), latest.Auth())
		}
	}
}

func TestOneDeviceHoldsSeveralSubscriptions(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	token := g.pairLAN()
	g.lanPush(token, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/safari"))
	g.lanPush(token, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/chrome"))
	if n := len(g.a.Subscriptions()); n != 2 {
		t.Fatalf("%d subscriptions, want 2 on one device", n)
	}
}

func TestUnsubscribeRemovesOnlyThatEndpointFromTheRequestingDevice(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	a, b := g.pairLAN(), g.pairLAN()
	g.lanPush(a, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/one"))
	g.lanPush(a, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/two"))
	g.lanPush(b, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/three"))

	if rec := g.lanPush(b, "DELETE", endpointBody("https://push.example/one")); !ok(rec) {
		t.Fatalf("a device removing an endpoint it does not hold: %d, want a no-op success", rec.Code)
	}
	if n := len(g.subsOf("https://push.example/one")); n != 1 {
		t.Fatalf("device b removed device a's endpoint")
	}
	if rec := g.lanPush(a, "DELETE", endpointBody("https://push.example/one")); !ok(rec) {
		t.Fatalf("unsubscribe: %d %s", rec.Code, rec.Body)
	}
	if n := len(g.subsOf("https://push.example/one")); n != 0 {
		t.Fatal("the endpoint is still stored")
	}
	if n := len(g.a.Subscriptions()); n != 2 {
		t.Fatalf("%d subscriptions left, want the other two", n)
	}
}

func TestDroppingASubscriptionTurnsTheDeviceNotificationsOff(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	token := g.pairLAN()
	g.lanPush(token, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/a"))
	subs := g.a.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("%d subscriptions", len(subs))
	}
	g.a.DropSubscription(subs[0].Device, subs[0].Endpoint)
	if d := g.devices(lanReq(bearer(token))); d[0].Notifications {
		t.Fatalf("notifications still on after the sender dropped the endpoint: %+v", d)
	}
}

func TestRevokingADeviceDropsItsSubscriptions(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	a, b := g.pairLAN(), g.pairLAN()
	g.lanPush(a, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/a"))
	g.lanPush(b, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/b"))
	var aID string
	for _, d := range g.devices(lanReq(bearer(a))) {
		if d.Current {
			aID = d.ID
		}
	}
	if rec := g.local("DELETE", "/api/devices/"+aID, ""); !ok(rec) {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if n := len(g.subsOf("https://push.example/a")); n != 0 {
		t.Fatal("a revoked device's subscription is still stored")
	}
	if n := len(g.subsOf("https://push.example/b")); n != 1 {
		t.Fatal("revoking one device dropped another's subscription")
	}
}

func TestRevokingAllDevicesDropsEverySubscription(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	a, b := g.pairLAN(), g.pairLAN()
	g.lanPush(a, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/a"))
	g.lanPush(b, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/b"))
	g.localPush("PUT", pushtest.NewSubscriber(t).JSON("https://push.example/mac"))
	if rec := g.local("DELETE", "/api/devices", ""); !ok(rec) {
		t.Fatalf("revoke all: %d %s", rec.Code, rec.Body)
	}
	if n := len(g.a.Subscriptions()); n != 0 {
		t.Fatalf("%d subscriptions survive revoke all", n)
	}
}

func TestAnExpiredDeviceTakesItsSubscriptionsWithIt(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	token := g.pairLAN()
	g.lanPush(token, "PUT", pushtest.NewSubscriber(t).JSON("https://push.example/a"))
	g.clk.advance(91 * 24 * time.Hour)
	g.devices(req{method: "GET", remote: loopback, host: "127.0.0.1:7420"})
	if n := len(g.a.Subscriptions()); n != 0 {
		t.Fatalf("%d subscriptions survive the 90-day prune", n)
	}
}

func TestSubscriptionsSurviveARestartAndStayOutOfTheDeviceList(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clk := &clock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	g := pushRigAt(t, home, clk)
	token := g.pairLAN()
	sub := pushtest.NewSubscriber(t)
	g.lanPush(token, "PUT", sub.JSON("https://push.example/a"))

	g2 := pushRigAt(t, home, clk)
	if n := len(g2.a.Subscriptions()); n != 1 {
		t.Fatalf("%d subscriptions after a restart, want 1", n)
	}
	rec := g2.do(func() req { r := lanReq(bearer(token)); r.target = "/api/devices"; return r }())
	if rec.Code != 200 || containsAny(rec.Body.String(), sub.Auth(), sub.P256dh()) {
		t.Fatalf("the device list leaks the subscription's keys or is not served: %d %s", rec.Code, rec.Body)
	}
	if fi, err := os.Stat(filepath.Join(home, "devices.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("devices.json: %v %v, want mode 0600 (it holds auth secrets)", fi, err)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if x != "" && strings.Contains(s, x) {
			return true
		}
	}
	return false
}

func TestThisMacIsMadeOnALocalSubscribeWithoutPairing(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	if _, found := g.deviceNamed(req{remote: loopback, host: "127.0.0.1:7420"}, "This Mac"); found {
		t.Fatal("This Mac exists before anyone subscribed")
	}
	if rec := g.localPush("PUT", pushtest.NewSubscriber(t).JSON("https://push.example/safari")); !ok(rec) {
		t.Fatalf("local subscribe: %d %s", rec.Code, rec.Body)
	}
	d, found := g.deviceNamed(req{remote: loopback, host: "127.0.0.1:7420"}, "This Mac")
	if !found || !d.Notifications {
		t.Fatalf("This Mac: %+v found=%v, want a device with notifications on", d, found)
	}
}

func TestASecondLocalSubscribeAddsToTheSameThisMac(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	g.localPush("PUT", pushtest.NewSubscriber(t).JSON("https://push.example/safari"))
	g.localPush("PUT", pushtest.NewSubscriber(t).JSON("https://push.example/chrome"))
	macs := 0
	for _, d := range g.devices(req{remote: loopback, host: "127.0.0.1:7420"}) {
		if d.Name == "This Mac" {
			macs++
		}
	}
	if macs != 1 || len(g.a.Subscriptions()) != 2 {
		t.Fatalf("%d This Mac devices and %d subscriptions, want 1 and 2", macs, len(g.a.Subscriptions()))
	}
}

func TestThisMacIsCurrentOnTheLocalRouteAndNotOnTheLAN(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	token := g.pairLAN()
	g.localPush("PUT", pushtest.NewSubscriber(t).JSON("https://push.example/safari"))
	for _, d := range g.devices(req{remote: loopback, host: "127.0.0.1:7420"}) {
		if d.Current != (d.Name == "This Mac") {
			t.Errorf("on the Local route %q current=%v, want only This Mac current", d.Name, d.Current)
		}
	}
	for _, d := range g.devices(lanReq(bearer(token))) {
		if d.Current == (d.Name == "This Mac") {
			t.Errorf("on the LAN %q current=%v, want only the caller current", d.Name, d.Current)
		}
	}
}

func TestLocalUseKeepsThisMacFromThe90DayPrune(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	g.localPush("PUT", pushtest.NewSubscriber(t).JSON("https://push.example/safari"))
	local := req{remote: loopback, host: "127.0.0.1:7420"}
	g.clk.advance(60 * 24 * time.Hour)
	g.devices(local)
	g.clk.advance(60 * 24 * time.Hour)
	if _, found := g.deviceNamed(local, "This Mac"); !found {
		t.Fatal("This Mac was pruned 120 days after it subscribed although the Local route was used on day 60")
	}
	if len(g.a.Subscriptions()) != 1 {
		t.Fatal("This Mac's subscription was dropped")
	}
}

func TestRevokingThisMacDropsItsSubscriptionsAndALaterSubscribeMakesANewOne(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	g.localPush("PUT", pushtest.NewSubscriber(t).JSON("https://push.example/safari"))
	local := req{remote: loopback, host: "127.0.0.1:7420"}
	mac, _ := g.deviceNamed(local, "This Mac")
	if rec := g.local("DELETE", "/api/devices/"+mac.ID, ""); !ok(rec) {
		t.Fatalf("revoke This Mac: %d %s", rec.Code, rec.Body)
	}
	if _, found := g.deviceNamed(local, "This Mac"); found || len(g.a.Subscriptions()) != 0 {
		t.Fatalf("This Mac found=%v subscriptions=%d after revoke", found, len(g.a.Subscriptions()))
	}
	g.localPush("PUT", pushtest.NewSubscriber(t).JSON("https://push.example/safari"))
	if again, found := g.deviceNamed(local, "This Mac"); !found || again.ID == mac.ID || !again.Notifications {
		t.Fatalf("after subscribing again: %+v found=%v (old id %s)", again, found, mac.ID)
	}
}

func TestLocalUnsubscribeRemovesOnlyThatEndpointFromThisMac(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	g.localPush("PUT", pushtest.NewSubscriber(t).JSON("https://push.example/safari"))
	g.localPush("PUT", pushtest.NewSubscriber(t).JSON("https://push.example/chrome"))
	if rec := g.localPush("DELETE", endpointBody("https://push.example/safari")); !ok(rec) {
		t.Fatalf("local unsubscribe: %d %s", rec.Code, rec.Body)
	}
	if len(g.subsOf("https://push.example/chrome")) != 1 || len(g.a.Subscriptions()) != 1 {
		t.Fatalf("subscriptions after one browser turned off: %+v", g.a.Subscriptions())
	}
}

func TestDeviceIDNamesThePairedDeviceAndIsLocalNamesTheLocalRoute(t *testing.T) {
	t.Parallel()
	g := pushRig(t)
	token := g.pairLAN()
	var gotID, gotLocal string
	mux := http.NewServeMux()
	mux.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		id, has := auth.DeviceID(r)
		gotID = map[bool]string{true: "device:" + id, false: "none"}[has]
		gotLocal = map[bool]string{true: "local", false: "remote"}[auth.IsLocal(r)]
	})
	h := g.a.Middleware(mux)
	rq := httptest.NewRequest("GET", "/probe", nil)
	rq.RemoteAddr, rq.Host = lan, "192.168.1.10:7420"
	rq.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), rq)
	if gotID == "none" || gotID == "" || gotLocal != "remote" {
		t.Fatalf("paired LAN request: DeviceID %q IsLocal %q", gotID, gotLocal)
	}
	rq = httptest.NewRequest("GET", "/probe", nil)
	rq.RemoteAddr, rq.Host = loopback, "127.0.0.1:7420"
	h.ServeHTTP(httptest.NewRecorder(), rq)
	if gotLocal != "local" {
		t.Fatalf("loopback request: IsLocal %q, want local", gotLocal)
	}
}
