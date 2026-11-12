package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
)

type notesRig struct {
	home string
	mux  *http.ServeMux
}

// newNotesRig configures fleets home and work with chat on, and off with no Claude Code dock.
func newNotesRig(t *testing.T) *notesRig {
	t.Helper()
	home := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{
		"home": map[string]any{"name": "Home", "search_roots": []string{home}, "claude": map[string]any{"executable": "/bin/sh"}},
		"work": map[string]any{"name": "Work", "search_roots": []string{home}, "claude": map[string]any{"executable": "/bin/sh"}},
		"off":  map[string]any{"name": "Off", "search_roots": []string{home}},
	}})
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, off, ok := cfg.ChatFleet("off"); !ok || off == "" {
		t.Fatalf("fleet off: chat off reason %q, ok %v; the rig needs it configured with its chat off", off, ok)
	}
	mux := http.NewServeMux()
	(&Service{Home: home, Config: func() *config.Config { return cfg }}).Routes(mux)
	return &notesRig{home: home, mux: mux}
}

func (r *notesRig) do(method, target, body, fleet string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if fleet != "" {
		req = req.WithContext(actor.WithFleet(context.Background(), fleet))
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	return rec
}

func (r *notesRig) ledgerLines(t *testing.T, fleet string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.home, "chat", fleet, "ledger.jsonl"))
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

type noteBody struct{ ID, Text, TS, Via string }

func (r *notesRig) list(t *testing.T, fleet string) []noteBody {
	t.Helper()
	rec := r.do("GET", "/api/chat/"+fleet+"/notes", "", "")
	var got struct{ Notes []noteBody }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("GET notes for %s = %d %s, want 200 {\"notes\": [...]}", fleet, rec.Code, rec.Body)
	}
	return got.Notes
}

func TestANoteAdmiralBotSavesIsStampedAndTheEngineerCanListIt(t *testing.T) {
	r := newNotesRig(t)
	rec := r.do("POST", "/api/chat/notes", `{"text":"  deploys go out on Tuesdays "}`, "home")
	var n noteBody
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &n) != nil || n.ID == "" || n.TS == "" {
		t.Fatalf("POST note = %d %s, want 201 {id, text, ts, via}", rec.Code, rec.Body)
	}
	if n.Text != "deploys go out on Tuesdays" || n.Via != "admiralbot" {
		t.Errorf("note = %+v, want trimmed text and via admiralbot", n)
	}
	if lines := r.ledgerLines(t, "home"); len(lines) != 1 || !strings.Contains(lines[0], `"via":"admiralbot"`) {
		t.Errorf("home's ledger = %q, want one event stamped via admiralbot", lines)
	}
	got := r.list(t, "home")
	if len(got) != 1 || got[0] != n {
		t.Errorf("listed = %+v, want exactly %+v", got, n)
	}
}

func TestANoteGoesToTheTokensFleetWhateverTheBodyNames(t *testing.T) {
	r := newNotesRig(t)
	if rec := r.do("POST", "/api/chat/notes", `{"fleet":"work","text":"mine"}`, "home"); rec.Code != http.StatusCreated {
		t.Fatalf("POST note = %d %s, want 201", rec.Code, rec.Body)
	}
	if got := r.ledgerLines(t, "work"); len(got) != 0 {
		t.Errorf("work's ledger = %q, want nothing: the token is home's", got)
	}
	if len(r.list(t, "home")) != 1 || len(r.list(t, "work")) != 0 {
		t.Error("home must list its one note and work none")
	}
}

func TestTheEngineersOwnRequestCannotWriteANote(t *testing.T) {
	r := newNotesRig(t)
	rec := r.do("POST", "/api/chat/notes", `{"text":"x"}`, "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST note with no actor = %d %s, want 403", rec.Code, rec.Body)
	}
	if got := r.ledgerLines(t, "home"); len(got) != 0 {
		t.Errorf("ledger = %q, want nothing written", got)
	}
}

func TestABadNoteIsRefusedAndNothingIsWritten(t *testing.T) {
	r := newNotesRig(t)
	for _, body := range []string{`not json`, `{}`, `{"text":""}`, `{"text":"  \n"}`, `{"text":"` + strings.Repeat("x", 501) + `"}`} {
		if rec := r.do("POST", "/api/chat/notes", body, "home"); rec.Code < 400 || rec.Code >= 500 || rec.Code == http.StatusForbidden || rec.Code == http.StatusNotFound {
			t.Errorf("POST note %.40q = %d %s, want a 4xx other than 403 and 404", body, rec.Code, rec.Body)
		}
	}
	if got := r.ledgerLines(t, "home"); len(got) != 0 {
		t.Errorf("ledger = %q, want nothing written", got)
	}
}

func TestTheThirtyFirstNoteIsRefusedWithAMessageThatPointsAtPreferences(t *testing.T) {
	r := newNotesRig(t)
	for i := range 30 {
		if rec := r.do("POST", "/api/chat/notes", `{"text":"note `+strings.Repeat("n", i)+`"}`, "home"); rec.Code != http.StatusCreated {
			t.Fatalf("note %d = %d %s, want 201", i+1, rec.Code, rec.Body)
		}
	}
	rec := r.do("POST", "/api/chat/notes", `{"text":"one too many"}`, "home")
	var e struct{ Error string }
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code < 400 || rec.Code >= 500 || rec.Code == http.StatusForbidden || rec.Code == http.StatusNotFound {
		t.Errorf("31st note = %d %s, want a 4xx other than 403 and 404", rec.Code, rec.Body)
	}
	if !strings.Contains(e.Error, "full") || !strings.Contains(e.Error, "Preferences") {
		t.Errorf("error = %q, want it to say the ledger is full and the engineer can remove notes on Preferences", e.Error)
	}
	if n := len(r.ledgerLines(t, "home")); n != 30 {
		t.Errorf("ledger has %d lines, want 30", n)
	}
}

func TestListingNotesForAFleetWithNoLedgerGivesAnEmptyList(t *testing.T) {
	r := newNotesRig(t)
	rec := r.do("GET", "/api/chat/home/notes", "", "")
	if rec.Code != 200 || !strings.Contains(strings.ReplaceAll(rec.Body.String(), " ", ""), `"notes":[]`) {
		t.Errorf("GET notes = %d %s, want 200 with \"notes\": []", rec.Code, rec.Body)
	}
}

func TestNotesStayListableWhileTheFleetsChatIsOff(t *testing.T) {
	r := newNotesRig(t)
	if err := os.MkdirAll(filepath.Join(r.home, "chat", "off"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"n1","kind":"note","text":"still here"}` + "\n"
	if err := os.WriteFile(filepath.Join(r.home, "chat", "off", "ledger.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := r.list(t, "off"); len(got) != 1 || got[0].Text != "still here" {
		t.Errorf("listed = %+v, want the one note", got)
	}
}

func TestListingNotesRefusesAnUnknownFleetOrAKeyThatIsNotOne(t *testing.T) {
	r := newNotesRig(t)
	for _, fleet := range []string{"nowhere", "bad.key", "a%20b"} {
		if rec := r.do("GET", "/api/chat/"+fleet+"/notes", "", ""); rec.Code != http.StatusNotFound || !strings.HasPrefix(rec.Body.String(), "{") {
			t.Errorf("GET notes for %q = %d %s, want a JSON 404", fleet, rec.Code, rec.Body)
		}
	}
}
