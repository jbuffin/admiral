package ships

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// A ship's JSON serves the stamp of its first launch beside the stamp of its latest one.
func TestAShipsJSONCarriesItsFirstLaunchStampBesideItsLaunchStamp(t *testing.T) {
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	got := shipJSON(t, FromSession(sessionhost.Session{Name: "saltpaw", Options: map[string]string{
		"launched": "2026-11-15T09:00:00Z", "first-launched": "2026-11-14T08:00:00Z"}}, now))
	if got["launched"] != "2026-11-15T09:00:00Z" || got["first_launched"] != "2026-11-14T08:00:00Z" {
		t.Errorf("launched = %v, first_launched = %v, want the two stamps apart", got["launched"], got["first_launched"])
	}
}
