package ships

import (
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

func TestRefHolderNamesTheListedShipThatHoldsAPROrAnIssue(t *testing.T) {
	t.Parallel()
	list := []Ship{
		{Name: "a", Session: "sess-a", Status: StatusUnderway, URL: "https://github.com/acme/widgets/issues/21"},
		{Name: "b", Session: "sess-b", Status: "moored", PRs: []moor.PR{{URL: "https://github.com/acme/widgets/pull/7"}, {URL: "https://github.com/acme/widgets/pull/8"}}},
		{Name: "c", Session: "sess-c", Status: StatusUnderway, Issues: []string{"https://github.com/acme/widgets/issues/30", "https://github.com/acme/widgets/issues/31"}},
	}
	for ref, holder := range map[string]string{
		"https://github.com/acme/widgets/issues/21":             "sess-a",
		"https://GitHub.com/Acme/Widgets/issues/21/":            "sess-a",
		"https://github.com/acme/widgets/pull/7":                "sess-b",
		"https://github.com/acme/widgets/pull/8#issuecomment-5": "sess-b",
		"https://github.com/acme/widgets/issues/31":             "sess-c",
	} {
		got, ok := RefHolder(list, ref)
		if !ok || got.Session != holder {
			t.Errorf("RefHolder(%q) = %q, %v, want %s: that ship holds it", ref, got.Session, ok, holder)
		}
	}
	for _, other := range []string{
		"https://github.com/acme/widgets/issues/22",
		"https://github.com/acme/widgets/pull/9",
		"https://github.com/acme/widgets/pull/21",
		"https://github.com/other/widgets/pull/7",
		"",
	} {
		if got, ok := RefHolder(list, other); ok {
			t.Errorf("RefHolder(%q) = %q, true, want no holder", other, got.Session)
		}
	}
}

func TestAShipWhoseOrderURLIsAPRHoldsItInAnySpellingWhateverItsStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []string{StatusUnderway, "needs_you", "anchored"} {
		list := []Ship{{Name: "dory", Session: "sess-dory", Status: status, URL: "https://github.com/acme/widgets/pull/7"}}
		for _, ref := range []string{
			"https://github.com/acme/widgets/pull/7",
			"https://GitHub.com/Acme/Widgets/pull/7/",
			"https://github.com/acme/widgets/pull/7#issuecomment-12",
			"https://github.com/acme/widgets/pull/7/files",
		} {
			if got, ok := RefHolder(list, ref); !ok || got.Session != "sess-dory" {
				t.Errorf("status %s: RefHolder(%q) = %q, %v, want sess-dory", status, ref, got.Session, ok)
			}
		}
	}
}

func TestAShipWhoseURLIsAnotherPRDoesNotHoldThisOne(t *testing.T) {
	t.Parallel()
	list := []Ship{{Name: "dory", Session: "sess-dory", Status: StatusUnderway, URL: "https://github.com/acme/widgets/pull/7/files"}}
	for _, ref := range []string{
		"https://github.com/acme/widgets/pull/8",
		"https://github.com/acme/widgets/pull/70",
		"https://github.com/other/widgets/pull/7",
		"https://github.com/acme/gadgets/pull/7",
	} {
		if got, ok := RefHolder(list, ref); ok {
			t.Errorf("RefHolder(%q) = %q, true, want none: the ship's URL is a different PR", ref, got.Session)
		}
	}
}
