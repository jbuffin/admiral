package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installEnv is what install.sh sees: a temp HOME, the given PATH, a temp
// PREFIX and the release tarball. Nothing of the engineer's.
type installRun struct {
	home, prefix, tarball string
	path                  string
}

func (r installRun) run(t *testing.T) (stdout, stderr string, code int) {
	t.Helper()
	env := []string{
		"HOME=" + r.home,
		"PATH=" + r.path,
		"PREFIX=" + r.prefix,
		"ADMIRAL_TARBALL=" + r.tarball,
		"SHELL=/bin/sh",
	}
	return run(t, runOpts{dir: r.home, env: env}, "/bin/sh", filepath.Join(repoRoot, "packaging", "install.sh"))
}

// systemPath is a PATH of the macOS system tools (symlinked from /bin and
// /usr/bin) without python3, python or tmux, plus any extra directories first.
func systemPath(t *testing.T, extra ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, src := range []string{"/bin", "/usr/bin"} {
		ents, _ := os.ReadDir(src)
		for _, e := range ents {
			n := e.Name()
			if strings.HasPrefix(n, "python") || n == "tmux" || n == "brew" || n == "sudo" {
				continue
			}
			if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
				continue
			}
			_ = os.Symlink(filepath.Join(src, n), filepath.Join(dir, n))
		}
	}
	return strings.Join(append(append([]string{}, extra...), dir), ":")
}

// copyRelease copies the tarball and its checksum into a fresh folder so a
// test can tamper with its copy.
func copyRelease(t *testing.T, r release) (tarball string) {
	t.Helper()
	dir := t.TempDir()
	tarball = filepath.Join(dir, filepath.Base(r.tarball))
	for _, p := range [][2]string{{r.tarball, tarball}, {r.sha, tarball + ".sha256"}} {
		b, err := os.ReadFile(p[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p[1], b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tarball
}

func fakeDeps(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "tmux"), "#!/bin/sh\nexit 0\n")
	writeExec(t, filepath.Join(dir, "python3"), "#!/bin/sh\nexit 0\n")
	return dir
}

// S3: the tarball installs into PREFIX/bin, the installed binary runs, nothing
// is written outside PREFIX, and with tmux and python3 on PATH it stays quiet.
func TestPackagingS3_InstallsFromATarball(t *testing.T) {
	rel := theRelease(t)
	script, err := os.ReadFile(filepath.Join(repoRoot, "packaging", "install.sh"))
	if err != nil {
		t.Fatalf("no install script: %v", err)
	}
	if strings.Contains(string(script), "sudo") {
		t.Error("install.sh must never use sudo")
	}

	home, prefix := t.TempDir(), filepath.Join(t.TempDir(), "prefix")
	r := installRun{home: home, prefix: prefix, tarball: copyRelease(t, rel),
		path: systemPath(t, fakeDeps(t), filepath.Join(prefix, "bin"))}
	out, errOut, code := r.run(t)
	if code != 0 {
		t.Fatalf("install.sh exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	bin := filepath.Join(prefix, "bin", "admiral")
	v, _, vcode := run(t, runOpts{env: hermeticEnv(t)}, bin, "--version")
	if vcode != 0 || !strings.HasPrefix(v, "admiral "+releaseVer+" (") {
		t.Fatalf("installed binary --version: exit %d, %q", vcode, v)
	}
	if strings.Contains(out+errOut, "brew install") || strings.Contains(strings.ToLower(out+errOut), "warning") {
		t.Errorf("install with tmux, python3 and PREFIX/bin on PATH should be quiet, got:\n%s%s", out, errOut)
	}
	if ents, _ := os.ReadDir(home); len(ents) > 0 {
		t.Errorf("install.sh wrote into HOME: %v", ents)
	}
}

// S3: a tampered tarball is refused before anything is extracted.
func TestPackagingS3_RefusesATamperedTarball(t *testing.T) {
	rel := theRelease(t)
	tarball := copyRelease(t, rel)
	f, err := os.OpenFile(tarball, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("tampered")
	f.Close()

	prefix := filepath.Join(t.TempDir(), "prefix")
	r := installRun{home: t.TempDir(), prefix: prefix, tarball: tarball, path: systemPath(t, fakeDeps(t))}
	_, errOut, code := r.run(t)
	if code == 0 {
		t.Fatal("install.sh accepted a tarball that does not match its checksum")
	}
	if !strings.Contains(strings.ToLower(errOut), "checksum") {
		t.Errorf("stderr should say the checksum failed, got %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "admiral")); err == nil {
		t.Error("a binary was installed from a tampered tarball")
	}
}

// S3: missing tmux and python3 are warned about, with the command to run; a
// PREFIX/bin off PATH is warned about; the install still succeeds.
func TestPackagingS3_WarnsAboutMissingDependenciesAndPath(t *testing.T) {
	rel := theRelease(t)
	prefix := filepath.Join(t.TempDir(), "prefix")
	r := installRun{home: t.TempDir(), prefix: prefix, tarball: copyRelease(t, rel), path: systemPath(t)}
	out, errOut, code := r.run(t)
	if code != 0 {
		t.Fatalf("missing dependencies must warn, not fail: exit %d\n%s%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "admiral")); err != nil {
		t.Errorf("binary not installed: %v", err)
	}
	for _, want := range []string{"tmux", "python3", "brew install tmux python@3.15", filepath.Join(prefix, "bin"), "PATH"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

// S3: arm64 is decided by sysctl, not uname: Rosetta says x86_64 and installs;
// an Intel Mac is refused.
func TestPackagingS3_ChecksArm64WithSysctl(t *testing.T) {
	rel := theRelease(t)
	for _, tc := range []struct {
		name, sysctl string
		ok           bool
	}{{"rosetta", "1", true}, {"intel", "0", false}} {
		t.Run(tc.name, func(t *testing.T) {
			fake := t.TempDir()
			writeExec(t, filepath.Join(fake, "uname"), "#!/bin/sh\ncase \"$1\" in -m) echo x86_64;; *) exec /usr/bin/uname \"$@\";; esac\n")
			writeExec(t, filepath.Join(fake, "sysctl"), "#!/bin/sh\necho "+tc.sysctl+"\n")
			prefix := filepath.Join(t.TempDir(), "prefix")
			r := installRun{home: t.TempDir(), prefix: prefix, tarball: copyRelease(t, rel), path: systemPath(t, fake, fakeDeps(t))}
			_, errOut, code := r.run(t)
			_, statErr := os.Stat(filepath.Join(prefix, "bin", "admiral"))
			if tc.ok && (code != 0 || statErr != nil) {
				t.Errorf("an arm64 Mac under Rosetta must install: exit %d, %v, %s", code, statErr, errOut)
			}
			if !tc.ok && (code == 0 || statErr == nil || !strings.Contains(errOut, "arm64")) {
				t.Errorf("an Intel Mac must be refused naming arm64: exit %d, stderr %q", code, errOut)
			}
		})
	}
}
