// Package update checks whether a newer public Admiral release exists and reports how to upgrade.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/delay"
)

const (
	defaultBase    = "https://api.github.com"
	releasesPrefix = "https://github.com/jbuffin/admiral/releases/"
	releasePage    = releasesPrefix + "latest"
	// A release's JSON is a few KB; anything near this size is not one.
	maxBody       = 1 << 20
	interval      = 12 * time.Hour
	firstCheck    = time.Minute
	afterTurnedOn = 10 * time.Second
)

// Clock is the checker's source of time; tests drive it by hand.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type Options struct {
	BaseURL string       // the releases API's origin; default https://api.github.com
	Client  *http.Client // default has a request timeout
	Clock   Clock        // default is the real one
	Version string       // the running version
	Exe     string       // the resolved executable path
	// Enabled is read at every tick; Changed (may be nil) says the answer may have changed.
	Enabled func() bool
	Changed <-chan struct{}
}

type Checker struct {
	opts    Options
	install Install

	mu         sync.Mutex
	latest     string
	releaseURL string
	checkedAt  time.Time
}

func New(o Options) *Checker {
	if o.BaseURL == "" {
		o.BaseURL = defaultBase
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.Enabled == nil {
		o.Enabled = func() bool { return true }
	}
	return &Checker{opts: o, install: InstallFor(o.Version, o.Exe)}
}

// Run blocks until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	var timer <-chan time.Time
	on := c.opts.Enabled()
	if on {
		timer = c.opts.Clock.After(delay.From("ADMIRAL_UPDATE_FIRST_CHECK", firstCheck))
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer:
			if !c.opts.Enabled() {
				on, timer = false, nil
				c.forget()
				continue
			}
			c.check(ctx)
			timer = c.opts.Clock.After(interval)
		case <-c.opts.Changed:
			now := c.opts.Enabled()
			switch {
			case now && !on:
				timer = c.opts.Clock.After(afterTurnedOn)
			case !now:
				timer = nil
				c.forget()
			}
			on = now
		}
	}
}

// forget drops the last result so turning the check back on never serves a stale one.
func (c *Checker) forget() {
	c.mu.Lock()
	c.latest, c.releaseURL, c.checkedAt = "", "", time.Time{}
	c.mu.Unlock()
}

type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// check keeps the last good result when this one fails.
func (c *Checker) check(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.opts.BaseURL, "/")+"/repos/jbuffin/admiral/releases/latest", nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "Admiral/"+c.opts.Version+" (+https://github.com/jbuffin/admiral)")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.opts.Client.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&rel); err != nil {
		return
	}
	version, ok := strings.CutPrefix(rel.TagName, "v")
	if !ok || !validVersion(version) {
		return
	}
	url := rel.HTMLURL
	if !strings.HasPrefix(url, releasesPrefix) {
		url = fmt.Sprintf("%stag/v%s", releasesPrefix, version)
	}
	c.mu.Lock()
	c.latest, c.releaseURL, c.checkedAt = version, url, c.opts.Clock.Now()
	c.mu.Unlock()
}

type report struct {
	CurrentVersion  string    `json:"current_version"`
	LatestVersion   *string   `json:"latest_version"`
	UpdateAvailable bool      `json:"update_available"`
	CheckedAt       *string   `json:"checked_at"`
	Enabled         bool      `json:"enabled"`
	InstallMethod   string    `json:"install_method"`
	UpgradeCommands []Command `json:"upgrade_commands"`
	RestartNote     string    `json:"restart_note"`
	ExecutablePath  string    `json:"executable_path"`
	ReleaseURL      string    `json:"release_url"`
}

func (c *Checker) report() report {
	s := report{CurrentVersion: c.opts.Version, Enabled: c.opts.Enabled(), InstallMethod: c.install.Method,
		UpgradeCommands: c.install.Commands, RestartNote: c.install.RestartNote, ExecutablePath: c.opts.Exe, ReleaseURL: releasePage}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !s.Enabled || c.latest == "" {
		return s
	}
	latest, at := c.latest, c.checkedAt.UTC().Format(time.RFC3339)
	s.LatestVersion, s.CheckedAt, s.ReleaseURL = &latest, &at, c.releaseURL
	s.UpdateAvailable = Available(c.opts.Version, latest)
	return s
}

// Routes registers GET /api/update.
func (c *Checker) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/update", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(c.report())
	})
}
