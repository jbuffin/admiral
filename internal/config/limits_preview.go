package config

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/jbuffin/admiral/internal/standing"
)

// LimitsPreview is a fleet's Limits as a save of the same fields would leave them.
type LimitsPreview struct {
	Preset    standing.Preset
	Limits    standing.Limits
	Sentences []standing.Sentence
}

// resolveLimits reads a fleet object's preset and Limits the way Parse does, naming paths under fleets.<key>.
func resolveLimits(key string, fleet *Object) (standing.Preset, standing.Limits, error) {
	path := "fleets." + key
	preset := standing.Work
	if raw, ok := fleet.Get("preset"); ok {
		var err error
		if preset, err = parsePreset(path+".preset", raw); err != nil {
			return "", standing.Limits{}, err
		}
	}
	raw, ok := fleet.Get("limits")
	if !ok {
		raw = json.RawMessage("{}")
	}
	l, err := parseLimits(path+".limits", raw, preset)
	return preset, l, err
}

// PreviewLimits layers f's preset and Limits over the fleet's stored ones in dir's config.json without writing.
func PreviewLimits(dir, key string, f FleetFields) (LimitsPreview, error) {
	_, _, fleet, err := loadFleet(dir, key)
	if err != nil {
		return LimitsPreview{}, err
	}
	if err := mergeLimits(fleet, f); err != nil {
		return LimitsPreview{}, err
	}
	preset, l, err := resolveLimits(key, fleet)
	if err != nil {
		return LimitsPreview{}, err
	}
	return LimitsPreview{Preset: preset, Limits: l, Sentences: standing.Sentences(l)}, nil
}

// CopyLimits gives fleet to the stored preset and Limits of fleet from, and returns the target's effective Limits before and after.
// The target's own stored Limits are replaced, so none survive the copy; its repo allowlist is checked against its own search roots.
func (m *Manager) CopyLimits(from, to string) (before, after standing.Limits, err error) {
	m.mu.Lock()
	repos := m.repos
	m.mu.Unlock()
	cfg := m.Current()
	src, ok := cfg.Fleet(from)
	if !ok {
		return before, after, fmt.Errorf("%w %q", ErrNoFleet, from)
	}
	dst, ok := cfg.Fleet(to)
	if !ok {
		return before, after, fmt.Errorf("%w %q", ErrNoFleet, to)
	}
	err = rewriteFleet(filepath.Dir(m.path), to, func(fleets, fleet *Object) error {
		raw, ok := fleets.Get(from)
		if !ok {
			return fmt.Errorf("%w %q", ErrNoFleet, from)
		}
		source, err := ParseObject(raw)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrConflict, err)
		}
		for _, k := range []string{"preset", "limits"} {
			if v, ok := source.Get(k); ok {
				fleet.Set(k, v)
			} else {
				fleet.Delete(k)
			}
		}
		if repos == nil || len(src.Limits.RepoAllowlist) == 0 {
			return nil
		}
		var stored []string
		if src.Preset == dst.Preset {
			stored = dst.Limits.RepoAllowlist
		}
		given, _ := json.Marshal(src.Limits.RepoAllowlist)
		return checkRepoMembership(to, given, stored, repos(to))
	})
	if err != nil {
		return before, after, err
	}
	m.reload()
	now, _ := m.Current().Fleet(to)
	return dst.Limits, now.Limits, nil
}
