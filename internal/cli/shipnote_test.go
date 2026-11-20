// Tests for `admiral ship-note`, the context a Claude ship gets at session start.
package cli

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shipNoteRig is a ship's environment with a tmux on PATH that logs any call, so a test sees whether the note touched tmux.
type shipNoteRig struct {
	home, user, tmuxLog string
}

func newShipNoteRig(t *testing.T, ship string) *shipNoteRig {
	t.Helper()
	r := &shipNoteRig{home: t.TempDir(), user: t.TempDir()}
	bin := t.TempDir()
	r.tmuxLog = filepath.Join(t.TempDir(), "tmux.log")
	script := "#!/bin/sh\necho \"$@\" >> '" + r.tmuxLog + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", r.user)
	t.Setenv("ADMIRAL_HOME", "")
	t.Setenv("ADMIRAL_PORT", "")
	t.Setenv("ADMIRAL_SCOUT", "")
	t.Setenv("ADMIRAL_SHIP", ship)
	t.Setenv("ADMIRAL_REPORT", "")
	if ship != "" {
		t.Setenv("ADMIRAL_REPORT", filepath.Join(r.home, "reports", ship+".json"))
	}
	t.Setenv("TMUX", filepath.Join(r.home, "tmux.sock")+",4242,0")
	t.Setenv("TMUX_PANE", "%7")
	return r
}

func (r *shipNoteRig) run(t *testing.T, stdin string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if err := ShipNote(nil, IO{In: strings.NewReader(stdin), Out: &out, Err: &errb}); err != nil {
		t.Errorf("ship-note returned %v; it always exits 0", err)
	}
	if errb.Len() != 0 {
		t.Errorf("ship-note wrote %q to stderr", errb.String())
	}
	return out.String()
}

// wantUntouched fails when the note called tmux or wrote any file under the ship's home or the user's home.
func (r *shipNoteRig) wantUntouched(t *testing.T) {
	t.Helper()
	if b, err := os.ReadFile(r.tmuxLog); err == nil {
		t.Errorf("ship-note ran tmux: %q", b)
	}
	for _, dir := range []string{r.home, r.user} {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && p != dir {
				t.Errorf("ship-note wrote %s", p)
			}
			return nil
		})
	}
}

const sessionStartPayload = `{"hook_event_name":"SessionStart","source":"compact","session_id":"3f6c2a10-0d4e-4c55-9a9e-8f1e2b7c6d01"}`

func shipNoteContext(t *testing.T, out string) string {
	t.Helper()
	var got struct {
		Hook struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("stdout %q is not the SessionStart hookSpecificOutput JSON: %v", out, err)
	}
	if got.Hook.Event != "SessionStart" {
		t.Errorf("hookEventName = %q; want SessionStart", got.Hook.Event)
	}
	return got.Hook.Context
}

func TestShipNoteTellsAShipBothCampaignCommands(t *testing.T) {
	r := newShipNoteRig(t, "saltpaw-ii")
	note := shipNoteContext(t, r.run(t, sessionStartPayload))
	for _, want := range []string{"admiral campaigns follow", "admiral campaigns link"} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q does not name %q", note, want)
		}
	}
	r.wantUntouched(t)
}

func TestShipNoteIsTheSameFixedTextInEveryShip(t *testing.T) {
	first := shipNoteContext(t, newShipNoteRig(t, "saltpaw-ii").run(t, sessionStartPayload))
	second := shipNoteContext(t, newShipNoteRig(t, "kelpie-iv").run(t, ""))
	if first != second {
		t.Errorf("the note differs between ships:\n%q\n%q", first, second)
	}
	for _, name := range []string{"saltpaw", "kelpie"} {
		if strings.Contains(first, name) {
			t.Errorf("note %q names the ship %q", first, name)
		}
	}
}

func TestShipNotePrintsNothingInAScoutOrOutsideAShip(t *testing.T) {
	t.Run("scout", func(t *testing.T) {
		r := newShipNoteRig(t, "saltpaw-ii")
		t.Setenv("ADMIRAL_SCOUT", filepath.Join(t.TempDir(), "checkout"))
		if out := r.run(t, sessionStartPayload); out != "" {
			t.Errorf("ship-note in a scout printed %q; a scout can't follow", out)
		}
		r.wantUntouched(t)
	})
	t.Run("outside a ship", func(t *testing.T) {
		r := newShipNoteRig(t, "")
		if out := r.run(t, sessionStartPayload); out != "" {
			t.Errorf("ship-note outside a ship printed %q", out)
		}
		r.wantUntouched(t)
	})
}
