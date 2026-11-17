package ships

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
)

func TestUpdateBranchRefusesALiveShip(t *testing.T) {
	s, _ := tailService(t)
	s.Launcher = &launcher.Launcher{Home: t.TempDir(), Config: func() *config.Config { return &config.Config{} }}
	s.MergeGuard = func(h http.Handler) http.Handler { return h }
	mux := http.NewServeMux()
	s.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/alpha/update-branch", strings.NewReader(`{"pr":"`+prSeven+`"}`)))
	if rec.Code != http.StatusConflict {
		t.Errorf("POST update-branch on a live ship = %d %s, want 409", rec.Code, rec.Body)
	}
}
