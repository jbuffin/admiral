package launcher

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// pinRig is a crewDirRig whose claude dock has a Commodore with a crew/ directory of its own.
type pinRig struct {
	*crewDirRig
	commodore string // the Commodore skill folder; its crew/ is the built-in layer
}

func newPinRig(t *testing.T, fleetEnv map[string]string) *pinRig {
	t.Helper()
	identity := t.TempDir()
	env := map[string]string{"CLAUDE_CONFIG_DIR": identity}
	for k, v := range fleetEnv {
		env[k] = v
	}
	p := &pinRig{crewDirRig: newCrewDirRig(t, env), commodore: filepath.Join(identity, "skills", "commodore")}
	mustMkdir(t, filepath.Join(p.commodore, "crew"))
	if err := os.WriteFile(filepath.Join(p.commodore, "SKILL.md"), []byte("---\nname: commodore\ndescription: a fake skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *pinRig) builtIn() string { return filepath.Join(p.commodore, "crew") }
func (p *pinRig) fleet() string   { return p.crewDir() }
func (p *pinRig) repo() string    { return filepath.Join(p.work, ".commodore", "crew") }

// member writes <dir>/<name>.md and returns the bytes written.
func member(t *testing.T, dir, name, role, extraFields, body string) []byte {
	t.Helper()
	mustMkdir(t, dir)
	text := "---\nname: " + name + "\nrole: " + role + "\npick-me-when: Pick " + name + ".\n" + extraFields + "---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return []byte(text)
}

func (p *pinRig) launchPinned(crew []string) (*Launched, error) {
	return p.l.Launch(Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "commodore"}, Crew: crew})
}

func (p *pinRig) mustLaunchPinned(t *testing.T, crew []string) (*Launched, map[string]string) {
	t.Helper()
	out, err := p.launchPinned(crew)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	return out, p.recorded(t, "claude")
}

// runLayer returns the run layer the ship was told about, failing unless it is a path under the home.
func (p *pinRig) runLayer(t *testing.T, env map[string]string) string {
	t.Helper()
	run := env["ADMIRAL_RUN_CREW"]
	if !filepath.IsAbs(run) || !strings.HasPrefix(run, p.home+string(os.PathSeparator)) {
		t.Fatalf("ADMIRAL_RUN_CREW = %q, want an absolute directory under the home %s", run, p.home)
	}
	return run
}

func fileNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestPinnedMembersBecomeTheShipsRunLayer(t *testing.T) {
	// The fleet's env cannot name the run layer.
	p := newPinRig(t, map[string]string{"ADMIRAL_RUN_CREW": "/fleet/says/otherwise"})
	member(t, p.builtIn(), "scout", "scribe", "", "Built-in scout.")
	repoScout := member(t, p.repo(), "scout", "scribe", "", "Repo scout, which replaces the built-in one.")
	fleetMate := member(t, p.fleet(), "mate", "reviewer", "", "Fleet mate.")
	member(t, p.fleet(), "idle", "triager", "", "Never pinned.")

	_, env := p.mustLaunchPinned(t, []string{"scout", "mate"})

	run := p.runLayer(t, env)
	if st, err := os.Stat(run); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("run layer %s: %v, want a directory with mode 0700", run, err)
	}
	if got, want := fileNames(t, run), []string{"mate.md", "scout.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("run layer holds %v, want exactly the pinned members %v", got, want)
	}
	for name, want := range map[string][]byte{"scout.md": repoScout, "mate.md": fleetMate} {
		if got, err := os.ReadFile(filepath.Join(run, name)); err != nil || string(got) != string(want) {
			t.Errorf("run layer %s reads %q (%v), want the resolved member's file byte for byte: %q", name, got, err, want)
		}
	}
}

func TestNoPinsLeaveNoRunLayer(t *testing.T) {
	p := newPinRig(t, map[string]string{"ADMIRAL_RUN_CREW": "/fleet/says/otherwise"})
	member(t, p.builtIn(), "scout", "scribe", "", "Built-in scout.")
	for name, crew := range map[string][]string{"absent": nil, "empty": {}} {
		_, env := p.mustLaunchPinned(t, crew)
		if got, set := env["ADMIRAL_RUN_CREW"]; set {
			t.Errorf("%s crew: ADMIRAL_RUN_CREW = %q, want it unset, even though the fleet's env sets it", name, got)
		}
	}
}

func TestPinRefusalsComeBeforeAnySession(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, p *pinRig)
		crew  []string
		msg   string
	}{
		{"a name no layer resolves", func(t *testing.T, p *pinRig) {
			member(t, p.builtIn(), "scout", "scribe", "", "x")
		}, []string{"ghost"}, "ghost"},
		{"a member locked to another harness", func(t *testing.T, p *pinRig) {
			member(t, p.fleet(), "coder", "implementer", "harness: codex\n", "x")
		}, []string{"coder"}, "codex"},
		{"an unpinned file the resolver rejects", func(t *testing.T, p *pinRig) {
			member(t, p.builtIn(), "scout", "scribe", "", "x")
			mustMkdir(t, p.fleet())
			broken := "---\nname: broken\npick-me-when: Pick.\n---\n\nNo role.\n"
			if err := os.WriteFile(filepath.Join(p.fleet(), "broken.md"), []byte(broken), 0o644); err != nil {
				t.Fatal(err)
			}
		}, []string{"scout"}, "role is required"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPinRig(t, nil)
			c.setup(t, p)
			_, err := p.launchPinned(c.crew)
			if le, ok := err.(*Error); !ok || le.Status != http.StatusBadRequest || !strings.Contains(le.Msg, c.msg) {
				t.Fatalf("Launch = %v, want a 400 naming %q", err, c.msg)
			}
			rows, err := p.l.server().SessionRows()
			if err != nil || len(rows) != 0 {
				t.Errorf("sessions after the refusal: %v (%v), want none", rows, err)
			}
		})
	}
}

