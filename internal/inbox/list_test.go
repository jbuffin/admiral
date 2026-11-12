package inbox_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/server"
)

// newInbox serves the inbox of a fresh home in which ship "s1" is a known
// claude ship named Dory in the fleet "work" (Work, #38bdf8).
func newInbox(t *testing.T) (*inbox.Service, string, *httptest.Server) {
	t.Helper()
	home := t.TempDir()
	s := &inbox.Service{Home: home, Hub: server.NewHub(), Ship: func(session string) (inbox.ShipInfo, bool) {
		return inbox.ShipInfo{Name: "Dory", Harness: "claude", Fleet: "work", FleetName: "Work", FleetColor: "#38bdf8"}, session == "s1"
	}}
	mux := http.NewServeMux()
	s.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return s, home, srv
}

func shipDir(t *testing.T, home, session string) string {
	t.Helper()
	d := filepath.Join(home, "inbox", session)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

func writeItem(t *testing.T, dir, name string, v any) {
	t.Helper()
	b, ok := v.([]byte)
	if !ok {
		b, _ = json.Marshal(v)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func question(id, created string) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "question", "created": created,
		"title": "Which way?", "order_url": "https://example.com/issues/9",
		"question": map[string]any{"questions": []any{
			map[string]any{"id": "1", "text": "Pick one", "multi": false,
				"options": []any{map[string]any{"label": "Alpha"}, map[string]any{"label": "Beta"}}},
			map[string]any{"id": "2", "text": "Pick some", "multi": true,
				"options": []any{map[string]any{"label": "X"}, map[string]any{"label": "Y"}}},
		}}}
}

func review(id, created string) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "review", "created": created, "title": "Review PR 12",
		"review": map[string]any{"pr_url": "https://example.com/pull/12", "review_id": 123}}
}

func listed(t *testing.T, s *inbox.Service) []map[string]any {
	t.Helper()
	s.Refresh()
	b, _ := json.Marshal(s.List())
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func idsOf(items []map[string]any) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it["id"].(string))
	}
	return out
}

func TestInboxListsItemsOldestFirstWithTheirShip(t *testing.T) {
	s, home, _ := newInbox(t)
	dir := shipDir(t, home, "s1")
	writeItem(t, dir, "r-1.json", review("r-1", "2026-10-22T10:15:00Z"))
	writeItem(t, dir, "q-1.json", question("q-1", "2026-10-22T10:00:00Z"))
	writeItem(t, shipDir(t, home, "ghost"), "g-1.json", review("g-1", "2026-10-22T09:00:00Z"))

	items := listed(t, s)
	if got := idsOf(items); !reflect.DeepEqual(got, []string{"g-1", "q-1", "r-1"}) {
		t.Fatalf("items = %v, want oldest created first", got)
	}
	q := items[1]
	if q["session"] != "s1" || q["name"] != "Dory" || q["harness"] != "claude" || q["title"] != "Which way?" || q["order_url"] != "https://example.com/issues/9" {
		t.Errorf("the question = %v, want its fields and its ship's name and harness", q)
	}
	if g := items[0]; g["session"] != "ghost" || g["name"] != nil || g["harness"] != nil {
		t.Errorf("an item of no known ship = %v, want its session and no name or harness", g)
	}
}

func TestInboxTagsItemsWithTheirShipsFleet(t *testing.T) {
	s, home, _ := newInbox(t)
	writeItem(t, shipDir(t, home, "s1"), "q-1.json", question("q-1", "2026-10-22T10:00:00Z"))

	q := listed(t, s)[0]
	if q["fleet"] != "work" || q["fleet_name"] != "Work" || q["fleet_color"] != "#38bdf8" {
		t.Errorf("the item = %v, want fleet work, fleet_name Work and fleet_color #38bdf8", q)
	}
}

