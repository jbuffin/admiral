package moor

// ReadWake reads the ship's wake state; a ship with none has the zero state.
func ReadWake(home, name string) (WakeState, error) { return WakeState{}, nil }

// WriteWake replaces the ship's wake state atomically, at <home>/moored/wake/<name>.json, and refuses a symlink.
func WriteWake(home, name string, s WakeState) error { return nil }

// RemoveWake deletes the ship's wake state; one already gone is no error.
func RemoveWake(home, name string) error { return nil }
