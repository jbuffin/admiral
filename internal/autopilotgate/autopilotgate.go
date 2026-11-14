// Package autopilotgate decides whether a harness may carry autopilot mode in a fleet:
// only when its guard probe passed there.
package autopilotgate

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/judge"
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

// RestrictedCapable reports whether some harness of the fleet can run a restricted ship: both
// restricted capabilities proven, plus the guard and the fleet's advisor that every restricted ship
// runs with. It reads the store fresh, and an unreadable store is not capable.
func RestrictedCapable(home string, fleet config.Fleet) bool {
	store := capabilities.Open(home)
	for _, h := range probes.RestrictedHarnesses() {
		if _, ok := fleet.Harness(h); !ok {
			continue
		}
		if store.Proven(fleet.Key, h, capabilities.RestrictedSandbox) && store.Proven(fleet.Key, h, capabilities.RestrictedAllow) && Proven(home, fleet.Key, h) {
			ok, _ := Advisor(fleet, store)
			return ok
		}
	}
	return false
}

// AdvisorRefusal is the error Check returns for a fleet with no judge harness, which
// every autopilot ship's advisor runs on.
type AdvisorRefusal struct {
	Fleet   string // the fleet's key
	Harness string
	Reason  string
}

func (e *AdvisorRefusal) Error() string {
	return fmt.Sprintf("autopilot is not proven on %s in fleet %s: its advisor is not proven (%s)", e.Harness, e.Fleet, e.Reason)
}

// Advisor reports whether the fleet has a judge harness to run the advisor on, and why
// not when it has none. It is computed from the store on every call and never stored: it
// depends on every harness of the fleet, so a stored row would go stale.
func Advisor(fleet config.Fleet, store *capabilities.Store) (ok bool, reason string) {
	_, err := judge.Choose(fleet, store)
	if err == nil {
		return true, ""
	}
	var none *judge.NoJudgeHarness
	if errors.As(err, &none) {
		return false, none.Reason
	}
	return false, err.Error()
}

// AdvisorStatus is the Advisor row's state: not checked until some harness of the fleet
// has a stored judge result, then passed (naming the judge harness) or failed with the
// reason. The gate refuses in every state but passed.
func AdvisorStatus(fleet config.Fleet, store *capabilities.Store) (capabilities.Status, string) {
	choice, err := judge.Choose(fleet, store)
	if err == nil {
		return capabilities.Passed, "the fleet's judge harness is " + choice.Name
	}
	for _, h := range fleet.Harnesses {
		snap, _ := store.Snapshot(fleet.Key, h.Name)
		for _, id := range []capabilities.ID{capabilities.JudgeNoTools, capabilities.JudgeNoNetwork} {
			if snap.States[id].Result != nil {
				_, reason := Advisor(fleet, store)
				return capabilities.Failed, reason
			}
		}
	}
	return capabilities.NotChecked, ""
}

// Check returns nil when the guard is Proven and the fleet has an advisor. Otherwise it
// returns a *Refusal (guard first) or an *AdvisorRefusal and posts one Inbox notice,
// unless an open one already says the same.
func Check(home string, fleet config.Fleet, harness string) error {
	if !Proven(home, fleet.Key, harness) {
		title := fmt.Sprintf("Couldn't launch unattended on %s: guard not proven", titleCase(harness))
		body := fmt.Sprintf("Fleet %s (%s): the guard probe has not passed for %s, so autopilot is not allowed there. Run the capability check for it, then launch again.", fleet.Name, fleet.Key, harness)
		postOnce(home, title, body)
		return &Refusal{Fleet: fleet.Key, Harness: harness}
	}
	ok, reason := Advisor(fleet, capabilities.Open(home))
	if ok {
		return nil
	}
	title := fmt.Sprintf("Couldn't launch unattended on %s: advisor not proven", titleCase(harness))
	body := fmt.Sprintf("Fleet %s (%s): no harness in the fleet is proven for judge calls, so the advisor has nothing to run on and autopilot is not allowed. %s. Run the capability check, then launch again.", fleet.Name, fleet.Key, reason)
	postOnce(home, title, body)
	return &AdvisorRefusal{Fleet: fleet.Key, Harness: harness, Reason: reason}
}

// CheckQuiet is Check without the Inbox notice, for a launch whose refusal is shown another way.
func CheckQuiet(home string, fleet config.Fleet, harness string) error {
	if !Proven(home, fleet.Key, harness) {
		return &Refusal{Fleet: fleet.Key, Harness: harness}
	}
	if ok, reason := Advisor(fleet, capabilities.Open(home)); !ok {
		return &AdvisorRefusal{Fleet: fleet.Key, Harness: harness, Reason: reason}
	}
	return nil
}

func postOnce(home, title, body string) {
	if !noticeOpen(home, title, body) {
		// The refusal stands whether or not the notice lands.
		_, _ = inbox.WriteNotice(home, title, body)
	}
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
