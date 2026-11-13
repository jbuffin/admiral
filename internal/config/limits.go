package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jbuffin/admiral/internal/standing"
)

// Bounds a Limit may take; a hand-edited file beyond them fails the config.
const (
	maxListEntries    = 5000
	maxWindows        = 100
	maxLaunchRate     = 1000
	maxConcurrent     = 100
	maxWallClockMins  = 1440
	maxSpendUSD       = 10000
	maxThresholdCount = 100000
	maxThresholdDays  = 365
)

var (
	clockRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	// owner/name as repofinder.Identity reads it, without the host.
	repoRe     = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	weekdayIDs = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
)

func parsePreset(path string, raw json.RawMessage) (standing.Preset, error) {
	s, err := str(path, raw)
	if err != nil {
		return "", err
	}
	if p := standing.Preset(s); p == standing.Work || p == standing.Home {
		return p, nil
	}
	return "", fmt.Errorf("%s: must be %q or %q, got %q", path, standing.Work, standing.Home, s)
}

// parseLimits layers the file's Limits per leaf over the preset's values, so a fleet with none reads as its preset.
func parseLimits(path string, raw json.RawMessage, preset standing.Preset) (standing.Limits, error) {
	l := standing.PresetLimits(preset)
	ms, err := members(path, raw)
	if err != nil {
		return l, err
	}
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "ceilings":
			err = parseCeilings(p, m.raw, &l.Ceilings)
		case "sender_allowlist":
			l.SenderAllowlist, err = textList(p, m.raw)
		case "rates":
			err = parseRates(p, m.raw, &l.Rates)
		case "quiet_hours":
			l.QuietHours, err = parseWindows(p, m.raw)
		case "repo_allowlist":
			l.RepoAllowlist, err = parseRepos(p, m.raw)
		case "wall_clock_minutes":
			l.WallClockMinutes, err = boundedInt(p, m.raw, 1, maxWallClockMins)
		case "spend":
			err = parseSpend(p, m.raw, &l.Spend)
		case "quota_hold_percent":
			l.QuotaHoldPercent, err = boundedInt(p, m.raw, 1, 100)
		case "write_deny":
			l.WriteDeny, err = parseGlobs(p, m.raw)
		case "skip":
			err = parseSkip(p, m.raw, &l.Skip)
		default:
			err = fmt.Errorf("%s: unknown key", p)
		}
		if err != nil {
			return l, err
		}
	}
	return l, nil
}

func parseCeilings(path string, raw json.RawMessage, c *standing.Ceilings) error {
	ms, err := members(path, raw)
	if err != nil {
		return err
	}
	for _, m := range ms {
		p := path + "." + m.key
		var dst *standing.Ceiling
		switch m.key {
		case "t1":
			dst = &c.T1
		case "t2":
			dst = &c.T2
		case "t3":
			dst = &c.T3
		case "t4":
			dst = &c.T4
		case "fleet":
			dst = &c.Fleet
		default:
			return fmt.Errorf("%s: unknown key", p)
		}
		if *dst, err = parseCeiling(p, m.key, m.raw); err != nil {
			return err
		}
	}
	return nil
}

func parseLevel(path string, raw json.RawMessage) (standing.Level, error) {
	s, err := str(path, raw)
	if err != nil {
		return 0, err
	}
	l, ok := standing.ParseLevel(s)
	if !ok {
		return 0, fmt.Errorf("%s: must be A0, A1, A3, A4 or A5 (there is no A2), got %q", path, s)
	}
	return l, nil
}

func parseRestricted(path string, raw json.RawMessage) (standing.Restricted, error) {
	s, err := str(path, raw)
	if err != nil {
		return "", err
	}
	if r := standing.Restricted(s); r == standing.Investigate || r == standing.DraftFix {
		return r, nil
	}
	return "", fmt.Errorf("%s: must be %q or %q, got %q", path, standing.Investigate, standing.DraftFix, s)
}

// checkRestricted requires a restricted level on an A3 and refuses one anywhere else.
func checkRestricted(path string, level standing.Level, r standing.Restricted) error {
	switch {
	case level == standing.A3 && r == "":
		return fmt.Errorf("%s.restricted: required at A3, one of %q or %q", path, standing.Investigate, standing.DraftFix)
	case level != standing.A3 && r != "":
		return fmt.Errorf("%s.restricted: only an A3 ceiling has a restricted level", path)
	}
	return nil
}

