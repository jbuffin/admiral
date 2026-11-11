package moor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func wakeDir(home string) string { return filepath.Join(dir(home), "wake") }

// refuseLink fails on a symlink at path; a path that does not exist is fine.
func refuseLink(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("moor: " + path + " is a symlink")
	}
	return nil
}

// ReadWake reads the ship's wake state; a ship with none has the zero state.
func ReadWake(home, name string) (WakeState, error) {
	if err := checkName(name); err != nil {
		return WakeState{}, err
	}
	path := filepath.Join(wakeDir(home), name+".json")
	if err := refuseLink(wakeDir(home)); err != nil {
		return WakeState{}, err
	}
	if err := refuseLink(path); err != nil {
		return WakeState{}, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return WakeState{}, nil
	}
	if err != nil {
		return WakeState{}, err
	}
	var s WakeState
	if err := json.Unmarshal(b, &s); err != nil {
		return WakeState{}, err
	}
	return s, nil
}

// WriteWake replaces the ship's wake state atomically, at <home>/moored/wake/<name>.json, and refuses a symlink.
func WriteWake(home, name string, s WakeState) error {
	if err := checkName(name); err != nil {
		return err
	}
	d := wakeDir(home)
	if err := os.MkdirAll(dir(home), 0o700); err != nil {
		return err
	}
	if err := refuseLink(d); err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	path := filepath.Join(d, name+".json")
	if err := refuseLink(path); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d, ".wake-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(b, '\n'))
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// RemoveWake deletes the ship's wake state; one already gone is no error.
func RemoveWake(home, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(wakeDir(home), name+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
