package github

import "testing"

func TestValidRepoAcceptsOwnerSlashNameOnly(t *testing.T) {
	for repo, want := range map[string]bool{
		"octocat/admiral-crew": true,
		"Hub_bot/crew.v2":      true,
		"a/b":                  true,
		"":                     false,
		"octocat":              false,
		"a/b/c":                false,
		"/b":                   false,
		"a/":                   false,
		"./crew":               false,
		"octocat/..":           false,
		"a b/crew":             false,
		"a/b?x=1":              false,
		"a/b%2Fc":              false,
		"é/crew":               false,
	} {
		if got := ValidRepo(repo); got != want {
			t.Errorf("ValidRepo(%q) = %v, want %v", repo, got, want)
		}
	}
}

func TestValidSegmentRefusesDotsAndAnythingOutsideThePattern(t *testing.T) {
	for seg, want := range map[string]bool{"octocat": true, "my.repo-1_x": true, "": false, ".": false, "..": false, "a/b": false, "a b": false, "a\n": false} {
		if got := ValidSegment(seg); got != want {
			t.Errorf("ValidSegment(%q) = %v, want %v", seg, got, want)
		}
	}
}