func parseCeiling(path, tier string, raw json.RawMessage) (standing.Ceiling, error) {
	ms, err := members(path, raw)
	if err != nil {
		return standing.Ceiling{}, err
	}
	var c standing.Ceiling
	var thresholdRaw json.RawMessage
	haveLevel := false
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "level":
			haveLevel = true
			if c.Level, err = parseLevel(p, m.raw); err != nil {
				return c, err
			}
			if tier == "t3" && c.Level > standing.A1 {
				return c, fmt.Errorf("%s: T3 is capped at A1, got %s", p, c.Level)
			}
		case "restricted":
			if c.Restricted, err = parseRestricted(p, m.raw); err != nil {
				return c, err
			}
		case "asks_first":
			if tier != "t1" {
				return c, fmt.Errorf("%s: only the T1 ceiling asks first", p)
			}
			if c.AsksFirst, err = boolean(p, m.raw); err != nil {
				return c, err
			}
		case "threshold":
			if tier != "t4" {
				return c, fmt.Errorf("%s: only the T4 ceiling has a threshold", p)
			}
			thresholdRaw = m.raw
		default:
			return c, fmt.Errorf("%s: unknown key", p)
		}
	}
	if !haveLevel {
		return c, fmt.Errorf("%s.level: required", path)
	}
	if err := checkRestricted(path, c.Level, c.Restricted); err != nil {
		return c, err
	}
	if thresholdRaw != nil {
		if c.Threshold, err = parseT4Threshold(path+".threshold", thresholdRaw); err != nil {
			return c, err
		}
	}
	return c, nil
}

func parseT4Threshold(path string, raw json.RawMessage) (*standing.Threshold, error) {
	ms, err := members(path, raw)
	if err != nil {
		return nil, err
	}
	t := &standing.Threshold{}
	have := map[string]bool{}
	for _, m := range ms {
		p := path + "." + m.key
		have[m.key] = true
		switch m.key {
		case "events":
			t.Events, err = boundedInt(p, m.raw, 1, maxThresholdCount)
		case "days":
			t.Days, err = boundedInt(p, m.raw, 1, maxThresholdDays)
		case "level":
			t.Level, err = parseLevel(p, m.raw)
		case "restricted":
			t.Restricted, err = parseRestricted(p, m.raw)
		default:
			err = fmt.Errorf("%s: unknown key", p)
		}
		if err != nil {
			return nil, err
		}
	}
	for _, k := range []string{"events", "days", "level"} {
		if !have[k] {
			return nil, fmt.Errorf("%s.%s: required", path, k)
		}
	}
	return t, checkRestricted(path, t.Level, t.Restricted)
}

func parseRates(path string, raw json.RawMessage, r *standing.Rates) error {
	ms, err := members(path, raw)
	if err != nil {
		return err
	}
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "launches_per_hour":
			r.LaunchesPerHour, err = boundedInt(p, m.raw, 1, maxLaunchRate)
		case "launches_per_sender_per_day":
			r.LaunchesPerSenderPerDay, err = boundedInt(p, m.raw, 1, maxLaunchRate)
		case "concurrent":
			r.Concurrent, err = boundedInt(p, m.raw, 1, maxConcurrent)
		default:
			err = fmt.Errorf("%s: unknown key", p)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func parseSpend(path string, raw json.RawMessage, s *standing.Spend) error {
	ms, err := members(path, raw)
	if err != nil {
		return err
	}
	for _, m := range ms {
		p := path + "." + m.key
		var dst *float64
		switch m.key {
		case "lookout_call_usd":
			dst = &s.LookoutCallUSD
		case "daily_worker_usd":
			dst = &s.DailyWorkerUSD
		case "reviewer_call_usd":
			dst = &s.ReviewerCallUSD
		default:
			return fmt.Errorf("%s: unknown key", p)
		}
		if *dst, err = dollars(p, m.raw); err != nil {
			return err
		}
	}
	return nil
}

func boundedInt(path string, raw json.RawMessage, lo, hi int) (int, error) {
	n, err := strconv.Atoi(string(bytes.TrimSpace(raw)))
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s: must be a whole number from %d to %d, got %s", path, lo, hi, raw)
	}
	return n, nil
}

// dollars reads a non-negative amount in whole cents.
func dollars(path string, raw json.RawMessage) (float64, error) {
	var v float64
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] == '"' || json.Unmarshal(t, &v) != nil || v < 0 || v > maxSpendUSD ||
		math.Abs(v*100-math.Round(v*100)) > 1e-6 {
		return 0, fmt.Errorf("%s: must be an amount in dollars from 0 to %d, in whole cents, got %s", path, maxSpendUSD, raw)
	}
	return v, nil
}

// entries decodes a JSON array, refusing one over the size bound.
func entries(path string, raw json.RawMessage) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '[' || json.Unmarshal(t, &items) != nil {
		return nil, fmt.Errorf("%s: must be an array", path)
	}
	if len(items) > maxListEntries {
		return nil, fmt.Errorf("%s: at most %d entries allowed, got %d", path, maxListEntries, len(items))
	}
	return items, nil
}

