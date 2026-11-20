package config

// Red-phase stubs: the model-list work replaces these.

// ValidModelName reports whether name is a model name Admiral passes to a harness as one argv element.
func ValidModelName(name string) bool { return false }

// HarnessModels reports a fleet's dock's models: the listed ones with listed true, none when the dock has no list.
type HarnessModels func(fleet, harness string) (models []string, listed bool)

// SetHarnessModels injects the per-fleet model lists; it survives a reload.
func (m *Manager) SetHarnessModels(HarnessModels) {}

// NotifyModelsChanged fires the Subscribe channels, for a list that changed outside a reload.
func (m *Manager) NotifyModelsChanged() {}
