package server_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// crewSharing is the fleet's crew_sharing object in GET /api/config, or nil when the view has none.
func (f *fleetFixture) crewSharing(fleet string) map[string]any {
	f.t.Helper()
	code, body := f.do(http.MethodGet, "/api/config", nil)
	if code != http.StatusOK {
		f.t.Fatalf("GET /api/config = %d: %s", code, body)
	}
	var v struct {
		Fleets []map[string]any `json:"fleets"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		f.t.Fatal(err)
	}
	for _, fl := range v.Fleets {
		if fl["key"] == fleet {
			cs, _ := fl["crew_sharing"].(map[string]any)
			return cs
		}
	}
	f.t.Fatalf("no fleet %q", fleet)
	return nil
}

func TestFleetSave_CrewSharingShowsInTheViewRightAwayAndLandsInTheFile(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	if cs := f.crewSharing("work"); cs == nil || cs["enabled"] != false {
		t.Errorf("before a save crew_sharing = %v, want enabled false", cs)
	}
	code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{
		"crew_sharing": map[string]any{"enabled": true, "share_repo": "octocat/admiral-crew", "sources": []string{"octocat/admiral-crew", "hubot/crew"}},
	})
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s, want 200", code, body)
	}
	want := map[string]any{"enabled": true, "share_repo": "octocat/admiral-crew", "sources": []any{"octocat/admiral-crew", "hubot/crew"}}
	if cs := f.crewSharing("work"); !reflect.DeepEqual(cs, want) {
		t.Errorf("crew_sharing = %v, want %v", cs, want)
	}
	if !strings.Contains(f.file(), `"share_repo": "octocat/admiral-crew"`) {
		t.Errorf("config.json lacks share_repo:\n%s", f.file())
	}
}

func TestFleetSave_ALeftOutCrewSharingStaysAsStored(t *testing.T) {
	f := newFleetFixture(t, fleetsFixture)
	f.do(http.MethodPut, "/api/fleets/work", map[string]any{"crew_sharing": map[string]any{"enabled": true, "sources": []string{"hubot/crew"}}})
	if code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"color": "#AABBCC"}); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if cs := f.crewSharing("work"); cs == nil || cs["enabled"] != true {
		t.Errorf("crew_sharing = %v, want it kept", cs)
	}
}

func TestFleetSave_ABadCrewSharingAnswers400NamingTheFieldAndWritesNothing(t *testing.T) {
	for name, c := range map[string]struct {
		object map[string]any
		field  string
	}{
		"a share repo with one segment":   {map[string]any{"share_repo": "octocat"}, "share_repo"},
		"a share repo with a bad segment": {map[string]any{"share_repo": "octocat/.."}, "share_repo"},
		"a share repo with a query":       {map[string]any{"share_repo": "octocat/x?y=1"}, "share_repo"},
		"a duplicate source":              {map[string]any{"sources": []string{"a/b", "a/b"}}, "sources"},
		"a source that is not owner/name": {map[string]any{"sources": []string{"a/b/c"}}, "sources"},
		"an unknown key":                  {map[string]any{"enabled": true, "nope": 1}, "nope"},
		"enabled of the wrong type":       {map[string]any{"enabled": "yes"}, "enabled"},
	} {
		f := newFleetFixture(t, fleetsFixture)
		code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"crew_sharing": c.object})
		if code != http.StatusBadRequest || !strings.Contains(body, c.field) {
			t.Errorf("%s: PUT = %d %s, want 400 naming %s", name, code, body, c.field)
		}
		if f.file() != fleetsFixture {
			t.Errorf("%s: config.json changed", name)
		}
	}
}
