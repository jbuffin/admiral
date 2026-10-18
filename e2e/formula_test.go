package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// S2: the formula parses as Ruby and carries what the spec asks of it. brew
// style and brew audit are not run here (they need a tap and install gems into
// Homebrew); the engineer runs them once the tap exists.
func TestPackagingS2_FormulaParsesAndDeclaresItsDependencies(t *testing.T) {
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby is not on PATH: the formula syntax check was skipped, not passed")
	}
	path := filepath.Join(repoRoot, "packaging", "homebrew", "admiral.rb")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no formula: %v", err)
	}
	if out, err := exec.Command(ruby, "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("ruby -c: %v\n%s", err, out)
	}

	f := string(b)
	for _, want := range []string{
		`depends_on "tmux"`,
		`depends_on "python@3.15"`,
		`depends_on arch: :arm64`,
		`depends_on :macos`,
		`launchctl kickstart -k gui/`,
		serviceLabel,
		`admiral setup`,
		`admiral service install`,
	} {
		if !strings.Contains(f, want) {
			t.Errorf("formula lacks %q", want)
		}
	}
	if !regexp.MustCompile(`(?s)test do.*admiral.*--version.*end`).MatchString(f) {
		t.Errorf("formula has no test block running admiral --version")
	}
	if strings.Contains(f, `"python@3"`) {
		t.Errorf("python@3 is an alias that brew audit --strict rejects: use python@3.15")
	}
}
