package ships

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

func TestMarkReadyForReviewOnARunningShip(t *testing.T) {
	t.Parallel()
	r := newPRRig(t)
	r.candidates = []string{prSeven}
	r.mine(prSeven, "OPEN", time.Minute)
	r.gh.detail = map[string]moor.Detail{prSeven: {URL: prSeven, State: "OPEN", Draft: true}}
	r.s.PollMoored(context.Background())
	r.s.MergeGuard = func(h http.Handler) http.Handler { return h }
	mux := http.NewServeMux()
	r.s.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/saltpaw/ready-for-review", strings.NewReader(`{"prs":["`+prSeven+`"]}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"marked_ready"`) {
		t.Fatalf("POST ready-for-review on a running ship = %d %s, want 200 marked_ready", rec.Code, rec.Body)
	}
	if got := r.gh.readyLog(); len(got) != 1 || got[0] != prSeven {
		t.Errorf("gh pr ready calls = %v, want one for %s", got, prSeven)
	}
}
