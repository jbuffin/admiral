package routines_test

import (
	"errors"
	"testing"

	"github.com/jbuffin/admiral/internal/routines"
)

func TestListNamesTheSchedulerZone(t *testing.T) {
	a := newAPI(t)
	status, body := a.do("GET", "/api/fleets/work/routines", "")
	if status != 200 {
		t.Fatalf("list answered %d: %v", status, body)
	}
	if body["zone"] != "America/Denver" {
		t.Fatalf("list zone = %v, want America/Denver", body["zone"])
	}
}

func TestZoneName(t *testing.T) {
	link := func(target string) func(string) (string, error) {
		return func(path string) (string, error) {
			if path != "/etc/localtime" {
				t.Errorf("read the link at %q, want /etc/localtime", path)
			}
			return target, nil
		}
	}
	noLink := func(string) (string, error) { return "", errors.New("not a link") }

	tests := []struct {
		name, loc, abbr string
		readlink        func(string) (string, error)
		want            string
	}{
		{"a named location gives its name", "America/Denver", "MST", noLink, "America/Denver"},
		{"a UTC location gives UTC", "UTC", "UTC", noLink, "UTC"},
		{"Local follows /etc/localtime after zoneinfo/", "Local", "MST", link("/var/db/timezone/zoneinfo/America/Denver"), "America/Denver"},
		{"Local on a Linux link", "Local", "CET", link("../usr/share/zoneinfo/Europe/Berlin"), "Europe/Berlin"},
		{"Local with no link gives the abbreviation", "Local", "MST", noLink, "MST"},
		{"a TZ path into zoneinfo gives the name after it", "/usr/share/zoneinfo/Asia/Tokyo", "JST", noLink, "Asia/Tokyo"},
		{"a TZ path outside zoneinfo gives the abbreviation", "/tmp/my-zone", "MST", noLink, "MST"},
		{"Local with a link outside zoneinfo gives the abbreviation", "Local", "MST", link("/tmp/my-clock"), "MST"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := routines.ZoneName(tt.loc, tt.abbr, tt.readlink); got != tt.want {
				t.Fatalf("ZoneName(%q, %q) = %q, want %q", tt.loc, tt.abbr, got, tt.want)
			}
		})
	}
}
