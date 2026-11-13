package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

type instructionsBody struct {
	Text string `json:"text"`
	Max  int    `json:"max"`
}

func (r *notesRig) instructions(t *testing.T, fleet string) instructionsBody {
	t.Helper()
	rec := r.do("GET", "/api/chat/"+fleet+"/instructions", "", "")
	var got instructionsBody
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("GET instructions for %s = %d %s, want 200 {text, max}", fleet, rec.Code, rec.Body)
	}
	return got
}

func (r *notesRig) putInstructions(fleet, text string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]string{"text": text})
	return r.do("PUT", "/api/chat/"+fleet+"/instructions", string(b), "")
}

func (r *notesRig) addNote(t *testing.T, fleet, text string) noteBody {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"text": text})
	rec := r.do("POST", "/api/chat/notes", string(b), fleet)
	var n noteBody
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &n) != nil {
		t.Fatalf("seeding a note = %d %s, want 201", rec.Code, rec.Body)
	}
	return n
}

func errorOf(rec *httptest.ResponseRecorder) string {
	var e struct{ Error string }
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Error
}

func (r *notesRig) instructionsFile(fleet string) string {
	b, _ := os.ReadFile(filepath.Join(r.home, "chat", fleet, "instructions.md"))
	return string(b)
}

func TestInstructionsStartEmptyWithTheirCapAndSaveThroughPUT(t *testing.T) {
	r := newNotesRig(t)
	if got := r.instructions(t, "home"); got.Text != "" || got.Max != 2000 {
		t.Errorf("GET = %+v, want {\"\", 2000}", got)
	}
	rec := r.putInstructions("home", "  Ask before launching.\n")
	var put instructionsBody
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &put) != nil || put.Text != "Ask before launching." || put.Max != 2000 {
		t.Fatalf("PUT = %d %s, want 200 with the trimmed text and max 2000", rec.Code, rec.Body)
	}
	if got := r.instructions(t, "home"); got != put {
		t.Errorf("GET after PUT = %+v, want %+v", got, put)
	}
	if got := r.instructionsFile("home"); got != "Ask before launching." {
		t.Errorf("chat/home/instructions.md = %q, want the trimmed text", got)
	}
	if got := r.instructions(t, "work"); got.Text != "" {
		t.Errorf("work's instructions = %q, want none: fleets are separate", got.Text)
	}
}

func TestAnEmptyPUTClearsTheInstructions(t *testing.T) {
	r := newNotesRig(t)
	if rec := r.putInstructions("home", "something"); rec.Code != 200 || r.instructionsFile("home") == "" {
		t.Fatalf("seeding instructions = %d %s", rec.Code, rec.Body)
	}
	rec := r.putInstructions("home", "  ")
	var put instructionsBody
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &put) != nil || put.Text != "" || put.Max != 2000 {
		t.Errorf("PUT blank = %d %s, want 200 {\"\", 2000}", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(r.home, "chat", "home", "instructions.md")); !os.IsNotExist(err) {
		t.Errorf("instructions.md after clearing: %v, want it gone", err)
	}
}

func TestInstructionsOverTheCapAreRefusedSayingByHowMuchAndKeepTheOldOnes(t *testing.T) {
	r := newNotesRig(t)
	r.putInstructions("home", "keep me")
	rec := r.putInstructions("home", strings.Repeat("x", 2005))
	if rec.Code < 400 || rec.Code >= 500 || rec.Code == http.StatusForbidden || rec.Code == http.StatusNotFound {
		t.Fatalf("PUT over the cap = %d %s, want a 4xx other than 403 and 404", rec.Code, rec.Body)
	}
	if !regexp.MustCompile(`\b5 characters?\b`).MatchString(errorOf(rec)) {
		t.Errorf("error = %q, want it to say the text is over by 5 characters", errorOf(rec))
	}
	if got := r.instructions(t, "home").Text; got != "keep me" {
		t.Errorf("instructions = %q, want unchanged", got)
	}
}

func TestAMalformedInstructionsBodyIsRefusedAndNothingChanges(t *testing.T) {
	r := newNotesRig(t)
	r.putInstructions("home", "keep me")
	for _, body := range []string{`not json`, `{}`, `{"text":5}`} {
		if rec := r.do("PUT", "/api/chat/home/instructions", body, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %q = %d %s, want 400", body, rec.Code, rec.Body)
		}
	}
	if got := r.instructions(t, "home").Text; got != "keep me" {
		t.Errorf("instructions = %q, want unchanged", got)
	}
}

