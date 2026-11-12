package push_test

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/push/pushtest"
)

func TestDialControlRefusesAnIPv4AddressInsideNAT64And6to4(t *testing.T) {
	t.Parallel()
	for addr, ok := range map[string]bool{
		"[64:ff9b::7f00:1]:443":           false, // 127.0.0.1
		"[64:ff9b::a00:1]:443":            false, // 10.0.0.1
		"[64:ff9b::5db8:d822]:443":        true,  // 93.184.216.34
		"[64:ff9b:1:0:0:7f:0:1]:443":      false, // /48 form of 127.0.0.1: v4 at bytes 6,7,9,10
		"[64:ff9b:1:0:0:5db8:0:d822]:443": false,
		"[2002:7f00:1::]:443":             false, // 127.0.0.1
		"[2002:c0a8:101::]:443":           false, // 192.168.1.1
		"[2002:5db8:d822::]:443":          true,
		"0.0.0.1:443":                     false,
		"240.0.0.1:443":                   false,
		"255.255.255.255:443":             false,
	} {
		if err := push.DialControl("tcp", addr, nil); (err == nil) != ok {
			t.Errorf("DialControl(%s) = %v, want allowed=%v", addr, err, ok)
		}
	}
}

func TestPayloadStripsArabicLetterMarkAndZeroWidthCharacters(t *testing.T) {
	t.Parallel()
	for _, c := range []string{"\xd8\x9c", "\xe2\x80\x8b", "\xe2\x80\x8c", "\xe2\x80\x8d", "\xef\xbb\xbf"} {
		p := push.PayloadOf(push.Event{Kind: push.ShipQuestion, Name: "na" + c + "me", FleetName: "fl" + c + "eet", Text: "li" + c + "ne"})
		if p.Name != "name" || p.FleetName != "fleet" || p.Line != "line" {
			t.Errorf("U+%04X left %q %q %q", []rune(c)[0], p.Name, p.FleetName, p.Line)
		}
	}
}

func TestADanglingKeySymlinkIsAnErrorAndNoKeyIsMade(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "vapid.json")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := push.LoadKey(dir); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("LoadKey followed a dangling symlink into a new key")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadKey looped on a dangling symlink")
	}
	if _, err := os.Stat(filepath.Join(dir, "nowhere")); err == nil {
		t.Error("a key was written through the symlink")
	}
}

func TestEncryptTakesThe3993BytesThatFitTheWholeBodyIn4096(t *testing.T) {
	t.Parallel()
	sub := target(pushtest.NewSubscriber(t))
	body, err := push.Encrypt(sub, make([]byte, 3993))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 4096 {
		t.Errorf("body is %d bytes, want 4096", len(body))
	}
	if _, err := push.Encrypt(sub, make([]byte, 3994)); err == nil {
		t.Error("3994 bytes were accepted; the body would pass 4096")
	}
}

func TestAFailedSendLogsTheHostAndNeverTheEndpointPath(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	svc := pushtest.NewService(t)
	key, err := push.LoadKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dev := &fakeDevices{}
	sub := pushtest.NewSubscriber(t)
	// Nothing listens on port 1, so the request fails with an error that carries the URL.
	dev.targets = []push.Target{{Device: "d", Subscription: push.Subscription{Endpoint: "https://127.0.0.1:1/capability-xyz", P256dh: sub.P256dh(), Auth: sub.Auth()}}}
	s := push.NewSender(push.SenderConfig{Key: key, Client: svc.Client(), Devices: dev, Timeout: 2 * time.Second})
	s.Deliver(question("n", "q"))
	s.Close()
	out := buf.String()
	if !strings.Contains(out, "127.0.0.1:1") || strings.Contains(out, "capability-xyz") {
		t.Errorf("log = %q, want the host and not the path", out)
	}
}

func TestCloseDoesNotWaitForeverOnAnEndpointThatNeverAnswers(t *testing.T) {
	t.Parallel()
	svc := pushtest.NewService(t)
	key, err := push.LoadKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dev := &fakeDevices{}
	sub := pushtest.NewSubscriber(t)
	dev.targets = []push.Target{{Device: "d", Subscription: push.Subscription{Endpoint: svc.Endpoint("/ep/hang"), P256dh: sub.P256dh(), Auth: sub.Auth()}}}
	release := svc.Block("/ep/hang")
	t.Cleanup(release)
	s := push.NewSender(push.SenderConfig{Key: key, Client: svc.Client(), Devices: dev, Timeout: time.Minute, CloseGrace: 200 * time.Millisecond})
	s.Deliver(question("n", "q"))
	if _, ok := svc.Next(5 * time.Second); !ok {
		t.Fatal("the request never arrived")
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited on a request that never answers")
	}
}
