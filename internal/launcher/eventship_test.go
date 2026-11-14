package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// eventShip is an A4 event launch: an event text and no restricted level.
func (p *pinRig) eventShip() (Request, *fakeCheckouts) {
	fc := &fakeCheckouts{}
	p.l.Checkouts = fc
	p.guided(guidanceText, nil)
	return Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "none"},
		EventText: rawEvent, Template: "Triage the new issue.", WorkerLaunched: true}, fc
}

func (p *pinRig) mustEventShip(t *testing.T) (*Launched, *fakeCheckouts, []string, map[string]string) {
	t.Helper()
	req, fc := p.eventShip()
	fc.t = t
	out, err := p.l.Launch(req)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	argv, env := p.shipRecord(t)
	return out, fc, argv, env
}

func TestAnEventShipWorksInItsOwnCloneAndNotInTheRequestsDir(t *testing.T) {
	p := newPinRig(t, nil)
	out, fc, _, env := p.mustEventShip(t)
	if len(fc.calls) != 1 {
		t.Fatalf("the checkout seam was asked %d times, want once", len(fc.calls))
	}
	if got := fc.calls[0]; got[0] != p.home || got[1] != out.Session || got[2] != p.work {
		t.Errorf("seam asked for %v, want (home %q, session %q, dir %q)", got, p.home, out.Session, p.work)
	}
	if out.Options["dir"] != fc.madeFor || fc.madeFor == "" || !samePath(t, env["PWD"], fc.madeFor) {
		t.Errorf("dir option %q, cwd %q, want the clone %q, not %s", out.Options["dir"], env["PWD"], fc.madeFor, p.work)
	}
}

