package push

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Target is one subscription and the device that holds it.
type Target struct {
	Device string
	Subscription
}

// Devices is where the sender finds its subscriptions and forgets the ones a push service says are gone.
type Devices interface {
	Subscriptions() []Target
	Drop(device, endpoint string)
}

type SenderConfig struct {
	Key     *Key
	Client  *http.Client
	Devices Devices
	// Queue is how many events wait for the sender; a fuller queue drops new ones.
	Queue int
	// Timeout bounds each request to a push service.
	Timeout time.Duration
	Now     func() time.Time
}

// pushTTL is how long a push service holds a message for an offline device: a stale question is noise.
const pushTTL = 3600

// Sender is the Sink for the Push outcome: it encrypts each event for every subscription and posts it, off the caller's goroutine.
type Sender struct {
	cfg SenderConfig

	mu     sync.Mutex
	closed bool
	queue  chan Decided
	wg     sync.WaitGroup
}

func NewSender(cfg SenderConfig) *Sender {
	if cfg.Queue < 1 {
		cfg.Queue = 64
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Sender{cfg: cfg, queue: make(chan Decided, cfg.Queue)}
	s.wg.Add(1)
	go s.run()
	return s
}

// Deliver never blocks: an event that finds the queue full is dropped and logged.
func (s *Sender) Deliver(d Decided) {
	if d.Outcome != Push {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.queue <- d:
	default:
		log.Printf("push: queue full, dropped a notification")
	}
}

// Close sends what is queued, waits for requests in flight, and stops the sender.
func (s *Sender) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.queue)
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Sender) run() {
	defer s.wg.Done()
	for d := range s.queue {
		s.fanOut(d)
	}
}

func (s *Sender) fanOut(d Decided) {
	defer recoverAndLog("event")
	body, err := json.Marshal(PayloadOf(d.Event))
	if err != nil {
		log.Printf("push: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, t := range s.cfg.Devices.Subscriptions() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer recoverAndLog("send")
			s.send(t, body)
		}()
	}
	wg.Wait()
}

func recoverAndLog(what string) {
	if r := recover(); r != nil {
		log.Printf("push: panic in %s: %v", what, r)
	}
}

func (s *Sender) send(t Target, payload []byte) {
	// A capability URL: log the push service, never the path.
	host := "?"
	if u, err := url.Parse(t.Endpoint); err == nil {
		host = u.Host
	}
	sealed, err := Encrypt(t.Subscription, payload)
	if err != nil {
		log.Printf("push: not sent to %s: %v", host, err)
		return
	}
	auth, err := AuthHeader(s.cfg.Key, t.Endpoint, s.cfg.Now())
	if err != nil {
		log.Printf("push: not sent to %s: %v", host, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, bytes.NewReader(sealed))
	if err != nil {
		log.Printf("push: not sent to %s: %v", host, err)
		return
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", auth)
	req.Header.Set("TTL", strconv.Itoa(pushTTL))
	req.Header.Set("Urgency", "high")
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		log.Printf("push: not sent to %s: %v", host, err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		s.cfg.Devices.Drop(t.Device, t.Endpoint)
		log.Printf("push: %s no longer holds a subscription (%d); dropped it", host, resp.StatusCode)
	case resp.StatusCode/100 != 2:
		log.Printf("push: %s answered %d", host, resp.StatusCode)
	}
}
