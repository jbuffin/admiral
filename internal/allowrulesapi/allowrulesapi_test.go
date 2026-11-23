package allowrulesapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/allowrules"
	"github.com/jbuffin/admiral/internal/allowrulesapi"
	"github.com/jbuffin/admiral/internal/config"
)

func newMux(t *testing.T) (*http.ServeMux, *allowrules.Store) {
	t.Helper()
	cfg, err := config.Parse([]byte(`{"version":1,"fleets":{"work":{"name":"Work","claude":{}},"home":{"name":"Home","claude":{}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	store := allowrules.Open(t.TempDir())
	mux := http.NewServeMux()
	(&allowrulesapi.Service{Config: func() *config.Config { return cfg }, Store: store}).Routes(mux)
	return mux, store
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) (int, allowrules.Fleet, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	var fl allowrules.Fleet
	var e struct{ Error string }
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &fl); err != nil {
			t.Fatalf("%s %s: %v in %s", method, path, err, rec.Body)
		}
	} else {
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
	}
	return rec.Code, fl, e.Error
}

func TestAFleetWithNoRulesListsTwoEmptyLists(t *testing.T) {
	mux, _ := newMux(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/fleets/work/allow-rules", nil))
	if got := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || got != `{"rules":[],"skipped":[]}` {
		t.Errorf("GET = %d %s, want 200 with empty rules and skipped", rec.Code, got)
	}
}

func TestTheListIsTheFleetsOwnRulesAndSkips(t *testing.T) {
	mux, store := newMux(t)
	claude := allowrules.Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(gh pr view:*)"}
	codex := allowrules.Rule{Harness: "codex", Pattern: []string{"make", "check"}}
	skipped := allowrules.Rule{Harness: "copilot", Repo: "acme/widgets", Rule: "shell(npm test)"}
	for _, err := range []error{store.Add("work", claude), store.Add("work", codex), store.Skip("work", skipped), store.Add("home", allowrules.Rule{Harness: "claude", Repo: "hobby/garden", Rule: "Bash(ls)"})} {
		if err != nil {
			t.Fatal(err)
		}
	}
	code, fl, _ := do(t, mux, "GET", "/api/fleets/work/allow-rules", "")
	if code != http.StatusOK || len(fl.Rules) != 2 || !fl.Rules[0].Same(claude) || !fl.Rules[1].Same(codex) || len(fl.Skipped) != 1 || !fl.Skipped[0].Same(skipped) {
		t.Fatalf("GET = %d %+v, want work's two rules then its one skip", code, fl)
	}
	if fl.Rules[0].Added.IsZero() {
		t.Error("a listed rule has no added time")
	}
}

func TestRemoveDropsTheRuleAndUnskipDropsTheSkip(t *testing.T) {
	mux, store := newMux(t)
	keep := allowrules.Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(make check)"}
	gone := allowrules.Rule{Harness: "codex", Pattern: []string{"gh", "pr", "view"}}
	skipped := allowrules.Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(npm test:*)"}
	for _, err := range []error{store.Add("work", keep), store.Add("work", gone), store.Skip("work", skipped)} {
		if err != nil {
			t.Fatal(err)
		}
	}

	code, fl, _ := do(t, mux, "POST", "/api/fleets/work/allow-rules/remove", `{"rule":{"harness":"codex","pattern":["gh","pr","view"],"added":"2026-01-02T03:04:05Z"}}`)
	if code != http.StatusOK || len(fl.Rules) != 1 || !fl.Rules[0].Same(keep) || len(fl.Skipped) != 1 {
		t.Fatalf("remove = %d %+v, want the other rule and the skip left", code, fl)
	}
	if rules, _ := store.For("work", "codex", ""); len(rules) != 0 {
		t.Errorf("the store still gives Codex ships %+v", rules)
	}

	code, fl, _ = do(t, mux, "POST", "/api/fleets/work/allow-rules/skipped/remove", `{"rule":{"harness":"claude","repo":"acme/widgets","rule":"Bash(npm test:*)"}}`)
	if code != http.StatusOK || len(fl.Skipped) != 0 || len(fl.Rules) != 1 {
		t.Fatalf("unskip = %d %+v, want no skips and the rule left", code, fl)
	}
}

func TestRefusals(t *testing.T) {
	mux, _ := newMux(t)
	for _, c := range []struct {
		name, method, path, body string
		code                     int
	}{
		{"unknown fleet list", "GET", "/api/fleets/nope/allow-rules", "", http.StatusNotFound},
		{"unknown fleet remove", "POST", "/api/fleets/nope/allow-rules/remove", `{"rule":{"harness":"codex","pattern":["ls"]}}`, http.StatusNotFound},
		{"no rule", "POST", "/api/fleets/work/allow-rules/remove", `{}`, http.StatusBadRequest},
		{"not JSON", "POST", "/api/fleets/work/allow-rules/remove", `nope`, http.StatusBadRequest},
		{"wrong shape", "POST", "/api/fleets/work/allow-rules/skipped/remove", `{"rule":{"harness":"claude","rule":"Bash(ls)"}}`, http.StatusBadRequest},
	} {
		if code, _, msg := do(t, mux, c.method, c.path, c.body); code != c.code || msg == "" {
			t.Errorf("%s: %d %q, want %d with an error", c.name, code, msg, c.code)
		}
	}
}
