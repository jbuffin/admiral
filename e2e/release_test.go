package e2e

import (
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
// signed arm64 binary, the notices and the freeform plugin.
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
	for _, name := range []string{"admiral", "THIRD_PARTY_NOTICES", "plugins/freeform/plugin.json"} {
		if entries[r.topDir+"/"+name] == nil {
			t.Errorf("tarball has no %s/%s; entries: %v", r.topDir, name, keys(entries))
		}
	}
	if h := entries[r.topDir+"/admiral"]; h != nil && h.Mode&0o111 != 0o111 {
		t.Errorf("tarball binary mode is %o, want executable", h.Mode)
	}
	for name := range entries {
		if !strings.HasPrefix(name, r.topDir) {
			t.Errorf("entry %q is outside %s/", name, r.topDir)
		}
		if strings.Contains(name, "logos/official") || strings.Contains(name, "node_modules") {
			t.Errorf("entry %q must not be in a shared release", name)
		}
	}

	// The reference copy matches the repo's plugin (u7's source of the embedded copy).
	srcDir := filepath.Join(repoRoot, "plugins", "freeform")
	srcs, err := os.ReadDir(srcDir)
	if err != nil || len(srcs) == 0 {
		t.Fatalf("plugins/freeform/ is missing from the repo (u7): %v", err)
	}
	for _, e := range srcs {
		src, _ := os.ReadFile(filepath.Join(srcDir, e.Name()))
		got, err := os.ReadFile(filepath.Join(r.extract, r.topDir, "plugins", "freeform", e.Name()))
		if err != nil || string(got) != string(src) {
			t.Errorf("tarball copy of plugins/freeform/%s differs from the repo's (%v)", e.Name(), err)
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
	pkg, _ := os.ReadFile(filepath.Join(repoRoot, "web", "package.json"))
	deps := regexp.MustCompile(`(?s)"dependencies":\s*\{(.*?)\}`).FindStringSubmatch(string(pkg))
	if deps == nil {
		t.Fatal("web/package.json has no dependencies block")
	}
	for _, m := range regexp.MustCompile(`"([^"]+)":`).FindAllStringSubmatch(deps[1], -1) {
		wants = append(wants, m[1])
	}
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
}

// S1: a file in the official-logos folder makes make release fail, naming the
// folder, before it writes a tarball.
func TestPackagingS1_OfficialLogosRefuseTheRelease(t *testing.T) {
	tmp := t.TempDir()
	logos := filepath.Join(tmp, "official")
	if err := os.MkdirAll(logos, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logos, "claude.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(tmp, "dist")
	cmd := exec.Command("make", "release", "VERSION="+releaseVer, "DIST="+dist, "BIN="+filepath.Join(tmp, "admiral"), "OFFICIAL_LOGOS="+logos)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("make release succeeded with a file in the official logos folder:\n%s", tail(string(out), 800))
	}
	if !strings.Contains(strings.ToLower(string(out)), "official") {
		t.Errorf("make release failed without naming the official logos:\n%s", tail(string(out), 800))
	}
	if m, _ := filepath.Glob(filepath.Join(dist, "*")); len(m) > 0 {
		t.Errorf("make release wrote %v despite refusing", m)
	}
}
