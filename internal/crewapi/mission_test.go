package crewapi_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jbuffin/admiral/internal/crewapi"
)

func TestKeepMissionCreatesTheMissionsDirectoryAndWritesTheTextOnce(t *testing.T) {
	home := t.TempDir()
	const text = "---\nname: sweep\n---\n\nRead every file ✓\n"
	written, err := crewapi.KeepMission(home, "work", "sweep", text)
	if err != nil || !written {
		t.Fatalf("KeepMission = %v, %v, want written", written, err)
	}
	dir := filepath.Join(home, "fleets", "work", "missions")
	if b, err := os.ReadFile(filepath.Join(dir, "sweep.md")); err != nil || string(b) != text {
		t.Errorf("sweep.md = %q (%v)", b, err)
	}
	for _, d := range []string{filepath.Join(home, "fleets"), filepath.Join(home, "fleets", "work"), dir} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s = %v (%v), want mode 0700", d, fi, err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("missions holds %v, want no temp file left", entries)
	}
	if written, err := crewapi.KeepMission(home, "work", "sweep", text); err != nil || written {
		t.Errorf("the same text again = %v, %v, want kept without a write", written, err)
	}
}

func TestKeepMissionRefusesADifferentFileWithAMissionRefusedErrorAndLeavesIt(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "fleets", "work", "missions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sweep.md"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	written, err := crewapi.KeepMission(home, "work", "sweep", "new")
	var refused *crewapi.MissionRefusedError
	if written || !errors.As(err, &refused) {
		t.Fatalf("KeepMission = %v, %v, want a *MissionRefusedError", written, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "sweep.md")); string(b) != "old" {
		t.Errorf("sweep.md = %q, want it unchanged", b)
	}
}

func TestKeepMissionRefusesAnyNameOrKeyThatIsNotASafeSingleComponent(t *testing.T) {
	home := t.TempDir()
	for _, c := range [][2]string{{"work", "../x"}, {"work", "a/b"}, {"work", "Bad"}, {"work", ""}, {"../work", "sweep"}, {"", "sweep"}, {"a/b", "sweep"}} {
		if written, err := crewapi.KeepMission(home, c[0], c[1], "text"); written || err == nil {
			t.Errorf("KeepMission(%q, %q) = %v, %v, want an error", c[0], c[1], written, err)
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("home holds %v, want nothing written", entries)
	}
	if written, err := crewapi.KeepMission(home, "work", "sweep", "text"); err != nil || !written {
		t.Errorf("a safe name and key = %v, %v, want written", written, err)
	}
}

func TestKeepMissionErrorsOnALinkOrANonDirectoryOnThePath(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, home, outside string){
		"fleets is a link": func(t *testing.T, home, outside string) {
			if err := os.Symlink(outside, filepath.Join(home, "fleets")); err != nil {
				t.Fatal(err)
			}
		},
		"missions is a link": func(t *testing.T, home, outside string) {
			if err := os.MkdirAll(filepath.Join(home, "fleets", "work"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(home, "fleets", "work", "missions")); err != nil {
				t.Fatal(err)
			}
		},
		"missions is a file": func(t *testing.T, home, outside string) {
			if err := os.MkdirAll(filepath.Join(home, "fleets", "work"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "fleets", "work", "missions"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"the mission file is a link": func(t *testing.T, home, outside string) {
			dir := filepath.Join(home, "fleets", "work", "missions")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "sweep.md"), filepath.Join(dir, "sweep.md")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			home, outside := t.TempDir(), t.TempDir()
			setup(t, home, outside)
			if written, err := crewapi.KeepMission(home, "work", "sweep", "text"); written || err == nil {
				t.Errorf("KeepMission = %v, %v, want an error", written, err)
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Errorf("the link's target holds %v, want nothing written through it", entries)
			}
		})
	}
	if written, err := crewapi.KeepMission(t.TempDir(), "work", "sweep", "text"); err != nil || !written {
		t.Errorf("with no link in the path = %v, %v, want written", written, err)
	}
}

func TestKeepMissionRacersWriteOneFileAndTheRestSeeItKept(t *testing.T) {
	home := t.TempDir()
	var wg sync.WaitGroup
	results := make([]bool, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			written, err := crewapi.KeepMission(home, "work", "sweep", "same text")
			if err != nil {
				t.Errorf("racer %d: %v", i, err)
			}
			results[i] = written
		}()
	}
	wg.Wait()
	n := 0
	for _, w := range results {
		if w {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d racers report writing the file, want exactly one", n)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "fleets", "work", "missions", "sweep.md")); string(b) != "same text" {
		t.Errorf("sweep.md = %q", b)
	}
}
