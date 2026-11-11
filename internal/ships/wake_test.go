package ships

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

func (f *mooredFixture) wake(session, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/ships/"+session+"/wake", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	f.mux.ServeHTTP(rec, req)
	return rec
}

func TestWakeOfAShipThatIsNotMooredAnswers404(t *testing.T) {
	f := mooredService(t, nil)
	if rec := f.wake("no-such-ship", `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("wake of an unknown ship = %d %s, want 404", rec.Code, rec.Body)
	}
}

func TestWakeOfAMooredShipWithNoResumeTextAnswers409NamingIt(t *testing.T) {
	f := mooredService(t, nil)
	session := f.moor(t, "Saltpaw", "work", "https://github.com/acme/widgets/pull/7")
	rec := f.wake(session, `{"text":"go on"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "resume") {
		t.Errorf("wake with no resume text = %d %s, want 409 naming the resume text", rec.Code, rec.Body)
	}
	if got := f.records(t); len(got) != 1 {
		t.Errorf("the refused wake left %d moored records, want the one it started with", len(got))
	}
}

func TestAServiceWakeOfAnUnknownShipIs404(t *testing.T) {
	f := mooredService(t, nil)
	if _, lerr := f.s.Wake(context.Background(), "no-such-ship", ""); lerr == nil || lerr.Status != http.StatusNotFound {
		t.Errorf("Wake of an unknown ship = %v, want a 404", lerr)
	}
	if _, lerr := f.s.FeedbackRound(context.Background(), "no-such-ship", "https://github.com/acme/widgets/pull/7", "text"); lerr == nil || lerr.Status != http.StatusNotFound {
		t.Errorf("FeedbackRound of an unknown ship = %v, want a 404", lerr)
	}
}

// The round and the resources ride the session's options through a woken ship's life, so a ship that
// moors again keeps both for the next wake.
func TestMooringAgainKeepsTheRoundAndTheResourcesInTheRecord(t *testing.T) {
	f := mooredService(t, nil)
	pr := "https://github.com/acme/widgets/pull/7"
	f.s.moor(context.Background(), sessionhost.Session{Name: "saltpaw", Options: map[string]string{
		"name": "Saltpaw", "fleet": "work", "harness": "claude", "dir": "/src/widgets", "round": "2", "resources": "phone"}}, []string{pr})
	recs := f.records(t)
	if len(recs) != 1 || recs[0].Round != 2 || len(recs[0].Resources) != 1 || recs[0].Resources[0] != "phone" {
		t.Fatalf("records = %+v, want one with Round 2 and Resources [phone]", recs)
	}
	if got := f.events(t, "saltpaw", "round"); len(got) != 1 || got[0]["round"] != float64(2) || got[0]["end"] != "moored" {
		t.Errorf("round events = %v, want one for round 2 ending moored", got)
	}
}

func TestWakeOfAShipTheEngineerDismissedAnswers404(t *testing.T) {
	f := mooredService(t, nil)
	session := f.moor(t, "Saltpaw", "work", "https://github.com/acme/widgets/pull/7")
	if rec := f.post("/api/ships/" + session + "/dismiss"); rec.Code != http.StatusOK {
		t.Fatalf("dismiss = %d %s", rec.Code, rec.Body)
	}
	if rec := f.wake(session, `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("wake of a dismissed ship = %d %s, want 404", rec.Code, rec.Body)
	}
}
