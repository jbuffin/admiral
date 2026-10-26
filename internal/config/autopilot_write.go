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
	// ErrInvalid says config.json doesn't load, so a write is refused.
	ErrInvalid = errors.New("config.json is invalid")
	// ErrChanged says the panel's entry in the file isn't the one the caller read.
	ErrChanged = errors.New("the config changed; reopen")
	// ErrNoSource says the file has no such source.
	ErrNoSource = errors.New("no such source")
)

// invalidError is a load error that also matches ErrInvalid.
type invalidError struct{ err error }

func (e invalidError) Error() string        { return e.err.Error() }
func (e invalidError) Is(target error) bool { return target == ErrInvalid }
func (e invalidError) Unwrap() error        { return e.err }

// entryVersion is the opaque version of a panel's entry: a hash of its compacted
// JSON, a fixed value when there is none.
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

// panelEntry finds one panel's entry in the file's bytes: the sources, the
// source and its autopilot object on the way. A missing entry is nil.
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

// ReadPanelAutopilot is one panel's autopilot entry as the file holds it (nil when
// there is none) and its version, read from the file and not from the loaded
// config. A file that doesn't load is ErrInvalid.
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

// SetPanelAutopilot sets one panel's autopilot entry in dir/config.json, or
// removes it when entry is nil, and says whether it wrote. It refuses with
// ErrChanged when the file's entry isn't the one version names, and writes
// nothing when the entry already equals the file's. The result must load; check
// then sees the entry the file held and the config about to be written, before
// the file is replaced. Every other key and entry stays as it is.
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

// ReadPanelAutopilot reads one panel's entry from the file; see the package function.
func (m *Manager) ReadPanelAutopilot(source, panel string) (json.RawMessage, string, error) {
	return ReadPanelAutopilot(filepath.Dir(m.path), source, panel)
}

// SetPanelAutopilot writes one panel's entry and reloads before it returns.
func (m *Manager) SetPanelAutopilot(source, panel string, entry json.RawMessage, version string, check func(old json.RawMessage, cfg *Config) error) (bool, error) {
	wrote, err := SetPanelAutopilot(filepath.Dir(m.path), source, panel, entry, version, check)
	if wrote {
		m.reload()
	}
	return wrote, err
}
