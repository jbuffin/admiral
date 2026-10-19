package e2e

import (
	"bytes"
	"debug/macho"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// `admiral --version` works on the ordinary build too: a stamp or "dev", the
// commit, exit 0, nothing else on stdout.
func TestPackagingVersionFlag(t *testing.T) {
	out, errOut, code := run(t, runOpts{env: hermeticEnv(t)}, binaryPath(t), "--version")
	if code != 0 {
		t.Fatalf("admiral --version exit %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !versionLine.MatchString(out) && !strings.HasPrefix(out, "admiral dev") {
		t.Fatalf("admiral --version printed %q, want \"admiral <version> (<commit>)\"", out)
	}
}

// S1: make release builds the tarball and its checksum; the tarball holds the
// signed arm64 binary, the notices and both plugins Admiral ships.
func TestPackagingS1_ReleaseTarball(t *testing.T) {
	r := theRelease(t)

	sumFile, err := os.ReadFile(r.sha)
	if err != nil {
		t.Fatalf("no checksum file next to the tarball: %v", err)
	}
	want := sha256Hex(t, r.tarball) + "  " + filepath.Base(r.tarball) + "\n"
	if string(sumFile) != want {
		t.Fatalf("checksum file is %q, want %q", sumFile, want)
	}

	entries := tarEntries(t, r.tarball)
	for _, name := range []string{"admiral", "LICENSE", "THIRD_PARTY_NOTICES", "plugins/freeform/plugin.json", "plugins/github/plugin.json"} {
		if entries[r.topDir+"/"+name] == nil {
			t.Errorf("tarball has no %s/%s; entries: %v", r.topDir, name, keys(entries))
		}
	}
	if h := entries[r.topDir+"/admiral"]; h != nil && h.Mode&0o111 != 0o111 {
		t.Errorf("tarball binary mode is %o, want executable", h.Mode)
	}
	for name, h := range entries {
		if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" {
			t.Errorf("entry %q carries the builder's owner (%d:%d %q:%q)", name, h.Uid, h.Gid, h.Uname, h.Gname)
		}
		if !strings.HasPrefix(name, r.topDir) {
			t.Errorf("entry %q is outside %s/", name, r.topDir)
		}
		if strings.Contains(name, "logos/official") || strings.Contains(name, "node_modules") {
			t.Errorf("entry %q must not be in a shared release", name)
		}
	}

	// install.sh sits next to the tarball, ready to upload with it.
	script, _ := os.ReadFile(filepath.Join(repoRoot, "packaging", "install.sh"))
	if got, err := os.ReadFile(filepath.Join(r.dir, "install.sh")); err != nil || string(got) != string(script) {
		t.Errorf("DIST/install.sh is not a copy of packaging/install.sh (%v)", err)
	}

	// The reference copies match the repo's plugins (the source of the embedded copies).
	for _, plugin := range []string{"freeform", "github"} {
		srcDir := filepath.Join(repoRoot, "plugins", plugin)
		srcs, err := os.ReadDir(srcDir)
		if err != nil || len(srcs) == 0 {
			t.Fatalf("plugins/%s/ is missing from the repo: %v", plugin, err)
		}
		for _, e := range srcs {
			src, _ := os.ReadFile(filepath.Join(srcDir, e.Name()))
			got, err := os.ReadFile(filepath.Join(r.extract, r.topDir, "plugins", plugin, e.Name()))
			if err != nil || string(got) != string(src) {
				t.Errorf("tarball copy of plugins/%s/%s differs from the repo's (%v)", plugin, e.Name(), err)
			}
		}
	}
}

// S1: the stamped version and commit, the signature and the architecture, on
// the binary in the tarball and the one `make release` left at BIN.
func TestPackagingS1_BinaryIsStampedSignedAndArm64(t *testing.T) {
	r := theRelease(t)
	head, _, _ := run(t, runOpts{dir: repoRoot}, "git", "rev-parse", "HEAD")
	head = strings.TrimSpace(head)

	for name, bin := range map[string]string{
		"tarball": filepath.Join(r.extract, r.topDir, "admiral"),
		"BIN":     r.bin,
	} {
		out, errOut, code := run(t, runOpts{env: hermeticEnv(t)}, bin, "--version")
		m := versionLine.FindStringSubmatch(out)
		if code != 0 || m == nil {
			t.Fatalf("%s binary: --version exit %d, stdout %q, stderr %q; want \"admiral %s (<commit>)\"", name, code, out, errOut, releaseVer)
		}
		if m[1] != releaseVer {
			t.Errorf("%s binary reports version %q, want %q", name, m[1], releaseVer)
		}
		if !strings.HasPrefix(head, m[2]) {
			t.Errorf("%s binary reports commit %q, which is not a prefix of HEAD %s", name, m[2], head)
		}
		if _, errOut, code := run(t, runOpts{}, "codesign", "-v", bin); code != 0 {
			t.Errorf("codesign -v %s: exit %d: %s", name, code, errOut)
		}
		f, err := macho.Open(bin)
		if err != nil {
			t.Errorf("%s binary is not Mach-O: %v", name, err)
			continue
		}
		if f.Cpu != macho.CpuArm64 {
			t.Errorf("%s binary is %v, want arm64", name, f.Cpu)
		}
		f.Close()
	}
}

// S1 + story 129: a shared build carries the lettered placeholders, and the
// notices cover every runtime dependency and font.
func TestPackagingS1_SharedBuildHasPlaceholdersAndNotices(t *testing.T) {
	r := theRelease(t)

	bin, err := os.ReadFile(r.bin)
	if err != nil {
		t.Fatal(err)
	}
	// -trimpath: no build path, and so no user name, in a shared binary.
	home, _ := os.UserHomeDir()
	if bytes.Contains(bin, []byte("/Users/")) || (home != "" && bytes.Contains(bin, []byte(home))) {
		t.Errorf("the release binary contains a local path (/Users/ or %s)", home)
	}
	for _, letters := range []string{"Cl", "Co", "Cx"} {
		if !regexp.MustCompile("[\"'`]" + letters + "[\"'`]").Match(bin) {
			t.Errorf("the embedded UI has no %q placeholder letters", letters)
		}
	}
	// The symbol ids are built as `logo-${harness}`, so only the unknown one is a literal.
	if !strings.Contains(string(bin), "logo-unknown") {
		t.Errorf("the embedded UI has no logo symbol ids")
	}

	notices, err := os.ReadFile(filepath.Join(r.extract, r.topDir, "THIRD_PARTY_NOTICES"))
	if err != nil {
		t.Fatal(err)
	}
	var wants []string
	gomod, _ := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	for _, m := range regexp.MustCompile(`(?m)^\s*(?:require\s+)?([a-z0-9.-]+\.[a-z]+/\S+)\s+v`).FindAllStringSubmatch(string(gomod), -1) {
		wants = append(wants, m[1])
	}
	// Every production package in the lockfile, transitive ones included.
	lockData, err := os.ReadFile(filepath.Join(repoRoot, "web", "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Packages map[string]struct {
			Dev         bool `json:"dev"`
			DevOptional bool `json:"devOptional"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(lockData, &lock); err != nil {
		t.Fatal(err)
	}
	for k, p := range lock.Packages {
		if name, ok := strings.CutPrefix(k, "node_modules/"); ok && !p.Dev && !p.DevOptional {
			wants = append(wants, name)
		}
	}
	if !slices.Contains(wants, "@preact/signals-core") {
		t.Fatalf("the lockfile should list @preact/signals-core as a production package; derived %v", wants)
	}
	wants = append(wants, "go1.", "Octicons")
	fonts, _ := filepath.Glob(filepath.Join(repoRoot, "web", "public", "licenses", "OFL-*.txt"))
	if len(fonts) == 0 {
		t.Fatal("no OFL licence files under web/public/licenses/")
	}
	for _, f := range fonts {
		b, _ := os.ReadFile(f)
		wants = append(wants, strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0]))
	}
	if len(wants) < 8 {
		t.Fatalf("expected at least 8 notice subjects, derived %v", wants)
	}
	for _, w := range wants {
		if !strings.Contains(string(notices), w) {
			t.Errorf("THIRD_PARTY_NOTICES does not mention %q", w)
		}
	}
	// Every entry (a "-- " header) has licence text under it.
	for _, sec := range strings.Split("\n"+string(notices), "\n-- ")[1:] {
		header, body, _ := strings.Cut(sec, "\n")
		if i := strings.Index(body, "\n== "); i >= 0 {
			body = body[:i]
		}
		if len(strings.TrimSpace(body)) < 40 {
			t.Errorf("THIRD_PARTY_NOTICES entry %q has no licence text", header)
		}
	}
}

// S1: official logos in the folder make make release fail, naming it, before it
// writes anything. A symlinked file and a folder that is a symlink count too.
func TestPackagingS1_OfficialLogosRefuseTheRelease(t *testing.T) {
	refused := func(t *testing.T, extra ...string) {
		t.Helper()
		dist := filepath.Join(t.TempDir(), "dist")
		args := append([]string{"release", "VERSION=" + releaseVer, "DIST=" + dist, "BIN=" + filepath.Join(t.TempDir(), "admiral")}, extra...)
		cmd := exec.Command("make", args...)
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("make release succeeded with official logos present:\n%s", tail(string(out), 800))
		}
		if !strings.Contains(strings.ToLower(string(out)), "official") {
			t.Errorf("make release failed without naming the official logos:\n%s", tail(string(out), 800))
		}
		if m, _ := filepath.Glob(filepath.Join(dist, "*")); len(m) > 0 {
			t.Errorf("make release wrote %v despite refusing", m)
		}
	}
	// A folder with logos in it, and the same logos behind symlinks.
	withLogo := func(t *testing.T) string {
		d := filepath.Join(t.TempDir(), "official")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "claude.svg"), []byte("<svg/>"), 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}
	t.Run("file", func(t *testing.T) { refused(t, "OFFICIAL_LOGOS="+withLogo(t)) })
	t.Run("symlinked file", func(t *testing.T) {
		real := withLogo(t)
		d := filepath.Join(t.TempDir(), "official")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(real, "claude.svg"), filepath.Join(d, "claude.svg")); err != nil {
			t.Fatal(err)
		}
		refused(t, "OFFICIAL_LOGOS="+d)
	})
	t.Run("symlinked folder", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "official")
		if err := os.Symlink(withLogo(t), link); err != nil {
			t.Fatal(err)
		}
		refused(t, "OFFICIAL_LOGOS="+link)
	})
}

// make release with an empty version is refused, not built as admiral--darwin.
func TestPackagingS1_EmptyVersionRefusesTheRelease(t *testing.T) {
	dist := filepath.Join(t.TempDir(), "dist")
	cmd := exec.Command("make", "release", "VERSION=", "DIST="+dist, "BIN="+filepath.Join(t.TempDir(), "admiral"))
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "VERSION is empty") {
		t.Fatalf("make release VERSION= should fail saying VERSION is empty: %v\n%s", err, tail(string(out), 600))
	}
}

// One version rule: a v-prefixed version (a v0.1.0 tag) is released as 0.1.0, in
// the tarball name and in --version, so install.sh's URL and the formula agree.
func TestPackagingS1_ALeadingVIsDropped(t *testing.T) {
	if len(officialLogoFiles()) > 0 {
		t.Skip("web/logos/official/ holds personal logos")
	}
	tmp := t.TempDir()
	dist, bin := filepath.Join(tmp, "dist"), filepath.Join(tmp, "admiral")
	cmd := exec.Command("make", "release", "VERSION=v9.8.7-test", "DIST="+dist, "BIN="+bin)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make release VERSION=v9.8.7-test: %v\n%s", err, tail(string(out), 1200))
	}
	if _, err := os.Stat(filepath.Join(dist, "admiral-9.8.7-test-darwin-arm64.tar.gz")); err != nil {
		got, _ := filepath.Glob(filepath.Join(dist, "*"))
		t.Errorf("no admiral-9.8.7-test-darwin-arm64.tar.gz; DIST holds %v", got)
	}
	out, _, code := run(t, runOpts{env: hermeticEnv(t)}, bin, "--version")
	if m := versionLine.FindStringSubmatch(out); code != 0 || m == nil || m[1] != "9.8.7-test" {
		t.Errorf("--version printed %q (exit %d), want version 9.8.7-test without the v", out, code)
	}
}
