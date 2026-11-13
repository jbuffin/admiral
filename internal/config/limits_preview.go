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

// PreviewLimits is the package PreviewLimits plus the repo-membership check a save runs.
func (m *Manager) PreviewLimits(key string, f FleetFields) (LimitsPreview, error) {
	m.mu.Lock()
	repos := m.repos
	m.mu.Unlock()
	if err := checkLimitsMembership(m.Current(), key, f, repos); err != nil {
		return LimitsPreview{}, err
	}
	return PreviewLimits(filepath.Dir(m.path), key, f)
}

// planCopy gives fleet to the preset and Limits of from, read from the objects it is handed, and returns the target's effective Limits before and after.
// The target's own stored Limits are replaced, so none survive; its repo allowlist is checked against its own search roots.
func planCopy(fleets, fleet *Object, from, to string, repos RepoLookup) (before, after standing.Limits, err error) {
	raw, ok := fleets.Get(from)
	if !ok {
		return before, after, fmt.Errorf("%w %q", ErrNoFleet, from)
	}
	source, err := ParseObject(raw)
	if err != nil {
		return before, after, fmt.Errorf("%w: %w", ErrConflict, err)
	}
	srcPreset, srcLimits, err := resolveLimits(from, source)
	if err != nil {
		return before, after, err
	}
	dstPreset, before, err := resolveLimits(to, fleet)
	if err != nil {
		return before, after, err
	}
	for _, k := range []string{"preset", "limits"} {
		if v, ok := source.Get(k); ok {
			fleet.Set(k, v)
		} else {
			fleet.Delete(k)
		}
	}
	if repos != nil && len(srcLimits.RepoAllowlist) > 0 {
		var stored []string
		if srcPreset == dstPreset {
			stored = before.RepoAllowlist
		}
		given, _ := json.Marshal(srcLimits.RepoAllowlist)
		if err := checkRepoMembership(to, given, stored, repos(to)); err != nil {
			return before, after, err
		}
	}
	_, after, err = resolveLimits(to, fleet)
	return before, after, err
}

// PreviewCopyLimits is CopyLimits without the write: it returns what the copy would refuse, or the Limits it would leave.
func (m *Manager) PreviewCopyLimits(from, to string) (before, after standing.Limits, err error) {
	m.mu.Lock()
	repos := m.repos
	m.mu.Unlock()
	_, fleets, fleet, err := loadFleet(filepath.Dir(m.path), to)
	if err != nil {
		return before, after, err
	}
	return planCopy(fleets, fleet, from, to, repos)
}

// CopyLimits gives fleet to the stored preset and Limits of fleet from, and returns the target's effective Limits before and after.
// Both fleets are read from the file under the write lock; the repo scan of the target's search roots also runs under it.
func (m *Manager) CopyLimits(from, to string) (before, after standing.Limits, err error) {
	m.mu.Lock()
	repos := m.repos
	m.mu.Unlock()
	err = rewriteFleet(filepath.Dir(m.path), to, func(fleets, fleet *Object) error {
		var err error
		before, after, err = planCopy(fleets, fleet, from, to, repos)
		return err
	})
	if err != nil {
		return standing.Limits{}, standing.Limits{}, err
	}
	m.reload()
	return before, after, nil
}