func TestACommodoreWithNoCrewDirectoryHasAnEmptyBuiltInLayer(t *testing.T) {
	p := newPinRig(t, nil)
	if err := os.RemoveAll(p.builtIn()); err != nil {
		t.Fatal(err)
	}
	member(t, p.fleet(), "mate", "reviewer", "", "Fleet mate.")
	if _, env := p.mustLaunchPinned(t, []string{"mate"}); fileNames(t, p.runLayer(t, env))[0] != "mate.md" {
		t.Errorf("the run layer is %q, want the fleet's mate", env["ADMIRAL_RUN_CREW"])
	}
	if _, err := p.launchPinned([]string{"scout"}); err == nil {
		t.Errorf("pinning a built-in member without a built-in layer launched a ship, want a 400")
	}
}

// A name is reused once its ship is gone; kill the first ship and let only its name stay free.
func (p *pinRig) reuse(t *testing.T, ship *Launched) {
	t.Helper()
	if out, err := exec.Command(p.l.Tmux, "-S", p.l.Socket, "kill-session", "-t", ship.Session).CombinedOutput(); err != nil {
		t.Fatalf("kill-session: %v\n%s", err, out)
	}
	p.l.Kept = func(session string) bool { return session != ship.Session }
}

func TestARunLayerIsClearedWhenItsShipNameIsReused(t *testing.T) {
	p := newPinRig(t, nil)
	member(t, p.builtIn(), "scout", "scribe", "", "Built-in scout.")
	first, env := p.mustLaunchPinned(t, []string{"scout"})
	run := p.runLayer(t, env)
	stale := filepath.Join(run, "stale.md")
	if err := os.WriteFile(stale, []byte("left by the first ship"), 0o600); err != nil {
		t.Fatal(err)
	}

	p.reuse(t, first)
	second, env := p.mustLaunchPinned(t, []string{"scout"})
	if second.Session != first.Session || env["ADMIRAL_RUN_CREW"] != run {
		t.Fatalf("the second ship is %s with run layer %s, want the first's name %s and layer %s", second.Session, env["ADMIRAL_RUN_CREW"], first.Session, run)
	}
	if got, want := fileNames(t, run), []string{"scout.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("run layer holds %v after the name was reused, want only %v", got, want)
	}

	p.reuse(t, second)
	third, env := p.mustLaunchPinned(t, nil)
	if third.Session != first.Session {
		t.Fatalf("the third ship is %s, want the reused name %s", third.Session, first.Session)
	}
	if _, set := env["ADMIRAL_RUN_CREW"]; set {
		t.Errorf("an unpinned ship has ADMIRAL_RUN_CREW = %q", env["ADMIRAL_RUN_CREW"])
	}
	if _, err := os.Stat(run); !os.IsNotExist(err) {
		t.Errorf("the previous ship's run layer survived an unpinned launch under its name (%v)", err)
	}
}

func TestRemovingAShipsFilesRemovesItsRunLayerAndNotTheFleetCrew(t *testing.T) {
	p := newPinRig(t, nil)
	member(t, p.fleet(), "mate", "reviewer", "", "Fleet mate.")
	ship, env := p.mustLaunchPinned(t, []string{"mate"})
	run := p.runLayer(t, env)

	RemoveShipFiles(p.home, ship.Session)

	if _, err := os.Stat(run); !os.IsNotExist(err) {
		t.Errorf("the run layer %s survived the removal of the ship's files (%v)", run, err)
	}
	if _, err := os.Stat(filepath.Join(p.fleet(), "mate.md")); err != nil {
		t.Errorf("the fleet's member file is gone: %v", err)
	}
}

func TestCrewBoatKeepsTheShipsRunLayer(t *testing.T) {
	// The docks set the name, so the boat's -u loop and -e env would clear or replace it.
	p := newPinRig(t, nil)
	fleet := p.l.Config().Fleets[0]
	for _, i := range []int{0, 1, 2} {
		fleet.Harnesses[i].Env["ADMIRAL_RUN_CREW"] = "/dock/says/otherwise"
	}
	member(t, p.builtIn(), "scout", "scribe", "", "Built-in scout.")
	pinned, env := p.mustLaunchPinned(t, []string{"scout"})
	run := p.runLayer(t, env)
	for _, harness := range []string{"codex", "copilot"} {
		if err := p.l.LaunchCrew(CrewRequest{Session: pinned.Session, Name: "boat-" + harness, Harness: harness, Dir: p.work, Prompt: "go"}); err != nil {
			t.Fatalf("LaunchCrew on %s: %v", harness, err)
		}
		if got := p.recorded(t, harness)["ADMIRAL_RUN_CREW"]; got != run {
			t.Errorf("a %s boat has ADMIRAL_RUN_CREW = %q, want the ship's %s", harness, got, run)
		}
	}

	unpinned, _ := p.mustLaunchPinned(t, nil)
	if err := p.l.LaunchCrew(CrewRequest{Session: unpinned.Session, Name: "boat-none", Harness: "codex", Dir: p.work, Prompt: "go"}); err != nil {
		t.Fatalf("LaunchCrew: %v", err)
	}
	if got, set := p.recorded(t, "codex")["ADMIRAL_RUN_CREW"]; set {
		t.Errorf("a boat of an unpinned ship has ADMIRAL_RUN_CREW = %q, want it unset though its dock sets one", got)
	}
}
