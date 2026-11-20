package config

import "regexp"

// modelName is one argv element and never dash-led; a bracketed suffix such as opus[1m] is one harness alias.
var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*(\[[A-Za-z0-9]+\])?$`)

// ValidModelName reports whether name is a model name Admiral passes to a harness as one argv element.
func ValidModelName(name string) bool { return modelName.MatchString(name) }

// HarnessModels reports a fleet's dock's models: the listed ones with listed true, none when the dock has no list.
type HarnessModels func(fleet, harness string) (models []string, listed bool)

// SetHarnessModels injects the per-fleet model lists; it survives a reload.
func (m *Manager) SetHarnessModels(h HarnessModels) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.models = h
	m.cur.HarnessModels = h
}

// NotifyModelsChanged fires the Subscribe channels, for a list that changed outside a reload.
func (m *Manager) NotifyModelsChanged() { m.notify() }

// ModelsFor is the models a fleet's dock offers and where they came from: the harness's own list when it has been listed, else the built-in one.
func (c *Config) ModelsFor(fleet, harness string) (models []string, listed bool) {
	if c.HarnessModels != nil {
		if models, listed = c.HarnessModels(fleet, harness); listed && len(models) > 0 {
			return models, true
		}
	}
	return ChatModelsFor(harness), false
}
