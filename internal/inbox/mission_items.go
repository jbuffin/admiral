package inbox

import (
	"encoding/json"
	"errors"
	"fmt"

	"slices"

	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/crewapi"
)

// KindMission is a mission a ship proposed; like the crew kinds it lives only in Admiral's own inbox.
const KindMission = "mission"

const maxMissionBytes = 65536

// MissionObject is the mission object of an item. Repo is null for a fleet-scope proposal.
type MissionObject struct {
	Fleet   string  `json:"fleet"`
	Ship    string  `json:"ship"`
	RunDate string  `json:"run_date"`
	Name    string  `json:"name"`
	Scope   string  `json:"scope"`
	Repo    *string `json:"repo"`
	Text    string  `json:"text"`
}

// shapeOK holds what the item must have to be shown; the built-in name check is Keep's.
func (o MissionObject) shapeOK() bool {
	return o.Fleet != "" && crew.ValidName(o.Name) && o.Text != "" && len(o.Text) <= maxMissionBytes &&
		(o.Scope == "fleet" || (o.Scope == "repo" && o.Repo != nil && repoOK(*o.Repo)))
}

// MissionObject is the object of a mission item.
func (it Item) MissionObject() (MissionObject, bool) {
	var o MissionObject
	if it.Kind != KindMission {
		return o, false
	}
	return o, json.Unmarshal(it.fields[it.Kind], &o) == nil && o.shapeOK()
}

type reportMission struct {
	Name  string  `json:"name"`
	Scope string  `json:"scope"`
	Repo  *string `json:"repo"`
	Text  string  `json:"text"`
}

// writeMissionItems writes one item for each valid mission proposal in the report, skipping a bad one alone.
func writeMissionItems(run CrewRun, fleet FleetInfo, report []byte, skip func(string, ...any)) error {
	var r struct {
		Missions []json.RawMessage `json:"missions"`
	}
	if json.Unmarshal(report, &r) != nil {
		skip("missions is not an array")
		return nil
	}
	var errs []error
	for _, raw := range r.Missions {
		var m reportMission
		if json.Unmarshal(raw, &m) != nil {
			skip("a mission is not an object of the schema")
			continue
		}
		o := MissionObject{Fleet: fleet.Key, Ship: run.Ship, RunDate: run.RunDate, Name: m.Name, Scope: m.Scope, Text: m.Text}
		if m.Scope == "repo" {
			o.Repo = m.Repo
		}
		if !o.shapeOK() || slices.Contains(crewapi.BuiltInMissions, m.Name) {
			skip("mission %q is not valid", m.Name)
			continue
		}
		errs = append(errs, publishAdmiralItem(run.Home, "mission-"+itemHash(run.Session, run.Launched, m.Name), map[string]any{
			"kind": KindMission, "title": fmt.Sprintf("Keep mission %s?", m.Name), KindMission: o,
		}))
	}
	return errors.Join(errs...)
}
