package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	devicesFile = "devices.json"
	idleDrop    = 90 * 24 * time.Hour
	// A device's last-seen time and cookie are refreshed at most this often, so a busy page doesn't write the store on every request.
	touchEvery   = time.Hour
	reissueEvery = 24 * time.Hour
	// stoppedAfter is how long a following device may have no socket open before it reads stopped.
	stoppedAfter = 5 * time.Minute
)

type device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Login     string    `json:"login,omitempty"`
	TokenHash string    `json:"tokenHash"`
	PairedAt  time.Time `json:"pairedAt"`
	LastSeen  time.Time `json:"lastSeen"`
	// ThisMac marks the device that loopback browsers subscribe as, with no pairing; it has no token.
	ThisMac       bool           `json:"thisMac,omitempty"`
	Subscriptions []Subscription `json:"subscriptions,omitempty"`
	Forwarding    bool           `json:"forwarding,omitempty"`
	LastFollowed  *time.Time     `json:"lastFollowed,omitempty"`

	reissued time.Time
}

type socket struct {
	device string
	login  string // the tailnet login it came in under; empty on the LAN
	kind   pathKind
	cancel context.CancelFunc
}

type Auth struct {
	cookie  string
	home    string
	now     func() time.Time
	baseURL func() string
	remote  func() (bool, []string)
	pushKey func() string
	start   time.Time
	// following is whether a device has a follower socket open now; nil means none does.
	following func(device string) bool

	mu      sync.Mutex
	devices []*device
	socks   map[*socket]struct{}
	code    liveCode
	fails   []time.Time
}

type Option func(*Auth)

func WithClock(now func() time.Time) Option { return func(a *Auth) { a.now = now } }

// WithStart gives the time Admiral started, which the forwarding rule counts from.
func WithStart(t time.Time) Option { return func(a *Auth) { a.start = t } }

// WithFollowing gives whether a device has a follower socket open now.
func WithFollowing(open func(device string) bool) Option { return func(a *Auth) { a.following = open } }

// Followed records that the device follows push events; a record that exists is left as it is.
func (a *Auth) Followed(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.find(id); d != nil && !d.Forwarding {
		d.Forwarding = true
		a.saveOrRevert(func() { d.Forwarding = false })
	}
}

// Unfollowed clears the device's forwarding record.
func (a *Auth) Unfollowed(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.find(id); d != nil && (d.Forwarding || d.LastFollowed != nil) {
		was, last := d.Forwarding, d.LastFollowed
		d.Forwarding, d.LastFollowed = false, nil
		a.saveOrRevert(func() { d.Forwarding, d.LastFollowed = was, last })
	}
}

// FollowEnded saves when the device's last follower socket closed; a device with no record, cleared or revoked, stays as it is.
func (a *Auth) FollowEnded(id string, at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.find(id); d != nil && d.Forwarding {
		last := d.LastFollowed
		d.LastFollowed = &at
		a.saveOrRevert(func() { d.LastFollowed = last })
	}
}

// saveOrRevert keeps the change in memory only when it reached the disk, so a restart never disagrees with what was shown. Callers hold mu.
func (a *Auth) saveOrRevert(undo func()) {
	if a.save() != nil {
		undo()
	}
}

// forwardingState is "on" while a follower socket is open and for stoppedAfter past the later of the last close and Admiral's start, then "stopped"; nil for a device with no record. Callers hold mu.
func (a *Auth) forwardingState(d *device) *string {
	if !d.Forwarding {
		return nil
	}
	state := "on"
	if a.following == nil || !a.following(d.ID) {
		from := a.start
		if d.LastFollowed != nil && d.LastFollowed.After(from) {
			from = *d.LastFollowed
		}
		if a.now().Sub(from) > stoppedAfter {
			state = "stopped"
		}
	}
	return &state
}

// WithBaseURL gives the address pairing URLs start with, such as http://192.168.1.10:7420.
func WithBaseURL(base func() string) Option { return func(a *Auth) { a.baseURL = base } }

// WithRemote gives the live Tailscale mode and login allowlist; without it the mode is off.
func WithRemote(remote func() (enabled bool, allowedLogins []string)) Option {
	return func(a *Auth) { a.remote = remote }
}

