package launcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// installFakeWrapper puts a script where the launcher expects the admiral binary: it records its
// arguments, one per NUL-ended entry, and stays up like the wrapper would.
func installFakeWrapper(t *testing.T, home string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(home, "wrapper-argv")
	script := "#!/bin/sh\nprintf '%s\\0' \"$@\" > " + out + ".part\nmv " + out + ".part " + out + "\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(home, "bin", "admiral"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return out
}

func readNul(t *testing.T, path string) []string {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil {
			return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
		}
	}
	t.Fatalf("%s never appeared", path)
	return nil
}

var socketShape = regexp.MustCompile(fmt.Sprintf(`^/tmp/admiral-%d/[0-9a-f]+\.sock$`, os.Getuid()))

func TestCodexShipRunsBehindThePaneWrapper(t *testing.T) {
	l, work, harnessArgv := recordingLauncher(t, "codex")
	wrapperArgv := installFakeWrapper(t, l.Home)
	out, err := l.Launch(Request{Fleet: "work", Harness: "codex", Dir: work, Skill: Skill{Kind: "none"}, Text: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	_ = harnessArgv
	got := readNul(t, wrapperArgv)
	if len(got) < 5 || got[0] != "codex-pane" || got[1] != "--socket" || got[3] != "--" {
		t.Fatalf("the pane ran admiral with %q, want codex-pane --socket <path> -- <exe> <args…>", got)
	}
	sock := got[2]
	if !socketShape.MatchString(sock) || len(sock) >= 104 {
		t.Errorf("socket %q, want /tmp/admiral-<uid>/<hash>.sock under 104 bytes", sock)
	}
	if exe := got[4]; filepath.Base(exe) != "harness" {
		t.Errorf("the wrapper was given %q as the exe, want the dock's", exe)
	}
	if !slices.Contains(got[5:], "-c") || got[len(got)-1] != "do it" {
		t.Errorf("the dock exe's args %q lost the hook flags or the prompt", got[5:])
	}
	ctx := context.Background()
	pane := "=" + out.Session + ":"
	if opt, err := l.server().ReadPaneOption(ctx, pane, "codex-socket"); err != nil || opt != sock {
		t.Errorf("@admiral-codex-socket = %q, %v; want %q", opt, err, sock)
	}
	if cmd, err := l.server().ReadPaneStartCommand(ctx, pane); err != nil || !strings.Contains(cmd, sock) || !strings.Contains(cmd, filepath.Join(l.Home, "bin", "admiral")) {
		t.Errorf("pane_start_command = %q, %v; want it to name the wrapper binary and the socket", cmd, err)
	}
}

func TestEachCodexShipGetsItsOwnSocket(t *testing.T) {
	l, work, _ := recordingLauncher(t, "codex")
	wrapperArgv := installFakeWrapper(t, l.Home)
	var socks []string
	for range 2 {
		if _, err := l.Launch(Request{Fleet: "work", Harness: "codex", Dir: work, Skill: Skill{Kind: "none"}}); err != nil {
			t.Fatal(err)
		}
		socks = append(socks, readNul(t, wrapperArgv)[2])
		if err := os.Remove(wrapperArgv); err != nil {
			t.Fatal(err)
		}
	}
	if socks[0] == socks[1] {
		t.Errorf("two ships share the socket %s", socks[0])
	}
}

func TestClaudeAndCopilotShipsAreNotWrapped(t *testing.T) {
	for _, harness := range []string{"claude", "copilot"} {
		t.Run(harness, func(t *testing.T) {
			l, work, argv := recordingLauncher(t, harness)
			installFakeWrapper(t, l.Home)
			out, err := l.Launch(Request{Fleet: "work", Harness: harness, Dir: work, Skill: Skill{Kind: "none"}})
			if err != nil {
				t.Fatal(err)
			}
			if got := argv(); slices.Contains(got, "codex-pane") || slices.Contains(got, "--remote") {
				t.Errorf("a %s ship ran with %q", harness, got)
			}
			if opt, _ := l.server().ReadPaneOption(context.Background(), "="+out.Session+":", "codex-socket"); opt != "" {
				t.Errorf("a %s ship has @admiral-codex-socket = %q", harness, opt)
			}
		})
	}
}

func TestCodexCrewBoatKeepsItsOwnArgv(t *testing.T) {
	r := newCrewDirRig(t, nil)
	installFakeWrapper(t, r.home)
	ship := r.launch(t)
	r.recorded(t, "claude")
	if err := r.l.LaunchCrew(CrewRequest{Session: ship.Session, Name: "boat", Harness: "codex", Dir: r.work, Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	r.recorded(t, "codex")
	argv := readNul(t, filepath.Join(r.rec, "codex.argv"))
	if slices.Contains(argv, "--remote") || slices.Contains(argv, "codex-pane") {
		t.Errorf("a Codex crew boat ran with %q, want the plain exe's args", argv)
	}
	pane := "=" + ship.Session + ":boat"
	if opt, _ := r.l.server().ReadPaneOption(context.Background(), pane, "codex-socket"); opt != "" {
		t.Errorf("a crew boat has @admiral-codex-socket = %q", opt)
	}
}

func TestCodexDockArgsThatWouldBreakTheServerAreRefusedBeforeLaunch(t *testing.T) {
	for _, arg := range []string{"--remote", "--remote=unix:///x", "app-server"} {
		t.Run(arg, func(t *testing.T) {
			l, work, _ := recordingLauncher(t, "codex")
			installFakeWrapper(t, l.Home)
			l.Config().Fleets[0].Harnesses[0].Args = []string{"--model", "x", arg}
			_, err := l.Launch(Request{Fleet: "work", Harness: "codex", Dir: work, Skill: Skill{Kind: "none"}})
			le, ok := err.(*Error)
			if !ok || le.Status != 400 || !strings.Contains(le.Msg, arg) {
				t.Fatalf("Launch = %v, want a 400 refusal naming %q", err, arg)
			}
			if rows, _ := l.server().SessionRows(); len(rows) != 0 {
				t.Errorf("a refused launch left sessions behind: %v", rows)
			}
		})
	}
}
