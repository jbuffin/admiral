package launcher

import (
	"regexp"
	"testing"
)

// Every name is drawn once before any repeats, a repeat gets the lowest free
// numeral from II, and each session is the name's slug.
func TestShipNamesAreDistinctThenNumbered(t *testing.T) {
	used := map[string]bool{}
	sessions := map[string]bool{}
	for range len(names) + 3 {
		n := pickName(taken(used, sessions, nil))
		if used[n] {
			t.Fatalf("name %q handed out twice", n)
		}
		used[n] = true
		sessions[Slug(n)] = true
	}
	lower := map[string]string{"III": "II", "IV": "III"}
	numbered := regexp.MustCompile(`^(.+) (II|III|IV)$`)
	found := 0
	for n := range used {
		m := numbered.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		found++
		if prev, ok := lower[m[2]]; ok && !used[m[1]+" "+prev] {
			t.Errorf("%q was drawn while %s %s was free", n, m[1], prev)
		}
	}
	if found != 3 {
		t.Errorf("%d names got a numeral after every name was used, want 3", found)
	}
	if got := Slug("Saltpaw II"); got != "saltpaw-ii" {
		t.Errorf("Slug = %q, want saltpaw-ii", got)
	}
}
