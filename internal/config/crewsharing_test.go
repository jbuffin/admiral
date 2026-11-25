package config_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// crewSharingOf is the fleet's crew_sharing object as a client reads it, or nil when the view has none.
func crewSharingOf(t *testing.T, m *config.Manager, fleet string) map[string]any {
	t.Helper()
	raw, err := json.Marshal(m.View())
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Fleets []map[string]any `json:"fleets"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, f := range v.Fleets {
		if f["key"] == fleet {
			cs, _ := f["crew_sharing"].(map[string]any)
			return cs
		}
	}
	t.Errorf("no fleet %q in the view: %s", fleet, raw)
	return nil
}

func TestACrewSharingObjectOnAFleetShowsInTheView(t *testing.T) {
	m, _ := openView(t, withFleets(`"work":{"name":"Work","crew_sharing":{"enabled":true,"share_repo":"octocat/admiral-crew","sources":["octocat/admiral-crew","hubot/crew"]}}`))
	cs := crewSharingOf(t, m, "work")
	want := map[string]any{"enabled": true, "share_repo": "octocat/admiral-crew", "sources": []any{"octocat/admiral-crew", "hubot/crew"}}
	if !reflect.DeepEqual(cs, want) {
		t.Errorf("crew_sharing = %v, want %v", cs, want)
	}
}

func TestAFleetWithoutCrewSharingHasItDisabledWithNoSources(t *testing.T) {
	m, _ := openView(t, withFleets(`"work":{"name":"Work"}`))
	cs := crewSharingOf(t, m, "work")
	if cs == nil || cs["enabled"] != false || len(cs["sources"].([]any)) != 0 {
		t.Errorf("crew_sharing = %v, want enabled false and no sources", cs)
	}
	if v, ok := cs["share_repo"]; !ok || v != "" && v != nil {
		t.Errorf("share_repo = %v (present %v), want empty", v, ok)
	}
}

func TestACrewSharingObjectWithABadValueIsRefusedNamingTheField(t *testing.T) {
	for field, object := range map[string]string{
		"fleets.work.crew_sharing.nope":       `{"enabled":true,"nope":1}`,
		"fleets.work.crew_sharing.share_repo": `{"share_repo":"octocat"}`,
		"fleets.work.crew_sharing.sources":    `{"sources":["a/b","a/b"]}`,
		"fleets.work.crew_sharing.sources[1]": `{"sources":["octo/crew","Octo/Crew"]}`,
		"fleets.work.crew_sharing.enabled":    `{"enabled":"yes"}`,
	} {
		_, err := config.Parse([]byte(withFleets(`"work":{"name":"Work","crew_sharing":` + object + `}`)))
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("Parse(%s) error = %v, want one naming %s", object, err, field)
		}
	}
}
