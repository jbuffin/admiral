package routines_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// sharedCheckout is the checkout every launchRoutine uses: its origin is https://github.com/acme/api.git.
var sharedCheckout string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "routines-checkout-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sharedCheckout = filepath.Join(dir, "api")
	if err := writeCheckout(sharedCheckout, [][2]string{{"origin", "https://github.com/acme/api.git"}}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func writeCheckout(dir string, remotes [][2]string) error {
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		return err
	}
	cfg := "[core]\n\trepositoryformatversion = 0\n"
	for _, r := range remotes {
		cfg += fmt.Sprintf("[remote %q]\n\turl = %s\n", r[0], r[1])
	}
	return os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o644)
}

// checkoutWith makes a git checkout whose config holds the given name/url remotes, in order; none makes a bare repo with no remote.
func checkoutWith(t *testing.T, remotes ...[2]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "checkout")
	if err := writeCheckout(dir, remotes); err != nil {
		t.Fatal(err)
	}
	return dir
}
