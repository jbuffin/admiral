package ships

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnswerRefusesWhatIsNotAnAnswerToThePaneNow(t *testing.T) {
	s, _ := tailService(t)
	mux := http.NewServeMux()
	s.Routes(mux)
	post := func(path, body string) int {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec.Code
	}
	const ship = "/api/ships/alpha/answer"

	for name, body := range map[string]string{
		"not JSON":                    `nope`,
		"no card":                     `{}`,
		"an unknown card":             `{"card":"trust","reason":"permission:Bash","choice":"allow"}`,
		"an unknown choice":           `{"card":"permission","reason":"permission:Bash","choice":"always"}`,
		"a permission with no reason": `{"card":"permission","choice":"allow"}`,
		"a question with no item":     `{"card":"question","reason":"question","option":1}`,
		"a question with option 0":    `{"card":"question","reason":"question","item":"q1","option":0}`,
		"a question with no option":   `{"card":"question","reason":"question","item":"q1"}`,
	} {
		if code := post(ship, body); code != http.StatusBadRequest {
			t.Errorf("POST answer with %s = %d, want 400", name, code)
		}
	}

	if code := post(ship, `{"card":"permission","reason":"permission:Bash","choice":"allow"}`); code != http.StatusConflict {
		t.Errorf("POST answer to a ship that does not need you = %d, want 409", code)
	}
	if code := post("/api/ships/ghost/answer", `{"card":"permission","reason":"permission:Bash","choice":"allow"}`); code != http.StatusNotFound {
		t.Errorf("POST answer to an unknown ship = %d, want 404", code)
	}
	if code := post("/api/ships/alpha/crew/ghost/answer", `{"card":"permission","reason":"permission:Bash","choice":"allow"}`); code != http.StatusNotFound {
		t.Errorf("POST answer to an unknown crew boat = %d, want 404", code)
	}
}
