package update

import "testing"

func TestAvailableComparesNumericallyAgainstTheBaseOfTheRunningVersion(t *testing.T) {
	for _, c := range []struct {
		name            string
		running, latest string
		want            bool
	}{
		{"newer release", "0.9.1", "0.9.2", true},
		{"equal", "0.9.1", "0.9.1", false},
		{"running is ahead", "0.9.2", "0.9.1", false},
		{"numeric not lexical", "0.9.9", "0.10.0", true},
		{"major bump", "9.9.9", "10.0.0", true},
		{"describe of the latest is not older", "0.9.1-12-gabc123", "0.9.1", false},
		{"dirty describe of the latest", "0.9.1-12-gabc123-dirty", "0.9.1", false},
		{"dirty tree on a tag is that tag", "0.9.1-dirty", "0.9.1", false},
		{"dirty tree on a tag is behind a newer release", "0.9.1-dirty", "0.9.2", true},
		{"describe is behind a newer release", "0.9.1-12-gabc123", "0.9.2", true},
		{"dev is unknown", "dev", "9.9.9", false},
		{"empty is unknown", "", "1.0.0", false},
		{"garbage is unknown", "garbage", "1.0.0", false},
		{"two parts is unknown", "1.2", "1.3.0", false},
		{"a prerelease suffix is not a describe", "0.9.1-rc1", "0.9.2", false},
		{"garbage latest", "1.0.0", "garbage", false},
		{"empty latest", "1.0.0", "", false},
	} {
		if got := Available(c.running, c.latest); got != c.want {
			t.Errorf("%s: Available(%q, %q) = %v, want %v", c.name, c.running, c.latest, got, c.want)
		}
	}
}
