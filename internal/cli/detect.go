package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Under launchd the setup terminal sees a minimal PATH, so detection also
// looks where the usual installers put commands.
var probeDirs = []string{"/opt/homebrew/bin", "/usr/local/bin", "~/.local/bin"}

// maxRootDepth is how far below ~ the search-root suggestions look.
const maxRootDepth = 3

// skipped never holds a checkout worth suggesting, or asks the Mac for
// permission to be read.
var skipped = map[string]bool{
	"Library": true, "Applications": true, "Downloads": true, "Movies": true,
	"Music": true, "Pictures": true, "Public": true, "node_modules": true,
}

// Detection is what setup finds on this Mac. It never reads inside an identity
// directory: it lists the names in ~ and nothing deeper (story 14).
type Detection struct {
	Home string
	Dirs []string // where executables are looked for: PATH, then the probes

	Harness   map[string]string // claude, codex, copilot → absolute path found
	Wrappers  []string
	Identity  []string // ~/.claude*, ~/.codex*, ~/.copilot* entries
	Suggested []string // folders under ~ that hold git checkouts
}

var harnessNames = []string{"claude", "codex", "copilot"}

// Detect looks at pathEnv (and the probe directories) and at the names in
// userHome. admiralHome is never walked.
func Detect(userHome, pathEnv, admiralHome string) Detection {
	d := Detection{Home: userHome, Harness: map[string]string{}}
	seen := map[string]bool{}
	add := func(dir string) {
		if strings.HasPrefix(dir, "~/") {
			dir = filepath.Join(userHome, dir[2:])
		}
		if filepath.IsAbs(dir) && !seen[dir] { // a relative entry means whatever the cwd is
			seen[dir] = true
			d.Dirs = append(d.Dirs, dir)
		}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		add(dir)
	}
	for _, dir := range probeDirs {
		add(dir)
	}

	for _, h := range harnessNames {
		if p, ok := d.Lookup(h); ok {
			d.Harness[h] = p
		}
	}
	wrapper := map[string]bool{}
	for _, dir := range d.Dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if _, plain := d.Harness[name]; plain || !hasHarnessPrefix(name) {
				continue
			}
			p := filepath.Join(dir, name)
			if !wrapper[p] && executable(p) {
				wrapper[p] = true
				d.Wrappers = append(d.Wrappers, p)
			}
		}
	}

	if entries, err := os.ReadDir(userHome); err == nil {
		for _, e := range entries {
			if hasHarnessPrefix(strings.TrimPrefix(e.Name(), ".")) && strings.HasPrefix(e.Name(), ".") && (e.IsDir() || e.Type()&os.ModeSymlink != 0) {
				d.Identity = append(d.Identity, filepath.Join(userHome, e.Name()))
			}
		}
	}
	d.Suggested = suggestRoots(userHome, admiralHome)
	return d
}

func hasHarnessPrefix(name string) bool {
	for _, h := range harnessNames {
		if strings.HasPrefix(name, h) {
			return true
		}
	}
	return false
}

// Lookup finds an executable name in the detection's directories. The path is
// the directory entry that matched; a symlink is not followed.
func (d Detection) Lookup(name string) (string, bool) {
	for _, dir := range d.Dirs {
		if p := filepath.Join(dir, name); executable(p) {
			return p, true
		}
	}
	return "", false
}

func executable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// suggestRoots lists the folders under home, at most maxRootDepth deep, that
// hold a git checkout. It skips every dot-directory (so ~/.claude*, ~/.codex*
// and ~/.copilot* are never entered), the folders in skipped, and admiralHome.
func suggestRoots(userHome, admiralHome string) []string {
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		holds := false
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || skipped[name] {
				continue
			}
			child := filepath.Join(dir, name)
			if child == admiralHome {
				continue
			}
			if _, err := os.Lstat(filepath.Join(child, ".git")); err == nil {
				holds = true
				continue
			}
			if depth+1 < maxRootDepth {
				walk(child, depth+1)
			}
		}
		if holds && dir != userHome {
			out = append(out, dir)
		}
	}
	walk(userHome, 0)
	return out
}

// Report prints what was found, one line each.
func (d Detection) Report(w io.Writer) {
	for _, h := range harnessNames {
		if p, ok := d.Harness[h]; ok {
			fmt.Fprintf(w, "Harness %s: %s\n", h, p)
		} else {
			fmt.Fprintf(w, "Harness %s: not found\n", h)
		}
	}
	for _, p := range d.Wrappers {
		fmt.Fprintf(w, "Wrapper: %s\n", p)
	}
	for _, p := range d.Identity {
		fmt.Fprintf(w, "Identity directory: %s\n", p)
	}
	for _, p := range d.Suggested {
		fmt.Fprintf(w, "Suggested search root: %s\n", p)
	}
}
