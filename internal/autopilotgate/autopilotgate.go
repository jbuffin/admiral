// Package autopilotgate decides whether a harness may carry autopilot mode in a fleet:
// only when its guard probe passed there.
package autopilotgate

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/probes"
)

// Refusal is the error Check returns for a harness whose guard is not proven.
type Refusal struct {
	Fleet   string // the fleet's key
	Harness string
}

func (e *Refusal) Error() string {
	return fmt.Sprintf("autopilot is not proven on %s in fleet %s: its guard probe has not passed", e.Harness, e.Fleet)
}

// Proven reports whether the guard probe passed for harness in the fleet, against the
// last harness version detected there. It reads the store fresh each time, and an
// unreadable or corrupt store is not proven.
func Proven(home, fleetKey, harness string) bool {
	// The store may only narrow what the code allows.
	if !slices.Contains(probes.GuardHarnesses(), harness) {
		return false
	}
	return capabilities.Open(home).Proven(fleetKey, harness, capabilities.AutopilotGuard)
}

// Check returns nil when Proven. Otherwise it returns a *Refusal and posts one Inbox
// notice, unless an open one already names this fleet and harness.
func Check(home, fleetKey, fleetName, harness string) error {
	if Proven(home, fleetKey, harness) {
		return nil
	}
	title := fmt.Sprintf("Couldn't launch unattended on %s: guard not proven", titleCase(harness))
	body := fmt.Sprintf("Fleet %s (%s): the guard probe has not passed for %s, so autopilot is not allowed there. Run the capability check for it, then launch again.", fleetName, fleetKey, harness)
	if !noticeOpen(home, title, body) {
		// The refusal stands whether or not the notice lands.
		_, _ = inbox.WriteNotice(home, title, body)
	}
	return &Refusal{Fleet: fleetKey, Harness: harness}
}

func noticeOpen(home, title, body string) bool {
	for _, it := range inbox.OpenNotices(home) {
		var t string
		if raw := it.Field("title"); raw != nil {
			t = strings.Trim(string(raw), `"`)
		}
		if t == title && it.NoticeBody() == body {
			return true
		}
	}
	return false
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return strings.ToUpper(string(r)) + s[n:]
}
