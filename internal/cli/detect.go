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

// Detection is what setup finds on this Mac. It lists only the names in ~ for
// identity directories (story 14). The walk and the wrapper scan skip every
// dot-directory, every identity directory in use, wherever it is, and every
// detected one, so they
// never list what is inside one. Looking for a harness by name only stats
// <dir>/<name>, so that still covers the PATH entries inside an identity
// directory (Claude Code's local install puts claude in ~/.claude/local).
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
// userHome. admiralHome and the identity directories in skip are never walked,
// and no PATH entry inside one is listed for wrappers.
func Detect(userHome, pathEnv, admiralHome string, skip []string) Detection {
	d := Detection{Home: userHome, Harness: map[string]string{}}
	// The names first, so the scan below skips the detected identity directories too.
	if entries, err := os.ReadDir(userHome); err == nil {
		for _, e := range entries {
			if hasHarnessPrefix(strings.TrimPrefix(e.Name(), ".")) && strings.HasPrefix(e.Name(), ".") && (e.IsDir() || e.Type()&os.ModeSymlink != 0) {
				d.Identity = append(d.Identity, filepath.Join(userHome, e.Name()))
			}
		}
	}
	skip = append(append([]string{}, skip...), d.Identity...)
	inside := func(p string) bool {
		for _, id := range skip {
			if p == id || strings.HasPrefix(p, id+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	var scan []string // the entries whose names may be listed, for wrappers
	add := func(dir string) {
		if strings.HasPrefix(dir, "~/") {
			dir = filepath.Join(userHome, dir[2:])
		}
		if filepath.IsAbs(dir) && !seen[dir] { // a relative entry means whatever the cwd is
			seen[dir] = true
			d.Dirs = append(d.Dirs, dir)
			if !inside(dir) {
				scan = append(scan, dir)
			}
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
	for _, dir := range scan {
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

	d.Suggested = suggestRoots(userHome, admiralHome, inside)
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

func (d Detection) hasIdentity(dir string) bool {
	for _, id := range d.Identity {
		if id == dir {
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
// and ~/.copilot* are never entered), the folders in skipped, admiralHome and
// whatever skip says.
func suggestRoots(userHome, admiralHome string, skip func(string) bool) []string {
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
			if child == admiralHome || skip(child) {
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
			fmt.Fprintf(w, "Harness %s: not found on PATH or in %s\n", h, strings.Join(probeDirs, ", "))
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

// WrappersOf are the detected wrappers named <harness>-something.
func (d Detection) WrappersOf(harness string) []string {
	var out []string
	for _, w := range d.Wrappers {
		if strings.HasPrefix(filepath.Base(w), harness+"-") {
			out = append(out, w)
		}
	}
	return out
}

// WrapperFor is the wrapper made for a fleet: <harness>-<key>.
func (d Detection) WrapperFor(harness, key string) (string, bool) {
	for _, w := range d.WrappersOf(harness) {
		if filepath.Base(w) == harness+"-"+key {
			return w, true
		}
	}
	return "", false
}