func TestAnEventShipGetsTheEventInItsUntrustedFileByteForByteAndNeverInItsArgs(t *testing.T) {
	p := newPinRig(t, nil)
	_, fc, argv, _ := p.mustEventShip(t)
	got, err := os.ReadFile(filepath.Join(fc.madeFor, ".admiral", "untrusted", "event.md"))
	if err != nil || string(got) != rawEvent {
		t.Errorf("event.md = %q (%v), want the event byte for byte: %q", got, err, rawEvent)
	}
	for _, a := range argv {
		for _, piece := range []string{"IGNORE ALL PREVIOUS ORDERS", "Issue 12 opened"} {
			if strings.Contains(a, piece) {
				t.Errorf("argv holds the event text %q in %q", piece, a)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(p.work, ".admiral")); err == nil {
		t.Errorf("the event launch wrote .admiral into the request's dir %s", p.work)
	}
}

func TestAnEventShipsPromptIsTheTemplateThenTheEventFileLineThenTheGuidance(t *testing.T) {
	p := newPinRig(t, nil)
	_, _, argv, _ := p.mustEventShip(t)
	want := "Triage the new issue.\n\n" + eventFileLine + "\n\n## Standing orders\n\n" + guidanceText
	if got := argv[len(argv)-1]; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
}

func TestAnEventShipIsANormalShipWithNoRestrictedProfileAndItsFleetsEnv(t *testing.T) {
	p := newPinRig(t, fleetTokens())
	out, fc, argv, env := p.mustEventShip(t)
	if fc.madeFor == "" {
		t.Error("no clone was made, so there is no event ship to check the profile of")
	}
	wantFlag(t, argv, "--settings", ClaudeSettingsPath(p.home))
	for _, f := range []string{"--permission-mode", "--setting-sources"} {
		if slices.Contains(argv, f) {
			t.Errorf("argv = %q, want no %s on a normal ship", argv, f)
		}
	}
	if _, err := os.Stat(restrictedSettingsPath(p.home, out.Session)); err == nil {
		t.Error("an event ship that is not restricted got a restricted settings file")
	}
	for _, k := range tokenVars {
		if env[k] == "" {
			t.Errorf("%s is gone from a normal event ship's env", k)
		}
	}
	if v := env["GIT_CONFIG_GLOBAL"]; v == "/dev/null" {
		t.Errorf("GIT_CONFIG_GLOBAL = %q, want no restricted stub on a normal ship", v)
	}
}

func TestAnEventShipWhoseCloneFailsStartsNoShip(t *testing.T) {
	p := newPinRig(t, nil)
	req, fc := p.eventShip()
	fc.err = errors.New("no room for a checkout")
	_, err := p.l.Launch(req)
	if err == nil || !strings.Contains(err.Error(), "no room for a checkout") {
		t.Fatalf("Launch = %v, want the checkout's error", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("the checkout seam was asked %d times, want once", len(fc.calls))
	}
	if out, err := exec.Command(p.l.Tmux, "-S", p.l.Socket, "list-sessions").CombinedOutput(); err == nil {
		t.Errorf("a refused launch left sessions: %s", out)
	}
}

func TestALateFailureOfAnEventShipRemovesItsClone(t *testing.T) {
	p := newPinRig(t, nil)
	req, fc := p.eventShip()
	fc.t = t
	req.Before = func(string) error { return errors.New("stop here") }
	if _, err := p.l.Launch(req); err == nil {
		t.Fatal("Launch succeeded, want Before's error")
	}
	if fc.madeFor == "" {
		t.Fatal("no clone was made for the event ship")
	}
	if _, err := os.Stat(fc.madeFor); err == nil {
		t.Errorf("the clone %s outlived the failed launch", fc.madeFor)
	}
}

func TestARelaunchOfAnEventShipKeepsItsCloneAndItsEventFile(t *testing.T) {
	p := newPinRig(t, nil)
	req, fc := p.eventShip()
	fc.t = t
	req.EventText = ""
	req.Session, req.Name = "kelpie", "Kelpie"
	kept := filepath.Join(p.home, "checkouts", "kelpie")
	if err := os.MkdirAll(filepath.Join(kept, ".admiral", "untrusted"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, ".admiral", "untrusted", "event.md"), []byte(rawEvent), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := p.l.Launch(req)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if len(fc.calls) != 0 {
		t.Errorf("the seam was asked to make a clone over a kept one: %v", fc.calls)
	}
	if !samePath(t, out.Options["dir"], kept) {
		t.Errorf("dir = %s, want the kept clone %s", out.Options["dir"], kept)
	}
	if b, err := os.ReadFile(filepath.Join(kept, ".admiral", "untrusted", "event.md")); err != nil || string(b) != rawEvent {
		t.Errorf("event.md = %q (%v), want the first launch's event kept", b, err)
	}
}

func TestAnEventShipInARealCloneIsOnItsOwnBranchWithAdmiralExcludedFromGit(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	p.l.Checkouts = GitCheckouts{}
	p.guided(guidanceText, nil)
	out, err := p.l.Launch(Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "none"},
		EventText: rawEvent, Template: "Triage.", WorkerLaunched: true})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	dir := out.Options["dir"]
	if dir == p.work {
		t.Fatal("the ship's dir is the request's Dir, want its own clone")
	}
	if !samePath(t, dir, filepath.Join(p.home, "checkouts", out.Session)) {
		t.Errorf("dir = %s, want $ADMIRAL_HOME/checkouts/%s", dir, out.Session)
	}
	if got := git(t, dir, "branch", "--show-current"); got != "admiral/"+out.Session {
		t.Errorf("branch = %q, want admiral/%s", got, out.Session)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude")); err != nil || !strings.Contains(string(b), ".admiral/") {
		t.Errorf("the clone's info/exclude = %q (%v), want .admiral/ in it", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".admiral", "untrusted", "event.md")); err != nil || string(b) != rawEvent {
		t.Errorf("event.md = %q (%v), want the event", b, err)
	}
	if status := git(t, dir, "status", "--porcelain"); strings.Contains(status, ".admiral") {
		t.Errorf("git status in the clone shows .admiral: %q", status)
	}
}
