package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Helpers for the u13 scenarios (packaging, install script, formula, service).
//
// ---- The surfaces these tests assume (u13 may adjust them; tell the tests) ----
//
// make release VERSION=<v> [DIST=<dir>] [BIN=<path>] [OFFICIAL_LOGOS=<dir>]
//   Rebuilds the UI from clean, builds the darwin/arm64 binary at $(BIN)
//   (default bin/admiral) stamped with the version and the short commit, and
//   writes into $(DIST) (default dist/):
//     admiral-<v>-darwin-arm64.tar.gz
//     admiral-<v>-darwin-arm64.tar.gz.sha256     "<hex>  <tarball file name>\n"
//   $(OFFICIAL_LOGOS) (default web/logos/official) must hold no files, or the
//   target fails before it writes anything, naming "official".
//   The tarball has one top folder, admiral-<v>-darwin-arm64/, holding:
//     admiral                      the binary (mode 0755, arm64, ad-hoc signed)
//     THIRD_PARTY_NOTICES          Go modules, npm runtime deps, the OFL fonts
//     plugins/freeform/            a reference copy of the repo's plugins/freeform/
//
// admiral --version         "admiral <version> (<commit>)\n" on stdout, exit 0.
//   <commit> is the short hash of HEAD, with an optional "-dirty" suffix.
//
// packaging/install.sh      POSIX sh. Environment: PREFIX (default ~/.local),
//   ADMIRAL_TARBALL (a local tarball; its checksum is the file next to it with
//   ".sha256" appended), ADMIRAL_VERSION (download; not exercised here).
//   Installs $PREFIX/bin/admiral. Exit 0, or non-zero with the reason on
//   stderr: "checksum" for a tampered tarball, "arm64" for a non-arm64 Mac.
//   Warnings (exit still 0) go to stderr: a missing tmux/python3 names them and
//   the command "brew install tmux python@3.15"; a PREFIX/bin that is not on
//   PATH names that directory and "PATH".
//
// admiral service install | uninstall | status
//   See service_test.go. launchctl is found on PATH.

const (
	serviceLabel = "dev.jasonbuffin.admiral"
	releaseVer   = "9.8.7-test"
)

var repoRoot = func() string {
	p, err := filepath.Abs("..")
	if err != nil {
		panic(err)
	}
	return p
}()

type release struct {
	err      string // non-empty when make release failed: every dependent test fails with it
	dir      string // DIST
	bin      string // the built binary (BIN)
	tarball  string
	sha      string // the checksum file
	topDir   string // admiral-<v>-darwin-arm64
	extract  string // the tarball unpacked here; <extract>/<topDir>/...
	skipWhy  string
	makeText string
}

var (
	relOnce sync.Once
	rel     release
)

func officialLogoFiles() []string {
	ents, _ := os.ReadDir(filepath.Join(repoRoot, "web", "logos", "official"))
	var names []string
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names
}

// theRelease runs `make release` once for the whole test binary: it is slow
// (it rebuilds the UI) and every release check reads the same output.
func theRelease(t *testing.T) release {
	t.Helper()
	relOnce.Do(func() {
		if len(officialLogoFiles()) > 0 {
			rel.skipWhy = "web/logos/official/ holds personal logos: the shared release build needs a checkout without them"
			return
		}
		tmp, err := os.MkdirTemp("", "admiral-release-")
		if err != nil {
			rel.err = err.Error()
			return
		}
		rel.dir = filepath.Join(tmp, "dist")
		rel.bin = filepath.Join(tmp, "admiral")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "make", "release", "VERSION="+releaseVer, "DIST="+rel.dir, "BIN="+rel.bin)
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		rel.makeText = string(out)
		if err != nil {
			rel.err = "make release VERSION=" + releaseVer + ": " + err.Error() + "\n" + tail(string(out), 1500)
			return
		}
		rel.topDir = "admiral-" + releaseVer + "-darwin-arm64"
		rel.tarball = filepath.Join(rel.dir, rel.topDir+".tar.gz")
		rel.sha = rel.tarball + ".sha256"
		rel.extract = filepath.Join(tmp, "unpacked")
		if err := os.MkdirAll(rel.extract, 0o755); err != nil {
			rel.err = err.Error()
			return
		}
		if out, err := exec.Command("tar", "-xzf", rel.tarball, "-C", rel.extract).CombinedOutput(); err != nil {
			rel.err = "unpack " + rel.tarball + ": " + err.Error() + " " + string(out)
		}
	})
	if rel.skipWhy != "" {
		t.Skip(rel.skipWhy)
	}
	if rel.err != "" {
		t.Fatal(rel.err)
	}
	return rel
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// tarEntries lists the tarball's entry names.
func tarEntries(t *testing.T, tarball string) map[string]*tar.Header {
	t.Helper()
	f, err := os.Open(tarball)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("%s is not a gzip file: %v", tarball, err)
	}
	tr := tar.NewReader(gz)
	got := map[string]*tar.Header{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return got
		}
		if err != nil {
			t.Fatal(err)
		}
		got[strings.TrimSuffix(h.Name, "/")] = h
	}
}

func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var versionLine = regexp.MustCompile(`^admiral (\S+) \(([0-9a-f]{7,40})(-dirty)?\)\n$`)

// run runs a command and returns stdout, stderr and the exit code.
func run(t *testing.T, o runOpts, name string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = o.dir
	cmd.Env = o.env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case asExit(err, &ee) && ctx.Err() == nil:
		code = ee.ExitCode()
	default:
		t.Fatalf("%s %v: %v\nstdout: %s\nstderr: %s", name, args, err, out.String(), errb.String())
	}
	return out.String(), errb.String(), code
}

type runOpts struct {
	dir string
	env []string
}

func asExit(err error, ee **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*ee = e
	}
	return ok
}

// writeExec writes an executable script.
func writeExec(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func execOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}
