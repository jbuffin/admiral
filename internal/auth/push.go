package auth

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

const thisMacName = "This Mac"

// Subscription is a browser's push subscription, kept on the device that holds it.
type Subscription struct {
	Device   string `json:"-"`
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// Subscriptions lists every subscription of every device.
func (a *Auth) Subscriptions() []Subscription {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.prune() {
		_ = a.save()
	}
	var out []Subscription
	for _, d := range a.devices {
		for _, s := range d.Subscriptions {
			s.Device = d.ID
			out = append(out, s)
		}
	}
	return out
}

// DropSubscription forgets one endpoint of a device, for when its push service says it is gone.
func (a *Auth) DropSubscription(device, endpoint string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.find(device); d != nil && removeEndpoint(d, endpoint) {
		_ = a.save()
	}
}

func removeEndpoint(d *device, endpoint string) bool {
	kept := make([]Subscription, 0, len(d.Subscriptions))
	for _, s := range d.Subscriptions {
		if s.Endpoint != endpoint {
			kept = append(kept, s)
		}
	}
	changed := len(kept) != len(d.Subscriptions)
	d.Subscriptions = kept
	return changed
}

// touchThisMac keeps This Mac out of the 90-day prune while the Local route is in use.
func (a *Auth) touchThisMac() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.devices {
		if d.ThisMac {
			if now := a.now(); now.Sub(d.LastSeen) > touchEvery {
				d.LastSeen = now
				_ = a.save()
			}
			return
		}
	}
}

// snapshot copies the device list, so a failed save can put it back. Callers hold mu.
func (a *Auth) snapshot() []*device {
	out := make([]*device, len(a.devices))
	for i, d := range a.devices {
		c := *d
		c.Subscriptions = append([]Subscription(nil), d.Subscriptions...)
		out[i] = &c
	}
	return out
}

// requester is the device a push request acts for: the paired caller, or This Mac on the Local route, made on demand when create is set.
// Callers hold mu.
func (a *Auth) requester(r *http.Request, create bool) (*device, bool) {
	if id, ok := DeviceID(r); ok {
		d := a.find(id)
		return d, d != nil
	}
	if !IsLocal(r) {
		return nil, false
	}
	for _, d := range a.devices {
		if d.ThisMac {
			return d, true
		}
	}
	if !create {
		return nil, true
	}
	idRaw := make([]byte, 8)
	if _, err := rand.Read(idRaw); err != nil {
		return nil, true
	}
	now := a.now()
	d := &device{ID: hex.EncodeToString(idRaw), Name: thisMacName, PairedAt: now, LastSeen: now, ThisMac: true}
	a.devices = append(a.devices, d)
	return d, true
}

func (a *Auth) servePushKey(w http.ResponseWriter, r *http.Request) {
	key := ""
	if a.pushKey != nil {
		key = a.pushKey()
	}
	if key == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "push is not available"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key})
}

func (a *Auth) serveSubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string
		Keys     struct{ P256dh, Auth string }
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected a PushSubscription"})
		return
	}
	sub := Subscription{Endpoint: body.Endpoint, P256dh: strings.TrimRight(body.Keys.P256dh, "="), Auth: strings.TrimRight(body.Keys.Auth, "=")}
	if msg := checkSubscription(sub); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	before := a.snapshot()
	d, ok := a.requester(r, true)
	if !ok || d == nil {
		a.devices = before
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	// An endpoint belongs to one browser, which one device holds at a time.
	for _, other := range a.devices {
		removeEndpoint(other, sub.Endpoint)
	}
	d.Subscriptions = append(d.Subscriptions, sub)
	if d.ThisMac {
		d.LastSeen = a.now()
	}
	if err := a.save(); err != nil {
		a.devices = before
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the device list"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Auth) serveUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct{ Endpoint string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Endpoint == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `expected {"endpoint": "…"}`})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	d, ok := a.requester(r, false)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if d != nil && removeEndpoint(d, body.Endpoint) {
		if err := a.save(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the device list"})
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkSubscription says what is wrong with a subscription, or "" when it is fit to store. A loopback or private
// endpoint passes: the sender refuses those at dial time, where DNS cannot get around it.
func checkSubscription(s Subscription) string {
	if u, err := url.Parse(s.Endpoint); err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return "the endpoint must be an https URL"
	}
	if raw, err := base64.RawURLEncoding.DecodeString(s.P256dh); err != nil {
		return "keys.p256dh is not base64url"
	} else if _, err := ecdh.P256().NewPublicKey(raw); err != nil {
		return "keys.p256dh is not a P-256 point"
	}
	if raw, err := base64.RawURLEncoding.DecodeString(s.Auth); err != nil || len(raw) != 16 {
		return "keys.auth must be 16 bytes of base64url"
	}
	return ""
}
