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
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

const askBody = `{"title":"Which branch?","questions":[{"id":"q1","text":"Which branch?","multi":false,"options":[{"label":"main"},{"label":"dev"}]}]}`

type askRig struct {
	home string
	mux  *http.ServeMux
}

func newAskRig(t *testing.T) *askRig {
	t.Helper()
	home := t.TempDir()
	for _, fleet := range []string{"home", "work"} {
		if err := os.MkdirAll(filepath.Join(home, "chat", fleet, "questions"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	(&Service{Home: home}).Routes(mux)
	return &askRig{home: home, mux: mux}
}

func (r *askRig) do(method, target, body, fleet string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if fleet != "" {
		req = req.WithContext(actor.WithFleet(context.Background(), fleet))
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	return rec
}

func (r *askRig) questionFiles(t *testing.T, fleet string) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(r.home, "chat", fleet, "questions", "*.json"))
	return files
}

// ask posts a question as AdmiralBot of fleet and returns its id.
func (r *askRig) ask(t *testing.T, fleet string) string {
	t.Helper()
	rec := r.do("POST", "/api/chat/questions", askBody, fleet)
	var got struct{ ID string }
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.ID == "" {
		t.Fatalf("ask = %d %s, want 201 {\"id\": ...}", rec.Code, rec.Body)
	}
	return got.ID
}

func TestAskWritesTheQuestionForTheTokensFleetOnly(t *testing.T) {
	r := newAskRig(t)
	// The body names another fleet; the token decides.
	rec := r.do("POST", "/api/chat/questions", strings.Replace(askBody, `{"title"`, `{"fleet":"work","title"`, 1), "home")
	if rec.Code != http.StatusCreated {
		t.Fatalf("ask = %d %s, want 201", rec.Code, rec.Body)
	}
	if got := r.questionFiles(t, "work"); len(got) != 0 {
		t.Errorf("work's questions = %v, want none: the token is home's", got)
	}
	files := r.questionFiles(t, "home")
	if len(files) != 1 {
		t.Fatalf("home's questions = %v, want one", files)
	}
	b, _ := os.ReadFile(files[0])
	id := strings.TrimSuffix(filepath.Base(files[0]), ".json")
	if _, err := inbox.ParseItem(b, sessionhost.ChatSession("home"), id); err != nil {
		t.Errorf("the written question is not an inbox item: %v", err)
	}
}

func TestAskRefusesARequestThatIsNotAnActors(t *testing.T) {
	r := newAskRig(t)
	rec := r.do("POST", "/api/chat/questions", askBody, "")
	if rec.Code < 400 || rec.Code >= 500 || rec.Code == http.StatusNotFound {
		t.Errorf("ask with no actor = %d %s, want a 4xx other than 404", rec.Code, rec.Body)
	}
	if len(r.questionFiles(t, "home"))+len(r.questionFiles(t, "work")) != 0 {
		t.Error("a question was written for a request with no actor")
	}
}

func TestAskRefusesAQuestionThatIsNotOne(t *testing.T) {
	r := newAskRig(t)
	for _, body := range []string{`not json`, `{"title":"t"}`, `{"title":"","questions":[]}`} {
		if rec := r.do("POST", "/api/chat/questions", body, "home"); rec.Code != http.StatusBadRequest {
			t.Errorf("ask %q = %d %s, want 400", body, rec.Code, rec.Body)
		}
	}
	if got := r.questionFiles(t, "home"); len(got) != 0 {
		t.Errorf("written for a bad question: %v", got)
	}
}

type answer struct {
	State  string          `json:"state"`
	Answer json.RawMessage `json:"answer"`
}

func (r *askRig) poll(t *testing.T, id, query, fleet string) (int, answer, string) {
	t.Helper()
	rec := r.do("GET", "/api/chat/questions/"+id+"/answer"+query, "", fleet)
	var a answer
	_ = json.Unmarshal(rec.Body.Bytes(), &a)
	return rec.Code, a, rec.Body.String()
}

func TestTheAnswerPollSaysWaitingUntilTheQuestionIsAnswered(t *testing.T) {
	r := newAskRig(t)
	id := r.ask(t, "home")
	if code, a, body := r.poll(t, id, "?wait=0", "home"); code != 200 || a.State != "waiting" {
		t.Fatalf("poll = %d %s, want 200 state waiting", code, body)
	}
	want := `{"answered":"2026-11-12T10:00:00Z","answers":{"q1":{"selected":["main"]}}}`
	path := filepath.Join(r.home, "chat", "home", "questions", inbox.AnswerName(id))
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	code, a, body := r.poll(t, id, "?wait=0", "home")
	if code != 200 || a.State != "answered" || !strings.Contains(string(a.Answer), "main") {
		t.Errorf("poll = %d %s, want 200 state answered carrying the answer", code, body)
	}
}

func TestTheAnswerPollSaysWithdrawnWhenTheQuestionIsGone(t *testing.T) {
	r := newAskRig(t)
	id := r.ask(t, "home")
	for _, f := range r.questionFiles(t, "home") {
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
	}
	if code, a, body := r.poll(t, id, "?wait=0", "home"); code != 200 || a.State != "withdrawn" {
		t.Errorf("poll = %d %s, want 200 state withdrawn", code, body)
	}
}

func TestTheAnswerPollNeverShowsAnotherFleetsAnswer(t *testing.T) {
	r := newAskRig(t)
	id := r.ask(t, "work")
	path := filepath.Join(r.home, "chat", "work", "questions", inbox.AnswerName(id))
	if err := os.WriteFile(path, []byte(`{"answered":"2026-11-12T10:00:00Z","answers":{"q1":{"selected":["secret-branch"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, a, body := r.poll(t, id, "?wait=0", "home")
	if code == 200 && a.State == "answered" || strings.Contains(body, "secret-branch") {
		t.Errorf("home read work's answer: %d %s", code, body)
	}
	if code == http.StatusNotFound && !strings.HasPrefix(body, "{") {
		t.Errorf("poll = 404 %q, want a JSON body: the route must be registered", body)
	}
	if code, _, _ := r.poll(t, id, "?wait=0", "work"); code != 200 {
		t.Errorf("the owning fleet's own poll = %d, want 200", code)
	}
}

func TestTheAnswerPollRefusesABadIDOrWait(t *testing.T) {
	r := newAskRig(t)
	id := r.ask(t, "home")
	for _, c := range []struct{ id, query string }{{"../etc", "?wait=0"}, {"not-an-id", "?wait=0"}, {id, "?wait=abc"}, {id, "?wait=-1"}} {
		if code, _, body := r.poll(t, c.id, c.query, "home"); code != http.StatusBadRequest {
			t.Errorf("poll %s%s = %d %s, want 400", c.id, c.query, code, body)
		}
	}
}
