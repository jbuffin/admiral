package skillindex_test

import (
	"reflect"
	"testing"

	"github.com/jbuffin/admiral/internal/skillindex"
)

func sk(names ...string) []skillindex.Skill {
	var out []skillindex.Skill
	for _, n := range names {
		out = append(out, skillindex.Skill{Name: n})
	}
	return out
}

func entry(name, desc string, harnesses ...string) skillindex.Entry {
	return skillindex.Entry{Name: name, Description: desc, Harnesses: harnesses, Aliases: []string{name}}
}

func aka(e skillindex.Entry, names ...string) skillindex.Entry {
	e.Aliases = names
	return e
}

func TestMergeNamesOneSkillOnce(t *testing.T) {
	for _, c := range []struct {
		what string
		in   []skillindex.HarnessSkills
		want []skillindex.Entry
	}{
		{"a plugin skill nobody else has takes its bare name",
			[]skillindex.HarnessSkills{{Harness: "claude", Skills: sk("p:foo")}},
			[]skillindex.Entry{aka(entry("foo", "", "claude"), "foo", "p:foo")}},
		{"two plugins' skills of one bare name keep their full names",
			[]skillindex.HarnessSkills{{Harness: "claude", Skills: sk("a:tdd", "b:tdd")}},
			[]skillindex.Entry{entry("a:tdd", "", "claude"), entry("b:tdd", "", "claude")}},
		{"a bare skill and a plugin's of the same bare name both stay as they are",
			[]skillindex.HarnessSkills{{Harness: "claude", Skills: sk("tdd", "a:tdd")}},
			[]skillindex.Entry{entry("a:tdd", "", "claude"), entry("tdd", "", "claude")}},
		{"a plugin skill on one harness and a bare one on another are one skill",
			[]skillindex.HarnessSkills{{Harness: "claude", Skills: sk("mattpocock-skills:tdd")}, {Harness: "codex", Skills: sk("tdd")}},
			[]skillindex.Entry{aka(entry("tdd", "", "claude", "codex"), "mattpocock-skills:tdd", "tdd")}},
		{"a collision on one harness does not rename the other harness's bare skill",
			[]skillindex.HarnessSkills{{Harness: "claude", Skills: sk("tdd", "a:tdd")}, {Harness: "codex", Skills: sk("tdd")}},
			[]skillindex.Entry{entry("a:tdd", "", "claude"), entry("tdd", "", "claude", "codex")}},
		{"harnesses are sorted whatever the dock order",
			[]skillindex.HarnessSkills{{Harness: "codex", Skills: sk("lint")}, {Harness: "claude", Skills: sk("lint")}},
			[]skillindex.Entry{entry("lint", "", "claude", "codex")}},
		{"entries are sorted by name",
			[]skillindex.HarnessSkills{{Harness: "claude", Skills: sk("zed", "alpha")}, {Harness: "codex", Skills: sk("mid")}},
			[]skillindex.Entry{entry("alpha", "", "claude"), entry("mid", "", "codex"), entry("zed", "", "claude")}},
	} {
		if got := skillindex.Merge(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.what, got, c.want)
		}
	}
}

func TestMergeKeepsTheFirstNonEmptyDescription(t *testing.T) {
	got := skillindex.Merge([]skillindex.HarnessSkills{
		{Harness: "claude", Skills: []skillindex.Skill{{Name: "p:tdd", Description: ""}, {Name: "review", Description: "from claude"}}},
		{Harness: "codex", Skills: []skillindex.Skill{{Name: "tdd", Description: "from codex"}, {Name: "review", Description: "from codex too"}}},
	})
	want := []skillindex.Entry{entry("review", "from claude", "claude", "codex"), aka(entry("tdd", "from codex", "claude", "codex"), "p:tdd", "tdd")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMergeOfNothingIsEmptyNotNil(t *testing.T) {
	if got := skillindex.Merge(nil); got == nil || len(got) != 0 {
		t.Errorf("got %#v, want an empty non-nil list", got)
	}
}
