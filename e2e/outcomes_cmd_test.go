package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// Records as the log stores them (docs/adr 0015): one JSON object per line.
const (
	oSaltLaunched1 = `{"v":1,"ts":"2026-08-30T10:00:00Z","ship":"saltpaw-ii","event":"launched","harness":"claude"}`
	oMistLaunched  = `{"v":1,"ts":"2026-08-31T09:00:00Z","ship":"mistral","event":"launched","harness":"codex"}`
	oSaltEnded1    = `{"v":1,"ts":"2026-08-31T23:59:59Z","ship":"saltpaw-ii","event":"ended","exit_code":0}`
	oMistNeedsYou  = `{"v":1,"ts":"2026-09-01T00:00:00Z","ship":"mistral","event":"needs_you","reason":"question"}`
	oSaltLaunched2 = `{"v":1,"ts":"2026-09-02T08:00:00Z","ship":"saltpaw-ii","event":"launched","harness":"claude"}`
	oSaltUnderway  = `{"v":1,"ts":"2026-09-03T00:00:01Z","ship":"saltpaw-ii","event":"underway","waited_s":4}`
)

// oMistEnded carries a report over 64 KiB, which bufio.Scanner's default
// limit would refuse.
var oMistEnded = `{"v":1,"ts":"2026-09-02T12:00:00Z","ship":"mistral","event":"ended","exit_code":0,"report":{"notes":"` +
	strings.Repeat("x", 100<<10) + `"}}`

// oHuge is longer than the command reads: it is skipped, and the lines after
// it still print.
var oHuge = `{"v":1,"ts":"2026-09-02T13:00:00Z","ship":"mistral","event":"ended","report":{"notes":"` +
	strings.Repeat("x", 5<<20) + `"}}`

// seedOutcomes writes two month files for two ships. The August file holds a
// line cut short, as a crash mid-write leaves one. Saltpaw II's name is used
// twice.
func seedOutcomes(w *world) {
	w.t.Helper()
	dir := filepath.Join(w.home, "outcomes")
	w.writeRaw(filepath.Join(dir, "2026-08.jsonl"), strings.Join([]string{
		oSaltLaunched1, oMistLaunched, `{"v":1,"ts":"2026-08-31T09:5`, oSaltEnded1,
	}, "\n")+"\n", 0o600)
	w.writeRaw(filepath.Join(dir, "2026-09.jsonl"), strings.Join([]string{
		oMistNeedsYou, oSaltLaunched2, oMistEnded, oHuge, oSaltUnderway,
	}, "\n")+"\n", 0o600)
}

func outLines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

func TestOutcomesCommand(t *testing.T) {
	w := newWorld(t)
	seedOutcomes(w)

	// Server not running: the command reads the files itself.
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"all, files ascending, malformed line skipped", nil,
			outLines(oSaltLaunched1, oMistLaunched, oSaltEnded1, oMistNeedsYou, oSaltLaunched2, oMistEnded, oSaltUnderway)},
		{"ship by session name", []string{"--ship", "mistral"},
			outLines(oMistLaunched, oMistNeedsYou, oMistEnded)},
		{"ship by display name through Slug", []string{"--ship", "Saltpaw II"},
			outLines(oSaltLaunched1, oSaltEnded1, oSaltLaunched2, oSaltUnderway)},
		{"ship by session name with a reused display name", []string{"--ship=saltpaw-ii"},
			outLines(oSaltLaunched1, oSaltEnded1, oSaltLaunched2, oSaltUnderway)},
		{"since a date is UTC midnight and includes it", []string{"--since", "2026-09-01"},
			outLines(oMistNeedsYou, oSaltLaunched2, oMistEnded, oSaltUnderway)},
		{"since an RFC 3339 time", []string{"--since", "2026-09-02T12:00:00Z"},
			outLines(oMistEnded, oSaltUnderway)},
		{"since an RFC 3339 time with an offset", []string{"--since", "2026-09-01T02:00:00+02:00"},
			outLines(oMistNeedsYou, oSaltLaunched2, oMistEnded, oSaltUnderway)},
		{"ship and since together", []string{"--since", "2026-09-01", "--ship", "Mistral"},
			outLines(oMistNeedsYou, oMistEnded)},
		{"nothing matches the ship", []string{"--ship", "nobody"}, ""},
		{"nothing matches the date", []string{"--since", "2027-01-01"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := w.sub(t)
			if c.want == "" {
				// An empty answer only counts where the log has records.
				if all := w.admiral("outcomes"); all.stdout == "" {
					t.Fatalf("outcomes printed nothing for a log with records; stderr %q", all.stderr)
				}
			}
			r := w.admiral(append([]string{"outcomes"}, c.args...)...)
			w.expectExit(r, 0, "outcomes "+strings.Join(c.args, " "))
			if r.stdout != c.want {
				t.Fatalf("outcomes %v printed:\n%s\nwant:\n%s", c.args, clipLong(r.stdout), clipLong(c.want))
			}
		})
	}

	t.Run("no log yet", func(t *testing.T) {
		empty := newWorld(t)
		r := empty.admiral("outcomes")
		empty.expectExit(r, 0, "outcomes with no log")
		if r.stdout != "" {
			t.Fatalf("printed %q, want nothing", r.stdout)
		}
		// The next call finds a log written since.
		empty.writeRaw(filepath.Join(empty.home, "outcomes", "2026-09.jsonl"), outLines(oSaltUnderway), 0o600)
		r = empty.admiral("outcomes")
		empty.expectExit(r, 0, "outcomes after a record was written")
		if r.stdout != outLines(oSaltUnderway) {
			t.Fatalf("printed %q, want %q", r.stdout, outLines(oSaltUnderway))
		}
	})

	for _, bad := range []string{"yesterday", "2026-13-45", ""} {
		t.Run("bad since "+bad, func(t *testing.T) {
			w := w.sub(t)
			r := w.admiral("outcomes", "--since", bad)
			w.expectExit(r, 2, "outcomes --since "+bad)
			if r.stdout != "" || !strings.Contains(r.stderr, "since") {
				t.Fatalf("want an error naming --since on stderr only; stdout %q stderr %q", r.stdout, r.stderr)
			}
		})
	}

	t.Run("positional argument", func(t *testing.T) {
		w := w.sub(t)
		r := w.admiral("outcomes", "mistral")
		w.expectExit(r, 2, "outcomes mistral")
		if r.stdout != "" {
			t.Fatalf("printed %q, want nothing", clipLong(r.stdout))
		}
	})

	t.Run("help", func(t *testing.T) {
		w := w.sub(t)
		r := w.admiral("outcomes", "--help")
		w.expectExit(r, 0, "outcomes --help")
		for _, want := range []string{"usage: admiral outcomes", "--since", "--ship"} {
			if !strings.Contains(r.stdout, want) {
				t.Fatalf("help lacks %q: %s", want, r.stdout)
			}
		}
	})
}

// clip keeps a failure message readable when a line holds a 100 KiB report.
func clipLong(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if len(l) > 200 {
			l = l[:200] + "…"
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
