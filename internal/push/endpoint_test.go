package push_test

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/push/pushtest"
)

func TestCheckEndpointNeedsHTTPS(t *testing.T) {
	t.Parallel()
	for endpoint, ok := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":       true,
		"https://web.push.apple.com/QWxs":               true,
		"https://127.0.0.1:9/loopback-is-a-dial-matter": true,
		"http://push.example/a":                         false,
		"ftp://push.example/a":                          false,
		"https:///nohost":                               false,
		"push.example/a":                                false,
		"":                                              false,
	} {
		if err := push.CheckEndpoint(endpoint); (err == nil) != ok {
			t.Errorf("CheckEndpoint(%q) = %v, want ok=%v", endpoint, err, ok)
		}
	}
}

func TestDialControlRefusesLocalDestinationsAndAllowsPublicOnes(t *testing.T) {
	t.Parallel()
	for addr, ok := range map[string]bool{
		"127.0.0.1:443":          false,
		"127.8.8.8:443":          false,
		"10.0.0.1:443":           false,
		"172.16.4.4:443":         false,
		"192.168.1.5:443":        false,
		"169.254.1.1:443":        false,
		"0.0.0.0:443":            false,
		"[::1]:443":              false,
		"[::]:443":               false,
		"[fe80::1]:443":          false,
		"[fc00::1]:443":          false,
		"[::ffff:127.0.0.1]:443": false,
		"[::ffff:10.0.0.1]:443":  false,
		"93.184.216.34:443":      true,
		"17.0.0.1:443":           true,
		"[2606:4700::1111]:443":  true,
	} {
		if err := push.DialControl("tcp", addr, nil); (err == nil) != ok {
			t.Errorf("DialControl(%s) = %v, want allowed=%v", addr, err, ok)
		}
	}
}

func TestTheSendersClientCannotReachALoopbackServiceUnlessLocalIsAllowed(t *testing.T) {
	t.Parallel()
	svc := pushtest.NewService(t)
	if _, err := push.SafeClient(svc.Pool(), false).Post(svc.URL+"/x", "text/plain", nil); err == nil {
		t.Fatal("the client reached a service on loopback")
	}
	if n := svc.Count(); n != 0 {
		t.Fatalf("%d requests reached the loopback service", n)
	}
	resp, err := push.SafeClient(svc.Pool(), true).Post(svc.URL+"/x", "text/plain", nil)
	if err != nil {
		t.Fatalf("with local allowed (the e2e seam): %v", err)
	}
	_ = resp.Body.Close()
}

func TestARefusedDestinationIsLoggedAndTheSubscriptionKept(t *testing.T) {
	t.Parallel()
	svc := pushtest.NewService(t)
	key, _ := push.LoadKey(t.TempDir())
	sub := pushtest.NewSubscriber(t)
	dev := &fakeDevices{targets: []push.Target{{Device: "d", Subscription: push.Subscription{Endpoint: svc.Endpoint("/a"), P256dh: sub.P256dh(), Auth: sub.Auth()}}}}
	s := push.NewSender(push.SenderConfig{Key: key, Client: push.SafeClient(svc.Pool(), false), Devices: dev, Queue: 4, Timeout: 2 * time.Second})
	s.Deliver(question("n", "hello"))
	s.Close()
	if d := dev.droppedList(); len(d) != 0 {
		t.Fatalf("a refused send dropped %v, want the subscription kept", d)
	}
	if n := svc.Count(); n != 0 {
		t.Fatalf("%d requests reached the service", n)
	}
}
