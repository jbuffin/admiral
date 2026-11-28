package launcher

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func (r *crewDirRig) missionsDir() string { return filepath.Join(r.home, "fleets", "work", "missions") }

func TestLaunchCreatesTheFleetMissionsDirAndKeepsItsFiles(t *testing.T) {
	t.Parallel()
	r := newCrewDirRig(t, map[string]string{"ADMIRAL_MISSIONS_DIR": "/fleet/says/otherwise"})
	r.launch(t)
	if got := r.recorded(t, "claude")["ADMIRAL_MISSIONS_DIR"]; got != r.missionsDir() {
		t.Errorf("ADMIRAL_MISSIONS_DIR = %q, want %s, whatever the fleet's env says", got, r.missionsDir())
	}
	if st, err := os.Stat(r.missionsDir()); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("missions dir %s: %v, want a directory with mode 0700", r.missionsDir(), err)
	}

	mission := filepath.Join(r.missionsDir(), "ship-it.md")
	if err := os.WriteFile(mission, []byte("a mission"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.launch(t)
	if b, err := os.ReadFile(mission); err != nil || string(b) != "a mission" {
		t.Errorf("after a second launch the mission file reads %q (%v), want it kept", b, err)
	}
}

func TestLaunchRefusesAMissionsPathThatIsALinkOrAFile(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, r *crewDirRig) (target string)
	}{
		{"missions links outside the home", func(t *testing.T, r *crewDirRig) string {
			target := t.TempDir()
			mustMkdir(t, filepath.Join(r.home, "fleets", "work"))
			mustSymlink(t, target, r.missionsDir())
			return target
		}},
		{"missions links inside the home", func(t *testing.T, r *crewDirRig) string {
			target := filepath.Join(r.home, "elsewhere")
			mustMkdir(t, target)
			mustMkdir(t, filepath.Join(r.home, "fleets", "work"))
			mustSymlink(t, target, r.missionsDir())
			return target
		}},
		{"missions is a file", func(t *testing.T, r *crewDirRig) string {
			mustMkdir(t, filepath.Join(r.home, "fleets", "work"))
			if err := os.WriteFile(r.missionsDir(), []byte("not a dir"), 0o600); err != nil {
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
			if inboxes, _ := os.ReadDir(filepath.Join(r.home, "inbox")); len(inboxes) != 0 {
				t.Errorf("a refused launch left an inbox behind: %v", inboxes)
			}
			if target == "" {
				return
			}
			if entries, _ := os.ReadDir(target); len(entries) != 0 {
				t.Errorf("the launch wrote through the link into %s: %v", target, entries)
			}
		})
	}
}

func TestCrewBoatKeepsTheShipsMissionsDir(t *testing.T) {
	t.Parallel()
	// The ship's dock sets the name, so the boat's -u loop would clear it for a dock that doesn't.
	r := newCrewDirRig(t, nil)
	cfg := r.l.Config()
	cfg.Fleets[0].Harnesses[0].Env["ADMIRAL_MISSIONS_DIR"] = "/ship/dock/says/otherwise"
	cfg.Fleets[0].Harnesses[2].Env["ADMIRAL_MISSIONS_DIR"] = "/boat/dock/says/otherwise"

	ship := r.launch(t)
	r.recorded(t, "claude")
	for _, harness := range []string{"codex", "copilot"} {
		err := r.l.LaunchCrew(CrewRequest{Session: ship.Session, Name: "boat-" + harness, Harness: harness, Dir: r.work, Prompt: "go"})
		if err != nil {
			t.Fatalf("LaunchCrew on %s: %v", harness, err)
		}
		if got := r.recorded(t, harness)["ADMIRAL_MISSIONS_DIR"]; got != r.missionsDir() {
			t.Errorf("a %s boat has ADMIRAL_MISSIONS_DIR = %q, want the ship's %s", harness, got, r.missionsDir())
		}
	}
}