// textList reads non-empty, distinct strings, checking each with ok.
func textList(path string, raw json.RawMessage, checks ...func(string) string) ([]string, error) {
	items, err := entries(path, raw)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for i, it := range items {
		p := fmt.Sprintf("%s[%d]", path, i)
		s, err := str(p, it)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("%s: must not be empty", p)
		}
		for _, check := range checks {
			if msg := check(s); msg != "" {
				return nil, fmt.Errorf("%s: %s, got %q", p, msg, s)
			}
		}
		if slices.Contains(out, s) {
			return nil, fmt.Errorf("%s: %q is listed twice", p, s)
		}
		out = append(out, s)
	}
	return out, nil
}

func parseRepos(path string, raw json.RawMessage) ([]string, error) {
	return textList(path, raw, func(s string) string {
		if !repoRe.MatchString(s) {
			return "must look like owner/name"
		}
		return ""
	})
}

func parseGlobs(path string, raw json.RawMessage) ([]string, error) {
	return textList(path, raw, func(s string) string {
		switch {
		case filepath.IsAbs(s) || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~"):
			return "must be relative to the repo"
		case slices.Contains(strings.Split(filepath.ToSlash(s), "/"), ".."):
			return "must stay inside the repo, with no .."
		}
		return ""
	})
}

func parseWindows(path string, raw json.RawMessage) ([]standing.Window, error) {
	items, err := entries(path, raw)
	if err != nil {
		return nil, err
	}
	if len(items) > maxWindows {
		return nil, fmt.Errorf("%s: at most %d windows allowed, got %d", path, maxWindows, len(items))
	}
	out := make([]standing.Window, 0, len(items))
	for i, it := range items {
		w, err := parseWindow(fmt.Sprintf("%s[%d]", path, i), it)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

func clock(path string, raw json.RawMessage) (string, error) {
	s, err := str(path, raw)
	if err != nil {
		return "", err
	}
	if !clockRe.MatchString(s) {
		return "", fmt.Errorf("%s: must be a time as HH:MM, got %q", path, s)
	}
	return s, nil
}

func parseWindow(path string, raw json.RawMessage) (standing.Window, error) {
	ms, err := members(path, raw)
	if err != nil {
		return standing.Window{}, err
	}
	w := standing.Window{Weekdays: []string{}}
	have := map[string]bool{}
	for _, m := range ms {
		p := path + "." + m.key
		have[m.key] = true
		switch m.key {
		case "start":
			w.Start, err = clock(p, m.raw)
		case "end":
			w.End, err = clock(p, m.raw)
		case "weekdays":
			w.Weekdays, err = parseWeekdays(p, m.raw)
		default:
			err = fmt.Errorf("%s: unknown key", p)
		}
		if err != nil {
			return w, err
		}
	}
	for _, k := range []string{"start", "end", "weekdays"} {
		if !have[k] {
			return w, fmt.Errorf("%s.%s: required", path, k)
		}
	}
	if w.Start == w.End {
		return w, fmt.Errorf("%s: start and end must differ", path)
	}
	return w, nil
}

func parseWeekdays(path string, raw json.RawMessage) ([]string, error) {
	days, err := stringList(path, raw)
	if err != nil {
		return nil, err
	}
	if len(days) == 0 {
		return nil, fmt.Errorf("%s: name at least one weekday", path)
	}
	for i, d := range days {
		if !slices.Contains(weekdayIDs, d) {
			return nil, fmt.Errorf("%s[%d]: must be one of %s, got %q", path, i, strings.Join(weekdayIDs, ", "), d)
		}
		if slices.Contains(days[:i], d) {
			return nil, fmt.Errorf("%s[%d]: %q is listed twice", path, i, d)
		}
	}
	return days, nil
}

func parseSkip(path string, raw json.RawMessage, s *standing.Skip) error {
	ms, err := members(path, raw)
	if err != nil {
		return err
	}
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "labels":
			s.Labels, err = parseScoped(p, m.raw)
		case "senders":
			s.Senders, err = parseScoped(p, m.raw)
		case "dedupe_keys":
			s.DedupeKeys, err = parseScoped(p, m.raw)
		default:
			err = fmt.Errorf("%s: unknown key", p)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func parseScoped(path string, raw json.RawMessage) ([]standing.Scoped, error) {
	items, err := entries(path, raw)
	if err != nil {
		return nil, err
	}
	out := make([]standing.Scoped, 0, len(items))
	for i, it := range items {
		p := fmt.Sprintf("%s[%d]", path, i)
		ms, err := members(p, it)
		if err != nil {
			return nil, err
		}
		var e standing.Scoped
		for _, m := range ms {
			switch m.key {
			case "value":
				e.Value, err = str(p+".value", m.raw)
			case "routine":
				e.Routine, err = str(p+".routine", m.raw)
			default:
				err = fmt.Errorf("%s.%s: unknown key", p, m.key)
			}
			if err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(e.Value) == "" {
			return nil, fmt.Errorf("%s.value: required and not empty", p)
		}
		if slices.Contains(out, e) {
			return nil, fmt.Errorf("%s: listed twice", p)
		}
		out = append(out, e)
	}
	return out, nil
}
