package ships

import (
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

func TestOwnRefsAreAListedShipsPRsAndClaimedIssues(t *testing.T) {
	list := []Ship{
		{Name: "a", Status: StatusUnderway, URL: "https://github.com/acme/widgets/issues/21"},
		{Name: "b", Status: "moored", PRs: []moor.PR{{URL: "https://github.com/acme/widgets/pull/7"}, {URL: "https://github.com/acme/widgets/pull/8"}}},
		{Name: "c", Status: StatusUnderway, Issues: []string{"https://github.com/acme/widgets/issues/30", "https://github.com/acme/widgets/issues/31"}},
	}
	for _, own := range []string{
		"https://github.com/acme/widgets/issues/21",
		"https://GitHub.com/Acme/Widgets/issues/21/",
		"https://github.com/acme/widgets/pull/7",
		"https://github.com/acme/widgets/pull/8#issuecomment-5",
		"https://github.com/acme/widgets/issues/31",
	} {
		if !OwnsRef(list, own) {
			t.Errorf("OwnsRef(%q) = false, want true: a ship holds it", own)
		}
	}
	for _, other := range []string{
		"https://github.com/acme/widgets/issues/22",
		"https://github.com/acme/widgets/pull/9",
		"https://github.com/acme/widgets/pull/21",
		"https://github.com/other/widgets/pull/7",
		"",
	} {
		if OwnsRef(list, other) {
			t.Errorf("OwnsRef(%q) = true, want false: no ship holds it", other)
		}
	}
}