func TestInboxItemFileCannotSetItsFleet(t *testing.T) {
	s, home, _ := newInbox(t)
	known := question("q-1", "2026-10-22T10:00:00Z")
	known["fleet"], known["fleet_name"], known["fleet_color"] = "evil", "Evil", "#ff0000"
	writeItem(t, shipDir(t, home, "s1"), "q-1.json", known)
	unknown := review("g-1", "2026-10-22T09:00:00Z")
	unknown["fleet"], unknown["fleet_name"], unknown["fleet_color"] = "evil", "Evil", "#ff0000"
	writeItem(t, shipDir(t, home, "ghost"), "g-1.json", unknown)

	items := listed(t, s)
	if g := items[0]; g["fleet"] != nil || g["fleet_name"] != nil || g["fleet_color"] != nil {
		t.Errorf("an item of no known ship = %v, want no fleet, fleet_name or fleet_color", g)
	}
	if q := items[1]; q["fleet"] != "work" || q["fleet_name"] != "Work" || q["fleet_color"] != "#38bdf8" {
		t.Errorf("an item whose file names a fleet = %v, want its ship's fleet", q)
	}
}

func TestInboxSkipsWhatIsNotAnItem(t *testing.T) {
	s, home, _ := newInbox(t)
	dir := shipDir(t, home, "s1")
	outside := filepath.Join(t.TempDir(), "outside.json")
	writeItem(t, filepath.Dir(outside), "outside.json", review("sneaky", "2026-10-22T10:00:00Z"))
	if err := os.Symlink(outside, filepath.Join(dir, "sneaky.json")); err != nil {
		t.Fatal(err)
	}
	writeItem(t, dir, "bad.json", []byte(`{"version":1,"id":"bad","kind":`))
	writeItem(t, dir, "nokind.json", map[string]any{"version": 1, "id": "nokind", "kind": "poem", "created": "2026-10-22T10:00:00Z", "title": "t"})
	writeItem(t, dir, "mismatch.json", review("other-id", "2026-10-22T10:00:00Z"))
	writeItem(t, dir, "big.json", []byte(`{"version":1,"id":"big","kind":"review","created":"2026-10-22T10:00:00Z","title":"`+strings.Repeat("x", 1<<20)+`"}`))
	writeItem(t, dir, ".ok.json.tmp.7", review("ok", "2026-10-22T10:00:00Z"))
	writeItem(t, dir, "good.json", review("good", "2026-10-22T12:00:00Z"))

	if got := idsOf(listed(t, s)); !reflect.DeepEqual(got, []string{"good"}) {
		t.Errorf("items = %v, want only good", got)
	}
}

func TestInboxRefusesAnInvalidAnswer(t *testing.T) {
	_, home, srv := newInbox(t)
	dir := shipDir(t, home, "s1")
	writeItem(t, dir, "q-1.json", question("q-1", "2026-10-22T10:00:00Z"))
	writeItem(t, dir, "r-1.json", review("r-1", "2026-10-22T10:05:00Z"))
	answer := func(id string, body any) int {
		t.Helper()
		b, _ := json.Marshal(body)
		resp, err := http.Post(srv.URL+"/api/inbox/s1/"+id+"/answer", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	both := func(one any) map[string]any {
		return map[string]any{"answers": map[string]any{"1": one, "2": map[string]any{"choice": []string{"X"}}}}
	}
	for name, body := range map[string]map[string]any{
		"a question left out":                {"answers": map[string]any{"1": map[string]any{"choice": "Alpha"}}},
		"a label the question doesn't offer": both(map[string]any{"choice": "Gamma"}),
		"a list on a single-choice question": both(map[string]any{"choice": []string{"Alpha", "Beta"}}),
		"a question the item doesn't have": {"answers": map[string]any{"1": map[string]any{"choice": "Alpha"},
			"2": map[string]any{"choice": []string{"X"}}, "9": map[string]any{"choice": "Alpha"}}},
	} {
		if code := answer("q-1", body); code != http.StatusBadRequest {
			t.Errorf("%s: answer = %d, want 400", name, code)
		}
	}
	if code := answer("r-1", both(map[string]any{"choice": "Alpha"})); code != http.StatusBadRequest {
		t.Errorf("a question answer to a review = %d, want 400", code)
	}
	for _, f := range []string{"q-1.answer.json", "r-1.answer.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s was written for an invalid answer", f)
		}
	}
	if code := answer("q-1", both(map[string]any{"other": "something else"})); code != http.StatusOK && code != http.StatusNoContent {
		t.Errorf("an answer by the engineer's own text alone = %d, want it taken", code)
	}
}