// WithPushKey gives the VAPID public key the push key route answers.
func WithPushKey(key func() string) Option { return func(a *Auth) { a.pushKey = key } }

// The port is in the cookie name so a test instance on the same host never
// overwrites the real one's cookie.
func New(home, port string, opts ...Option) (*Auth, error) {
	a := &Auth{cookie: "admiral_token_" + port, home: home, now: time.Now, remote: func() (bool, []string) { return false, nil }, socks: map[*socket]struct{}{}}
	for _, o := range opts {
		o(a)
	}
	if a.start.IsZero() {
		a.start = a.now()
	}
	// The shared token is retired with no fallback.
	if err := os.Remove(filepath.Join(home, "token")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(home, devicesFile))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var f struct{ Devices []*device }
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, err
		}
		a.devices = f.Devices
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.prune() {
		if err := a.save(); err != nil {
			return nil, err
		}
	}
	return a, nil
}

func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Callers hold mu.
func (a *Auth) save() error {
	b, err := json.Marshal(struct {
		Devices []*device `json:"devices"`
	}{a.devices})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(a.home, ".devices.*") // created 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(a.home, devicesFile))
}

// prune drops devices unseen for 90 days and reports whether any went. Callers hold mu.
func (a *Auth) prune() bool {
	now := a.now()
	kept := a.devices[:0]
	for _, d := range a.devices {
		if now.Sub(d.LastSeen) > idleDrop {
			a.closeSockets(d.ID)
			continue
		}
		kept = append(kept, d)
	}
	dropped := len(kept) != len(a.devices)
	a.devices = kept
	return dropped
}

func (a *Auth) find(id string) *device {
	for _, d := range a.devices {
		if d.ID == id {
			return d
		}
	}
	return nil
}

func (a *Auth) closeSockets(id string) {
	for s := range a.socks {
		if s.device == id {
			s.cancel()
		}
	}
}

// authenticate returns the device id when the token is a paired device's and the request came in by the route that device paired on.
func (a *Auth) authenticate(token string, rt route) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.prune() {
		_ = a.save()
	}
	sum := hashToken(token)
	var found *device
	for _, d := range a.devices {
		if subtle.ConstantTimeCompare([]byte(sum), []byte(d.TokenHash)) == 1 {
			found = d
		}
	}
	if found == nil || found.ThisMac || found.Login != rt.login || (rt.kind != pathLAN && rt.kind != pathTailnet) {
		return "", false
	}
	if now := a.now(); now.Sub(found.LastSeen) > touchEvery {
		found.LastSeen = now
		_ = a.save()
	}
	return found.ID, true
}

func (a *Auth) shouldReissue(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.find(id)
	if d == nil || a.now().Sub(d.reissued) < reissueEvery {
		return false
	}
	d.reissued = a.now()
	return true
}

func (a *Auth) add(name, login string) (*device, string, error) {
	token, err := newToken()
	if err != nil {
		return nil, "", err
	}
	idRaw := make([]byte, 8)
	if _, err := rand.Read(idRaw); err != nil {
		return nil, "", err
	}
	now := a.now()
	d := &device{ID: hex.EncodeToString(idRaw), Name: name, Login: login, TokenHash: hashToken(token), PairedAt: now, LastSeen: now}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.devices = append(a.devices, d)
	if err := a.save(); err != nil {
		a.devices = a.devices[:len(a.devices)-1]
		return nil, "", err
	}
	return d, token, nil
}

// revoke removes the device and closes its open sockets at once; a failed save puts it back.
func (a *Auth) revoke(id string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, d := range a.devices {
		if d.ID == id {
			before := a.devices
			a.devices = append(append([]*device{}, before[:i]...), before[i+1:]...)
			if err := a.save(); err != nil {
				a.devices = before
				return true, err
			}
			a.closeSockets(id)
			return true, nil
		}
	}
	return false, nil
}

func (a *Auth) revokeAll() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	before := a.devices
	a.devices = nil
	if err := a.save(); err != nil {
		a.devices = before
		return err
	}
	for s := range a.socks {
		s.cancel()
	}
	return nil
}
