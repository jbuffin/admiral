package push_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/push/pushtest"
)

type fakeDevices struct {
	mu      sync.Mutex
	targets []push.Target
	dropped []string
	panics  int
}

func (d *fakeDevices) Subscriptions() []push.Target {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.panics > 0 {
		d.panics--
		panic("subscriptions blew up")
	}
	return append([]push.Target(nil), d.targets...)
}

func (d *fakeDevices) Drop(device, endpoint string) {
	d.mu.Lock()
	d.dropped = append(d.dropped, device+" "+endpoint)
	d.mu.Unlock()
}

func (d *fakeDevices) droppedList() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.dropped...)
}

type sendRig struct {
	svc  *pushtest.Service
	dev  *fakeDevices
	key  *push.Key
	send *push.Sender
}

func newSendRig(t *testing.T, queue int, timeout time.Duration) *sendRig {
	t.Helper()
	svc := pushtest.NewService(t)
	key, err := push.LoadKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dev := &fakeDevices{}
	s := push.NewSender(push.SenderConfig{Key: key, Client: svc.Client(), Devices: dev, Queue: queue, Timeout: timeout})
	t.Cleanup(s.Close)
	return &sendRig{svc: svc, dev: dev, key: key, send: s}
}

func (r *sendRig) subscribe(t *testing.T, device, path string) *pushtest.Subscriber {
	t.Helper()
	sub := pushtest.NewSubscriber(t)
	r.dev.mu.Lock()
	r.dev.targets = append(r.dev.targets, push.Target{Device: device, Subscription: push.Subscription{Endpoint: r.svc.Endpoint(path), P256dh: sub.P256dh(), Auth: sub.Auth()}})
	r.dev.mu.Unlock()
	return sub
}

func question(name, text string) push.Decided {
	return push.Decided{Outcome: push.Push, Event: push.Event{Kind: push.ShipQuestion, Fleet: "work", FleetName: "Work", Key: "s1", Name: name, Text: text, Target: push.TargetPeek, Session: "s1"}}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDeliverSendsAnEncryptedSignedRequestThatDecryptsToThePayload(t *testing.T) {
	t.Parallel()
	r := newSendRig(t, 8, 2*time.Second)
	sub := r.subscribe(t, "d1", "/ep/a")
	ev := question("api-gateway", "Should I delete the migration?")
	r.send.Deliver(ev)

	got, ok := r.svc.Next(5 * time.Second)
	if !ok {
		t.Fatal("no request reached the push service")
	}
	if got.Path != "/ep/a" {
		t.Errorf("path %q", got.Path)
	}
	h := got.Header
	if h.Get("Content-Encoding") != "aes128gcm" {
		t.Errorf("Content-Encoding %q, want aes128gcm", h.Get("Content-Encoding"))
	}
	if ttl, err := strconv.Atoi(h.Get("TTL")); err != nil || ttl <= 0 {
		t.Errorf("TTL %q, want a positive number of seconds (RFC 8030 requires it)", h.Get("TTL"))
	}
	if h.Get("Urgency") != "high" {
		t.Errorf("Urgency %q, want high", h.Get("Urgency"))
	}
	if _, has := h["Topic"]; has {
		t.Errorf("Topic %q sent; the tag is the notification's, not the push's", h.Get("Topic"))
	}
	v, err := pushtest.VerifyVAPID(h.Get("Authorization"))
	if err != nil || v.Key != r.key.Public() || v.Aud != r.svc.URL {
		t.Errorf("Authorization: %+v err %v, want k=%s aud=%s", v, err, r.key.Public(), r.svc.URL)
	}
	plain, err := sub.Decrypt(got.Body)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	want, _ := json.Marshal(push.PayloadOf(ev.Event))
	if string(plain) != string(want) {
		t.Fatalf("payload %s, want %s", plain, want)
	}
	if strings.Contains(string(got.Body), "api-gateway") {
		t.Error("the body carries the name in the clear")
	}
}

func TestDeliverReachesEverySubscriptionOfEveryDevice(t *testing.T) {
	t.Parallel()
	r := newSendRig(t, 16, 2*time.Second)
	subs := map[string]*pushtest.Subscriber{
		"/ep/a1": r.subscribe(t, "a", "/ep/a1"),
		"/ep/a2": r.subscribe(t, "a", "/ep/a2"),
		"/ep/b":  r.subscribe(t, "b", "/ep/b"),
	}
	r.send.Deliver(question("n", "hello"))
	seen := map[string]bool{}
	for range subs {
		got, ok := r.svc.Next(5 * time.Second)
		if !ok {
			t.Fatalf("only %d of 3 subscriptions got a push", len(seen))
		}
		if _, err := subs[got.Path].Decrypt(got.Body); err != nil {
			t.Errorf("%s: %v", got.Path, err)
		}
		seen[got.Path] = true
	}
	if len(seen) != 3 {
		t.Fatalf("pushed to %v", seen)
	}
}

func TestDeliverIgnoresOutcomesThatAreNotAPush(t *testing.T) {
	t.Parallel()
	r := newSendRig(t, 8, 2*time.Second)
	r.subscribe(t, "d1", "/ep/a")
	for _, o := range []push.Outcome{push.Alert, push.Nothing} {
		d := question("n", "hello")
		d.Outcome = o
		r.send.Deliver(d)
	}
	r.send.Close()
	if n := r.svc.Count(); n != 0 {
		t.Fatalf("%d pushes for a watched or silent outcome", n)
	}
}

func TestAGoneSubscriptionIsDroppedOn404And410ButNotOnA500(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		code int
		drop bool
	}{{404, true}, {410, true}, {500, false}, {429, false}, {201, false}} {
		t.Run(strconv.Itoa(tc.code), func(t *testing.T) {
			t.Parallel()
			r := newSendRig(t, 8, 2*time.Second)
			r.subscribe(t, "dev", "/ep/a")
			r.svc.SetStatus("/ep/a", tc.code)
			r.send.Deliver(question("n", "hello"))
			if _, ok := r.svc.Next(5 * time.Second); !ok {
				t.Fatal("no request")
			}
			r.send.Close()
			dropped := r.dev.droppedList()
			if tc.drop && (len(dropped) != 1 || dropped[0] != "dev "+r.svc.Endpoint("/ep/a")) {
				t.Fatalf("status %d: dropped %v, want the device's endpoint", tc.code, dropped)
			}
			if !tc.drop && len(dropped) != 0 {
				t.Fatalf("status %d dropped %v, want the subscription kept", tc.code, dropped)
			}
		})
	}
}

