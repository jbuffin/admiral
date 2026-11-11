package transcriptfs_test

// Opening a Copilot session's events.jsonl as strictly as Claude's projects/.

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/jbuffin/admiral/internal/transcriptfs"
)

const copilotID = "5d1f7a3e-2b4c-4e8a-9c61-3f0a7b8d9e12"

// copilotHome is a Copilot home holding one session, with no projects/ so only the Copilot layout can match.
func copilotHome(t *testing.T) (home, events string) {
	t.Helper()
	home = t.TempDir()
	dir := filepath.Join(home, "session-state", copilotID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	events = filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(events, []byte(`{"type":"session.start"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, events
}

func TestCopilotEventsFileIsOpenedAndItsSessionIDIsTheDirectory(t *testing.T) {
	home, events := copilotHome(t)

	f, info, err := transcriptfs.Open(home, events)
	if err != nil {
		t.Fatalf("Open(%s) = %v", events, err)
	}
	f.Close()
	if !info.Mode().IsRegular() {
		t.Errorf("not regular: %v", info.Mode())
	}
	if id, ok := transcriptfs.SessionID(events); !ok || id != copilotID {
		t.Errorf("SessionID = %q, %v", id, ok)
	}
	if _, ok := transcriptfs.SessionID(filepath.Join(home, "session-state", "not-a-uuid", "events.jsonl")); ok {
		t.Error("a directory that is not a uuid gave a session id")
	}
}

func TestCopilotSessionStateMayBeALinkLikeProjects(t *testing.T) {
	home, events := copilotHome(t)
	real := filepath.Join(home, "session-state")
	moved := filepath.Join(t.TempDir(), "state")
	if err := os.Rename(real, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, real); err != nil {
		t.Fatal(err)
	}
	f, _, err := transcriptfs.Open(home, events)
	if err != nil {
		t.Fatalf("a linked session-state/ was refused: %v", err)
	}
	f.Close()
}

func TestCopilotRefusals(t *testing.T) {
	home, events := copilotHome(t)
	// The control: the layout is accepted, so each refusal below is about its own change.
	if f, _, err := transcriptfs.Open(home, events); err != nil {
		t.Fatalf("the untouched layout was refused: %v", err)
	} else {
		f.Close()
	}

	dir := filepath.Dir(events)
	state := filepath.Join(home, "session-state")
	other := t.TempDir()
	write := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("outside session-state", func(t *testing.T) {
		p := filepath.Join(home, "config.json")
		write(p)
		if _, _, err := transcriptfs.Open(home, p); !errors.Is(err, transcriptfs.ErrRefused) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("another file in the session directory", func(t *testing.T) {
		p := filepath.Join(dir, "workspace.yaml")
		write(p)
		if _, _, err := transcriptfs.Open(home, p); !errors.Is(err, transcriptfs.ErrRefused) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("directory that is not a uuid", func(t *testing.T) {
		p := filepath.Join(state, "sneaky", "events.jsonl")
		write(p)
		if _, _, err := transcriptfs.Open(home, p); !errors.Is(err, transcriptfs.ErrRefused) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("events.jsonl is a link", func(t *testing.T) {
		id := "11111111-2222-4333-8444-555555555555"
		target := filepath.Join(other, "events.jsonl")
		write(target)
		p := filepath.Join(state, id, "events.jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		if _, _, err := transcriptfs.Open(home, p); !errors.Is(err, transcriptfs.ErrRefused) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("session directory is a link", func(t *testing.T) {
		id := "22222222-3333-4444-8555-666666666666"
		write(filepath.Join(other, "events.jsonl"))
		if err := os.Symlink(other, filepath.Join(state, id)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := transcriptfs.Open(home, filepath.Join(state, id, "events.jsonl")); !errors.Is(err, transcriptfs.ErrRefused) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("events.jsonl is a fifo", func(t *testing.T) {
		id := "33333333-4444-4555-8666-777777777777"
		p := filepath.Join(state, id, "events.jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := transcriptfs.Open(home, p); !errors.Is(err, transcriptfs.ErrRefused) {
			t.Errorf("err = %v", err)
		}
	})
}
