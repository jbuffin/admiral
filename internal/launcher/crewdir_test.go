package launcher

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

// crewDirRig is a launcher on its own tmux server whose harnesses record their environment.
type crewDirRig struct {
	l    *Launcher
	home string
	work string
	rec  string
}

// writeRecorder makes a harness that writes its environment to $REC_FILE, then stays up.
func writeRecorder(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "recorder")
	script := "#!/bin/sh\nenv > \"$REC_FILE.tmp\" && mv \"$REC_FILE.tmp\" \"$REC_FILE\"\nexec sleep 300\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func newCrewDirRig(t *testing.T, fleetEnv map[string]string) *crewDirRig {
	t.Helper()
	tmux, err := exec.LookPath("tmux")
	if testing.Short() {
		t.Skip("needs tmux: skipped under -short")
	}
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	tmp, err := os.MkdirTemp("/tmp", "cd")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(tmp, "tmux.sock")
	t.Setenv("TMUX_TMPDIR", tmp)
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", sock, "kill-server").Run()
		_ = os.RemoveAll(tmp)
	})
	r := &crewDirRig{home: filepath.Join(tmp, "home"), work: filepath.Join(tmp, "work"), rec: filepath.Join(tmp, "rec")}
	for _, d := range []string{r.home, r.work, r.rec} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe := writeRecorder(t)
	dock := func(name string, env map[string]string) config.Harness {
		e := map[string]string{"REC_FILE": filepath.Join(r.rec, name)}
		for k, v := range fleetEnv {
			e[k] = v
		}
		for k, v := range env {
			e[k] = v
		}
		return config.Harness{Name: name, Executable: exe, Found: true, Env: e}
	}
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{r.work},
		Harnesses: []config.Harness{dock("claude", nil), dock("codex", nil), dock("copilot", nil)}}}}
	r.l = &Launcher{Tmux: tmux, Socket: sock, Home: r.home, Config: func() *config.Config { return cfg }}
	return r
}

func (r *crewDirRig) launch(t *testing.T) *Launched {
	t.Helper()
	out, err := r.l.Launch(Request{Fleet: "work", Harness: "claude", Dir: r.work, Skill: Skill{Kind: "none"}})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	return out
}

// recorded waits for the harness to write its environment and returns it.
func (r *crewDirRig) recorded(t *testing.T, harness string) map[string]string {
	t.Helper()
	path := filepath.Join(r.rec, harness)
	deadline := time.Now().Add(15 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			env := map[string]string{}
			for _, line := range strings.Split(string(b), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok {
					env[k] = v
				}
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			return env
		}
		if time.Now().After(deadline) {
			t.Fatalf("the %s harness never recorded its environment", harness)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (r *crewDirRig) crewDir() string { return filepath.Join(r.home, "fleets", "work", "crew") }

func TestLaunchCreatesTheFleetCrewDirAndKeepsItsFiles(t *testing.T) {
	r := newCrewDirRig(t, map[string]string{"ADMIRAL_CREW_DIR": "/fleet/says/otherwise"})
	r.launch(t)
	if got := r.recorded(t, "claude")["ADMIRAL_CREW_DIR"]; got != r.crewDir() {
		t.Errorf("ADMIRAL_CREW_DIR = %q, want %s, whatever the fleet's env says", got, r.crewDir())
	}
	if st, err := os.Stat(r.crewDir()); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("crew dir %s: %v, want a directory with mode 0700", r.crewDir(), err)
	}

	member := filepath.Join(r.crewDir(), "scout.md")
	if err := os.WriteFile(member, []byte("a member"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.launch(t)
	if b, err := os.ReadFile(member); err != nil || string(b) != "a member" {
		t.Errorf("after a second launch the member file reads %q (%v), want it kept", b, err)
	}
}

func TestLaunchRefusesACrewDirPathThatIsALinkOrAFile(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, r *crewDirRig) (target string)
	}{
		{"fleets/<key> links outside the home", func(t *testing.T, r *crewDirRig) string {
			target := t.TempDir()
			mustMkdir(t, filepath.Join(r.home, "fleets"))
			mustSymlink(t, target, filepath.Join(r.home, "fleets", "work"))
			return target
		}},
		{"fleets/<key> links inside the home", func(t *testing.T, r *crewDirRig) string {
			target := filepath.Join(r.home, "elsewhere")
			mustMkdir(t, target)
			mustMkdir(t, filepath.Join(r.home, "fleets"))
			mustSymlink(t, target, filepath.Join(r.home, "fleets", "work"))
			return target
		}},
		{"fleets links outside the home", func(t *testing.T, r *crewDirRig) string {
			target := t.TempDir()
			mustSymlink(t, target, filepath.Join(r.home, "fleets"))
			return target
		}},
		{"crew is a file", func(t *testing.T, r *crewDirRig) string {
			mustMkdir(t, filepath.Join(r.home, "fleets", "work"))
			if err := os.WriteFile(r.crewDir(), []byte("not a dir"), 0o600); err != nil {
				t.Fatal(err)
			}
			return ""
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newCrewDirRig(t, nil)
			target := c.setup(t, r)
			_, err := r.l.Launch(Request{Fleet: "work", Harness: "claude", Dir: r.work, Skill: Skill{Kind: "none"}})
			if le, ok := err.(*Error); !ok || le.Status != http.StatusInternalServerError {
				t.Fatalf("Launch = %v, want a 500 refusal", err)
			}
			if target == "" {
				return
			}
			entries, _ := os.ReadDir(target)
			if len(entries) != 0 {
				t.Errorf("the launch wrote through the link into %s: %v", target, entries)
			}
		})
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestCrewBoatKeepsTheShipsCrewDir(t *testing.T) {
	// The ship's dock sets the name, so the boat's -u loop would clear it for a dock that doesn't.
	r := newCrewDirRig(t, nil)
	shipDock := r.l.Config().Fleets[0].Harnesses[0]
	shipDock.Env["ADMIRAL_CREW_DIR"] = "/ship/dock/says/otherwise"
	boatDock := r.l.Config().Fleets[0].Harnesses[2]
	boatDock.Env["ADMIRAL_CREW_DIR"] = "/boat/dock/says/otherwise"
	r.l.Config().Fleets[0].Harnesses[0] = shipDock
	r.l.Config().Fleets[0].Harnesses[2] = boatDock

	ship := r.launch(t)
	r.recorded(t, "claude")
	for _, harness := range []string{"codex", "copilot"} {
		err := r.l.LaunchCrew(CrewRequest{Session: ship.Session, Name: "boat-" + harness, Harness: harness, Dir: r.work, Prompt: "go"})
		if err != nil {
			t.Fatalf("LaunchCrew on %s: %v", harness, err)
		}
		if got := r.recorded(t, harness)["ADMIRAL_CREW_DIR"]; got != r.crewDir() {
			t.Errorf("a %s boat has ADMIRAL_CREW_DIR = %q, want the ship's %s", harness, got, r.crewDir())
		}
	}
}
