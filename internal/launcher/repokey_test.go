package launcher

import (
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/permlog"
)

func TestThePermissionLogAndLaunchNameARepoByOneKey(t *testing.T) {
	cases := []struct {
		name   string
		config []string // git config key, value pairs
		want   string   // "" means the checkout's path
	}{
		{"a GitHub origin", []string{"remote.origin.url", "git@github.com:Acme/API.git"}, "Acme/API"},
		{"an origin that insteadOf expands", []string{"remote.origin.url", "gh:acme/api", "url.https://github.com/.insteadOf", "gh:"}, "acme/api"},
		{"a non-GitHub origin", []string{"remote.origin.url", "https://gitlab.example.com/acme/api.git"}, ""},
		{"a bare-path origin", []string{"remote.origin.url", "/srv/git/api.git"}, ""},
		{"no origin", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			main := t.TempDir()
			gitIn(t, main, "", "init", "-q")
			for i := 0; i+1 < len(tc.config); i += 2 {
				gitIn(t, main, "", "config", tc.config[i], tc.config[i+1])
			}
			gitIn(t, main, "", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
			work := filepath.Join(t.TempDir(), "wt")
			gitIn(t, main, "", "worktree", "add", "-q", work)

			want := tc.want
			if want == "" {
				want, _ = filepath.EvalSymlinks(main)
			}
			for dir, label := range map[string]string{main: "the checkout", work: "its worktree"} {
				if got := permlog.Repo(dir); got != RepoKey(dir) || got != want {
					t.Errorf("%s: permlog records %q and launch looks up %q, want both %q", label, got, RepoKey(dir), want)
				}
			}
		})
	}
}
