package crewapi

import (
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/jbuffin/admiral/internal/crew"
)

// builtInMissions are the names Commodore ships its own missions under; a fleet mission never takes one.
var builtInMissions = []string{"build", "pr-feedback", "review"}

// IsBuiltInMission says whether name is one of Commodore's built-in mission names.
func IsBuiltInMission(name string) bool { return slices.Contains(builtInMissions, name) }

// MissionRefusedError is a Keep that can't be applied, with nothing changed.
type MissionRefusedError struct{ Msg string }

func (e *MissionRefusedError) Error() string { return e.Msg }

// KeepMission writes text to the fleet's missions directory as <name>.md, once: a file already there with the same
// bytes counts as kept (written false), one with other bytes is refused with a *MissionRefusedError.
func KeepMission(home, key, name, text string) (written bool, err error) {
	if !safeKey(key) || !crew.ValidName(name) {
		return false, errors.New("not a mission name")
	}
	if IsBuiltInMission(name) {
		return false, &MissionRefusedError{name + " is a built-in mission; a fleet mission never replaces one"}
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	dir, _, err := walk(home, true, "fleets", key, "missions")
	if err != nil {
		return false, unsafePath(err)
	}
	dst := filepath.Join(dir, name+".md")
	tmp, err := os.CreateTemp(dir, ".mission-*.tmp")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(text)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	// A link fails when the file exists, so a kept mission is never replaced.
	err = os.Link(tmp.Name(), dst)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return false, err
	}
	raw, found, rerr := readRegular(dst)
	switch {
	case rerr != nil:
		return false, unsafePath(rerr)
	case found && string(raw) == text:
		return false, nil
	}
	return false, &MissionRefusedError{"this fleet already has a mission " + name + " with other text; nothing was changed"}
}

// unsafePath turns a link, a non-directory or an unreadable file on the path into a refusal, so it is a conflict and not a server error.
func unsafePath(err error) error {
	if errors.Is(err, errLink) || errors.Is(err, errNotDir) || errors.Is(err, errUnreadable) {
		return &MissionRefusedError{err.Error() + "; nothing was changed"}
	}
	return err
}
