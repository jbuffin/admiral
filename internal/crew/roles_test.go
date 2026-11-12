package crew

import (
	"slices"
	"testing"
)

func TestRolesListsTheNineBuiltInRolesAndHandsOutACopy(t *testing.T) {
	want := []string{"implementer", "test-writer", "reviewer", "security-reviewer", "brief-checker", "triager", "target-steward", "scribe", "fix-checker"}
	got := Roles()
	if !slices.Equal(got, want) {
		t.Fatalf("Roles() = %v, want %v", got, want)
	}
	got[0] = "changed"
	if Roles()[0] != "implementer" {
		t.Error("Roles() shares its slice: a caller's edit changed the resolver's roles")
	}
}