func TestDeliverNeverBlocksOnASlowPushService(t *testing.T) {
	t.Parallel()
	r := newSendRig(t, 2, 5*time.Second)
	r.subscribe(t, "d", "/ep/slow")
	release := r.svc.Block("/ep/slow")
	t.Cleanup(release)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			r.send.Deliver(question("n", "hello"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Deliver blocked while the push service held a request")
	}
}

func TestARequestThatHangsTimesOutAndLaterPushesStillGo(t *testing.T) {
	t.Parallel()
	r := newSendRig(t, 8, 300*time.Millisecond)
	r.subscribe(t, "d", "/ep/hang")
	release := r.svc.Block("/ep/hang")
	t.Cleanup(release)
	r.send.Deliver(question("n", "first"))
	if _, ok := r.svc.Next(5 * time.Second); !ok {
		t.Fatal("the hanging request never arrived")
	}
	r.subscribe(t, "d2", "/ep/ok")
	r.send.Deliver(question("n", "second"))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, ok := r.svc.Next(100 * time.Millisecond); ok && got.Path == "/ep/ok" {
			return
		}
	}
	t.Fatal("the sender stayed stuck on a request that never answered")
}

func TestAPanicInTheSenderIsRecoveredAndItKeepsSending(t *testing.T) {
	t.Parallel()
	r := newSendRig(t, 8, 2*time.Second)
	r.subscribe(t, "d", "/ep/a")
	r.dev.mu.Lock()
	r.dev.panics = 1
	r.dev.mu.Unlock()
	r.send.Deliver(question("n", "boom"))
	r.send.Deliver(question("n", "after"))
	if _, ok := r.svc.Next(5 * time.Second); !ok {
		t.Fatal("the sender goroutine died on a panic")
	}
}

func TestADroppedSubscriptionGetsNoMorePushes(t *testing.T) {
	t.Parallel()
	r := newSendRig(t, 8, 2*time.Second)
	r.subscribe(t, "d", "/ep/gone")
	r.svc.SetStatus("/ep/gone", 410)
	r.send.Deliver(question("n", "one"))
	eventually(t, "the drop", func() bool { return len(r.dev.droppedList()) == 1 })
	r.dev.mu.Lock()
	r.dev.targets = nil
	r.dev.mu.Unlock()
	r.send.Deliver(question("n", "two"))
	r.send.Close()
	if n := r.svc.Count(); n != 1 {
		t.Fatalf("%d requests, want just the first", n)
	}
}