func TestTheEngineerDeletesANoteAndItsCancelCarriesNoVia(t *testing.T) {
	r := newNotesRig(t)
	keep := r.addNote(t, "home", "keep")
	gone := r.addNote(t, "home", "gone")
	if rec := r.do("DELETE", "/api/chat/home/notes/"+gone.ID, "", ""); rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("DELETE note = %d %s, want 2xx", rec.Code, rec.Body)
	}
	if got := r.list(t, "home"); len(got) != 1 || got[0].ID != keep.ID {
		t.Errorf("listed = %+v, want only the kept note", got)
	}
	ls := r.ledgerLines(t, "home")
	if last := ls[len(ls)-1]; len(ls) != 3 || !strings.Contains(last, `"cancelled"`) || strings.Contains(last, `"via"`) {
		t.Errorf("ledger = %q, want a cancelled event with no via", ls)
	}
}

func TestDeletingAnUnknownOrAlreadyDeletedNoteIs404(t *testing.T) {
	r := newNotesRig(t)
	n := r.addNote(t, "home", "once")
	if rec := r.do("DELETE", "/api/chat/home/notes/"+n.ID, "", ""); rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("DELETE note = %d %s, want 2xx", rec.Code, rec.Body)
	}
	for _, id := range []string{n.ID, "nope"} {
		if rec := r.do("DELETE", "/api/chat/home/notes/"+id, "", ""); rec.Code != http.StatusNotFound || errorOf(rec) == "" {
			t.Errorf("DELETE note %q = %d %s, want a JSON 404", id, rec.Code, rec.Body)
		}
	}
	if rec := r.do("DELETE", "/api/chat/work/notes/"+n.ID, "", ""); rec.Code != http.StatusNotFound || errorOf(rec) == "" {
		t.Errorf("DELETE home's note through work = %d %s, want a JSON 404", rec.Code, rec.Body)
	}
}

func TestPromotingANoteMovesItsTextAfterTheInstructionsAndRemovesTheNote(t *testing.T) {
	r := newNotesRig(t)
	r.putInstructions("home", "Be terse.")
	n := r.addNote(t, "home", "Deploys go out on Tuesdays.")
	if rec := r.do("POST", "/api/chat/home/notes/"+n.ID+"/promote", "", ""); rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("promote = %d %s, want 2xx", rec.Code, rec.Body)
	}
	if got := r.instructions(t, "home").Text; got != "Be terse.\n\nDeploys go out on Tuesdays." {
		t.Errorf("instructions = %q, want the note after a blank line", got)
	}
	if got := r.list(t, "home"); len(got) != 0 {
		t.Errorf("listed = %+v, want the promoted note gone", got)
	}
	if rec := r.do("POST", "/api/chat/home/notes/"+n.ID+"/promote", "", ""); rec.Code != http.StatusNotFound || errorOf(rec) == "" {
		t.Errorf("promoting it again = %d %s, want a JSON 404", rec.Code, rec.Body)
	}
}

func TestPromotingPastTheCapIs409SayingByHowMuchAndChangesNothing(t *testing.T) {
	r := newNotesRig(t)
	r.putInstructions("home", strings.Repeat("i", 1990))
	n := r.addNote(t, "home", strings.Repeat("n", 20))
	rec := r.do("POST", "/api/chat/home/notes/"+n.ID+"/promote", "", "")
	if rec.Code != http.StatusConflict || !regexp.MustCompile(`\b12 characters?\b`).MatchString(errorOf(rec)) {
		t.Errorf("promote = %d %s, want 409 saying it is over by 12 characters (the blank line counts)", rec.Code, rec.Body)
	}
	if got := r.instructions(t, "home").Text; len(got) != 1990 {
		t.Errorf("instructions are %d long, want unchanged at 1990", len(got))
	}
	if got := r.list(t, "home"); len(got) != 1 || got[0].ID != n.ID {
		t.Errorf("listed = %+v, want the note still open", got)
	}
}

// engineerRoutes are the four u2 routes, each called for a fleet with a note id that need not exist.
func engineerRoutes(fleet, id string) []struct{ method, path, body string } {
	return []struct{ method, path, body string }{
		{"GET", "/api/chat/" + fleet + "/instructions", ""},
		{"PUT", "/api/chat/" + fleet + "/instructions", `{"text":"hi"}`},
		{"DELETE", "/api/chat/" + fleet + "/notes/" + id, ""},
		{"POST", "/api/chat/" + fleet + "/notes/" + id + "/promote", ""},
	}
}

