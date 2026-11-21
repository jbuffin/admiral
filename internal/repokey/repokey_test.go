package repokey

import (
	"os/exec"
	"testing"
)

func TestConfiguredSlugReadsTheOriginAsConfiguredNotAsRewritten(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "https://github.com/acme/widgets.git"},
		{"config", "url./tmp/local-mirror/.insteadOf", "https://github.com/"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if slug, _ := OriginSlug(dir); slug == "acme/widgets" {
		t.Skip("this git does not apply insteadOf to get-url")
	}
	if got := ConfiguredSlug(dir); got != "acme/widgets" {
		t.Errorf("ConfiguredSlug = %q, want acme/widgets", got)
	}
	if got := ConfiguredSlug(t.TempDir()); got != "" {
		t.Errorf("ConfiguredSlug of a dir with no origin = %q, want empty", got)
	}
}
