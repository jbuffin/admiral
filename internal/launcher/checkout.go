package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitCheckouts makes a restricted ship's checkout as a shared clone of the request's directory, so the
// ship's .git sits inside its cwd and nothing is written under the directory's own .git.
type GitCheckouts struct{}

func (GitCheckouts) Make(home, session, dir string) (path string, err error) {
	if session == "." || !filepath.IsLocal(session) || strings.ContainsRune(session, filepath.Separator) {
		return "", fmt.Errorf("%q is not a name a ship can have", session)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return "", err
	}
	path = filepath.Join(home, "checkouts", session)
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	branch := "admiral/" + session
	fail := func(what string, out []byte, err error) (string, error) {
		_ = os.RemoveAll(path)
		return "", fmt.Errorf("%s: %v: %s", what, err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "clone", "--shared", "--quiet", "--", dir, path).CombinedOutput(); err != nil {
		return fail("git clone of "+dir, out, err)
	}
	if out, err := exec.Command("git", "-C", path, "checkout", "--quiet", "-b", branch).CombinedOutput(); err != nil {
		return fail("git checkout -b "+branch, out, err)
	}
	exclude := filepath.Join(path, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return fail("exclude", nil, err)
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = f.WriteString("\n.admiral/\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		return fail("exclude", nil, err)
	}
	return path, nil
}
