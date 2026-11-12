package sessionhost

import (
	"strconv"
	"strings"
	"testing"
)

func TestAppendRolloutKeepsTheNewestAndSkipsRepeatsAndBadPaths(t *testing.T) {
	got := AppendRollout("", "/h/a.jsonl")
	got = AppendRollout(got, "/h/b.jsonl")
	if got = AppendRollout(got, "/h/a.jsonl"); got != "/h/a.jsonl /h/b.jsonl" {
		t.Errorf("a repeat gave %q, want the list as it was", got)
	}
	for _, bad := range []string{"/h/with space.jsonl", "rel.jsonl", "/h/q'.jsonl", "/h/x.txt"} {
		if AppendRollout(got, bad) != got {
			t.Errorf("%q was added", bad)
		}
	}
	list := ""
	for i := 0; i < MaxRollouts+5; i++ {
		list = AppendRollout(list, "/h/r"+strconv.Itoa(i)+".jsonl")
	}
	paths := strings.Fields(list)
	if len(paths) != MaxRollouts || paths[0] != "/h/r5.jsonl" || paths[MaxRollouts-1] != "/h/r"+strconv.Itoa(MaxRollouts+4)+".jsonl" {
		t.Errorf("list = %d paths from %s to %s, want %d, the oldest dropped", len(paths), paths[0], paths[len(paths)-1], MaxRollouts)
	}
}

func TestClosedBoatsRoundTripAndRefuseForgedTokens(t *testing.T) {
	raw := AppendClosed("", []ClosedBoat{{"scout", "claude", "/h/a:b.jsonl"}, {"x y", "claude", "/h/c.jsonl"}, {"ok", "co:dex", "/h/c.jsonl"}})
	got := ParseClosed(raw + " nope ../x:claude:/h/d.jsonl scout:claude:/h/a:b.jsonl")
	if len(got) != 1 || got[0] != (ClosedBoat{"scout", "claude", "/h/a:b.jsonl"}) {
		t.Errorf("parsed %+v from %q, want the one valid boat once (a colon in the path is fine)", got, raw)
	}
	var list string
	for i := 0; i < MaxClosedCrew+3; i++ {
		list = AppendClosed(list, []ClosedBoat{{"b" + strconv.Itoa(i), "codex", "/h/r.jsonl"}})
	}
	if n := len(strings.Fields(list)); n != MaxClosedCrew {
		t.Errorf("%d tokens, want the bound %d", n, MaxClosedCrew)
	}
}
