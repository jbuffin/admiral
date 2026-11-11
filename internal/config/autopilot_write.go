package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
)

var (
	ErrInvalid  = errors.New("config.json is invalid")
	ErrChanged  = errors.New("the config changed; reopen")
	ErrNoSource = errors.New("no such source")
)

type invalidError struct{ err error }

func (e invalidError) Error() string        { return e.err.Error() }
func (e invalidError) Is(target error) bool { return target == ErrInvalid }
func (e invalidError) Unwrap() error        { return e.err }

func entryVersion(raw json.RawMessage) string {
	if raw == nil {
		return "none"
	}
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		b.Write(raw)
	}
	sum := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(sum[:8])
}

// sameJSON compares two JSON documents as values, so key order and layout don't count.
func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func panelEntry(data []byte, source, panel string) (sources, src, ap *Object, entry json.RawMessage, err error) {
	top, err := ParseObject(data)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	raw, _ := top.Get("sources")
	if raw == nil {
		raw = json.RawMessage(`{}`)
	}
	if sources, err = ParseObject(raw); err != nil {
		return nil, nil, nil, nil, err
	}
	raw, ok := sources.Get(source)
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("%w: %s", ErrNoSource, source)
	}
	if src, err = ParseObject(raw); err != nil {
		return nil, nil, nil, nil, err
	}
	ap = NewObject()
	if raw, ok := src.Get("autopilot"); ok {
		if ap, err = ParseObject(raw); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	entry, _ = ap.Get(panel)
	return sources, src, ap, entry, nil
}

// ReadPanelAutopilot reads the file, not the loaded config.
func ReadPanelAutopilot(dir, source, panel string) (json.RawMessage, string, error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	path := filepath.Join(dir, FileName)
	data, err := readConfig(path)
	if err != nil {
		return nil, "", err
	}
	if _, err := Parse(data); err != nil {
		return nil, "", invalidError{fmt.Errorf("%s is invalid, fix it first: %w", path, err)}
	}
	_, _, _, entry, err := panelEntry(data, source, panel)
	return entry, entryVersion(entry), err
}

// SetPanelAutopilot removes the entry when it is nil. check runs on the old entry
// and the config about to be written, before the file is replaced.
func SetPanelAutopilot(dir, source, panel string, entry json.RawMessage, version string, check func(old json.RawMessage, cfg *Config) error) (bool, error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	path := filepath.Join(dir, FileName)
	data, err := readConfig(path)
	if err != nil {
		return false, err
	}
	if _, err := Parse(data); err != nil {
		return false, invalidError{fmt.Errorf("%s is invalid, fix it first: %w", path, err)}
	}
	sources, src, ap, old, err := panelEntry(data, source, panel)
	if err != nil {
		return false, err
	}
	if entryVersion(old) != version {
		return false, ErrChanged
	}
	if entry == nil {
		if old == nil {
			return false, nil
		}
		ap.Delete(panel)
	} else {
		if old != nil && sameJSON(old, entry) {
			return false, nil
		}
		ap.Set(panel, entry)
	}
	if ap.Len() == 0 {
		src.Delete("autopilot")
	} else {
		src.Set("autopilot", ap.JSON())
	}
	sources.Set(source, src.JSON())
	_, out, err := renderKey(path, "sources", sources.JSON())
	if err != nil {
		return false, err
	}
	cfg, err := Parse(out)
	if err != nil {
		return false, err
	}
	if check != nil {
		if err := check(old, cfg); err != nil {
			return false, err
		}
	}
	return true, replaceFile(path, out)
}

func (m *Manager) ReadPanelAutopilot(source, panel string) (json.RawMessage, string, error) {
	return ReadPanelAutopilot(filepath.Dir(m.path), source, panel)
}

// SetPanelAutopilot reloads before it returns.
func (m *Manager) SetPanelAutopilot(source, panel string, entry json.RawMessage, version string, check func(old json.RawMessage, cfg *Config) error) (bool, error) {
	wrote, err := SetPanelAutopilot(filepath.Dir(m.path), source, panel, entry, version, check)
	if wrote {
		m.reload()
	}
	return wrote, err
}
