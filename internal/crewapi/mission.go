package crewapi

import "errors"

// MissionRefusedError is a Keep that can't be applied, with nothing changed.
type MissionRefusedError struct{ Msg string }

func (e *MissionRefusedError) Error() string { return e.Msg }

// KeepMission writes text to the fleet's missions directory as <name>.md, once.
func KeepMission(home, key, name, text string) (written bool, err error) {
	return false, errors.New("not implemented")
}
