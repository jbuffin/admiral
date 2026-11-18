package skillindex_test

import (
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/skillindex"
)

func carryingRig(t *testing.T, plugins map[string][]string) (*ids, string) {
	d := newIDs(t)
	cache := filepath.Join(d.claude, "plugins", "cache", "mk")
	for p, skills := range plugins {
		d.plugin(p+"@mk", p, filepath.Join(cache, p), nil, skills...)
		d.enable(p+"@mk", true)
	}
	return d, d.project("p1")
}

func TestCarryingFindsTheExactInstalledName(t *testing.T) {
	d, p := carryingRig(t, nil)
	writeSkill(t, filepath.Join(d.claude, "skills"), "wayfinder")
	sameSet(t, "wayfinder", skillindex.Carrying("claude", d.environ(), p, "wayfinder"), []string{"wayfinder"})
}

func TestCarryingFindsAPluginSkillByItsBareName(t *testing.T) {
	d, p := carryingRig(t, map[string][]string{"a": {"wayfinder"}})
	sameSet(t, "wayfinder", skillindex.Carrying("claude", d.environ(), p, "wayfinder"), []string{"a:wayfinder"})
}

func TestCarryingFindsTheFullNamespacedName(t *testing.T) {
	d, p := carryingRig(t, map[string][]string{"a": {"wayfinder"}, "b": {"wayfinder"}})
	sameSet(t, "a:wayfinder", skillindex.Carrying("claude", d.environ(), p, "a:wayfinder"), []string{"a:wayfinder"})
}

func TestCarryingReturnsEveryPluginSkillSharingTheBareName(t *testing.T) {
	d, p := carryingRig(t, map[string][]string{"a": {"wayfinder"}, "b": {"wayfinder", "other"}})
	sameSet(t, "wayfinder", skillindex.Carrying("claude", d.environ(), p, "wayfinder"), []string{"a:wayfinder", "b:wayfinder"})
}

func TestCarryingCountsAPlainSkillAndAPluginSkillSharingTheNameAsTwo(t *testing.T) {
	d, p := carryingRig(t, map[string][]string{"a": {"wayfinder"}})
	writeSkill(t, filepath.Join(d.claude, "skills"), "wayfinder")
	sameSet(t, "wayfinder", skillindex.Carrying("claude", d.environ(), p, "wayfinder"), []string{"wayfinder", "a:wayfinder"})
}

func TestCarryingReturnsNothingWhenTheSkillIsInstalledNowhere(t *testing.T) {
	d, p := carryingRig(t, map[string][]string{"a": {"other"}})
	writeSkill(t, filepath.Join(d.claude, "skills"), "unrelated")
	sameSet(t, "wayfinder", skillindex.Carrying("claude", d.environ(), p, "wayfinder"), nil)
	sameSet(t, "other", skillindex.Carrying("claude", d.environ(), p, "other"), []string{"a:other"})
}

func TestCarryingReadsOnlyTheHarnessAskedAbout(t *testing.T) {
	d, p := carryingRig(t, nil)
	writeSkill(t, filepath.Join(d.claude, "skills"), "wayfinder")
	writeSkill(t, filepath.Join(d.codex, "skills"), "wayfinder")
	writeSkill(t, filepath.Join(d.codex, "skills"), "only-codex")
	sameSet(t, "codex", skillindex.Carrying("codex", d.environ(), p, "wayfinder"), []string{"wayfinder"})
	sameSet(t, "codex only-codex", skillindex.Carrying("codex", d.environ(), p, "only-codex"), []string{"only-codex"})
	sameSet(t, "claude only-codex", skillindex.Carrying("claude", d.environ(), p, "only-codex"), nil)
}