func TestTheNotesAndInstructionsRoutesRefuseAnUnknownFleetOrAKeyThatIsNotOne(t *testing.T) {
	r := newNotesRig(t)
	for _, fleet := range []string{"nowhere", "bad.key"} {
		for _, c := range engineerRoutes(fleet, "n1") {
			if rec := r.do(c.method, c.path, c.body, ""); rec.Code != http.StatusNotFound || !strings.Contains(errorOf(rec), "no fleet") {
				t.Errorf("%s %s = %d %s, want a JSON 404 saying there is no such fleet", c.method, c.path, rec.Code, rec.Body)
			}
		}
	}
}

func TestTheNotesAndInstructionsRoutesStayServedWhileTheFleetsChatIsOff(t *testing.T) {
	r := newNotesRig(t)
	if err := os.MkdirAll(filepath.Join(r.home, "chat", "off"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"n1","kind":"note","text":"still here"}` + "\n"
	if err := os.WriteFile(filepath.Join(r.home, "chat", "off", "ledger.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := r.putInstructions("off", "Be terse."); rec.Code != 200 {
		t.Errorf("PUT instructions while chat is off = %d %s, want 200", rec.Code, rec.Body)
	}
	if got := r.instructions(t, "off").Text; got != "Be terse." {
		t.Errorf("instructions = %q, want Be terse.", got)
	}
	if rec := r.do("POST", "/api/chat/off/notes/n1/promote", "", ""); rec.Code < 200 || rec.Code >= 300 {
		t.Errorf("promote while chat is off = %d %s, want 2xx", rec.Code, rec.Body)
	}
	if got := r.instructions(t, "off").Text; got != "Be terse.\n\nstill here" {
		t.Errorf("instructions = %q, want the note promoted", got)
	}
	r.do("PUT", "/api/chat/off/instructions", `{"text":""}`, "")
	line2 := strings.Replace(line, `"n1"`, `"n2"`, 1)
	f, err := os.OpenFile(filepath.Join(r.home, "chat", "off", "ledger.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(line2)
	f.Close()
	if rec := r.do("DELETE", "/api/chat/off/notes/n2", "", ""); rec.Code < 200 || rec.Code >= 300 {
		t.Errorf("DELETE note while chat is off = %d %s, want 2xx", rec.Code, rec.Body)
	}
}

// newClashRig has fleet work before fleet Work: both would share chat/work/ on a disk that ignores case.
func newClashRig(t *testing.T) *notesRig {
	t.Helper()
	home := t.TempDir()
	one := func(name string) string {
		return `{"name":"` + name + `","search_roots":["` + home + `"],"claude":{"executable":"/bin/sh"}}`
	}
	cfg, err := config.Parse([]byte(`{"version":1,"fleets":{"work":` + one("Work") + `,"Work":` + one("Shouty") + `}}`))
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := cfg.Fleet("Work"); !strings.Contains(cfg.ChatOffReason(f), "case") {
		t.Fatalf("fleet Work: chat off reason %q, the rig needs the case clash", cfg.ChatOffReason(f))
	}
	mux := http.NewServeMux()
	(&Service{Home: home, Config: func() *config.Config { return cfg }}).Routes(mux)
	return &notesRig{home: home, mux: mux}
}

func TestEveryNotesAndInstructionsRouteRefusesAFleetWhoseKeyClashesInCaseWithAnEarlierOne(t *testing.T) {
	r := newClashRig(t)
	for _, c := range engineerRoutes("Work", "n1") {
		if rec := r.do(c.method, c.path, c.body, ""); rec.Code != http.StatusConflict || !strings.Contains(errorOf(rec), "differs only in case") {
			t.Errorf("%s %s = %d %s, want 409 giving the case clash", c.method, c.path, rec.Code, rec.Body)
		}
	}
	if rec := r.do("GET", "/api/chat/Work/notes", "", ""); rec.Code != http.StatusConflict || !strings.Contains(errorOf(rec), "differs only in case") {
		t.Errorf("GET notes = %d %s, want 409 giving the case clash", rec.Code, rec.Body)
	}
	if rec := r.do("POST", "/api/chat/notes", `{"text":"x"}`, "Work"); rec.Code != http.StatusConflict || !strings.Contains(errorOf(rec), "differs only in case") {
		t.Errorf("POST note from the clashing fleet = %d %s, want 409 giving the case clash", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(r.home, "chat", "Work")); err == nil {
		t.Error("chat/Work was created, want nothing written for the clashing fleet")
	}
	// The earlier fleet keeps its chat.
	if rec := r.do("GET", "/api/chat/work/instructions", "", ""); rec.Code != 200 {
		t.Errorf("GET instructions for the earlier fleet = %d %s, want 200", rec.Code, rec.Body)
	}
}
