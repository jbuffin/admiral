package config

import (
	"errors"

	"github.com/jbuffin/admiral/internal/standing"
)

// LimitsPreview is a fleet's Limits as a save of the same fields would leave them.
type LimitsPreview struct {
	Preset    standing.Preset
	Limits    standing.Limits
	Sentences []standing.Sentence
}

// PreviewLimits layers f's preset and Limits over the fleet's stored ones in dir's config.json without writing.
func PreviewLimits(dir, key string, f FleetFields) (LimitsPreview, error) {
	return LimitsPreview{}, errors.New("not implemented")
}

// CopyLimits gives fleet to the stored preset and Limits of fleet from, and returns the target's effective Limits before and after.
func (m *Manager) CopyLimits(from, to string) (before, after standing.Limits, err error) {
	return before, after, errors.New("not implemented")
}
