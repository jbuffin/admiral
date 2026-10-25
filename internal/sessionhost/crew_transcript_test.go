package sessionhost

import (
	"strings"
	"testing"
)

// A crew row's transcript path (#175, ADR 0021) is a tail field before the
// subagents. tailFields is 7 once it is there; the rows here are sized by that
// number, not by tailFields, so a reader that has not moved fails on what it
// reads, not on a panic.
const wantTailFields = 7

func transcriptRow(session, crew, transcript string) string {
	f := make([]string, fixedFields+len(optionNames)+wantTailFields)
	f[0] = session
	for i := range optionNames {
		f[fixedFields+i] = "v"
	}
	tail := fixedFields + len(optionNames)
	f[tail], f[tail+1], f[tail+2] = crew, "%1", "@1"
	if crew != "" {
		f[tail+5] = transcript
	} else {
		f[tail+6] = `[{"id":"a1","type":"Explore","start":"2026-10-24T10:00:00Z"}]`
	}
	return strings.Join(f, sep)
}

func TestParseCrewReadsTranscript(t *testing.T) {
	known := map[string]bool{"ship": true}
	read := func(transcript string) string {
		t.Helper()
		crew := parseCrew([]string{transcriptRow("ship", "mate", transcript)}, known)["ship"]
		if len(crew) != 1 {
			t.Fatalf("crew = %+v, want one boat, mate", crew)
		}
		return crew[0].Transcript
	}
	for _, c := range []struct{ name, in, want string }{
		{"a valid path, spaces allowed", "/Users/me/My Projects/x/abc.jsonl", "/Users/me/My Projects/x/abc.jsonl"},
		{"empty", "", ""},
		{"relative", "x/abc.jsonl", ""},
		{"not .jsonl", "/a/abc.txt", ""},
		{"a refused character", "/a/it's.jsonl", ""},
		{"a tmux format", "/a/#{pane_id}.jsonl", ""},
	} {
		if got := read(c.in); got != c.want {
			t.Errorf("%s: Transcript = %q, want %q", c.name, got, c.want)
		}
	}
}

// The ship's subagents sit after the transcript field, and a ship's own row has
// no transcript.
func TestParseRowsShipSubagentsAfterTranscript(t *testing.T) {
	got, unreadable := parseRows([]string{transcriptRow("ship", "", "")}, map[string]bool{"ship": true})
	if len(unreadable) != 0 {
		t.Fatalf("unreadable = %v, want none (the row has %d tail fields)", unreadable, wantTailFields)
	}
	if s := got["ship"]; len(s.Subagents) != 1 || s.Subagents[0].ID != "a1" {
		t.Errorf("subagents = %+v, want a1 at the moved index", s.Subagents)
	}
}

// The crew rule refuses what the chat's rule allows: invalid UTF-8 (which
// encoding/json never delivers to the hook, but a tmux option can hold), the C1
// controls and the bidi formatting characters.
func TestValidCrewTranscriptPath(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"/a/My Projects/b.jsonl", true},
		{"/a/b\xffc.jsonl", false},
		{"/a/b\u0085c.jsonl", false},
		{"/a/b\u202ec.jsonl", false},
		{"/a/b\u2066c.jsonl", false},
		{"/a/b\u2069c.jsonl", false},
		{"/a/b\u00e9c.jsonl", true},
	} {
		if got := ValidCrewTranscriptPath(c.in); got != c.want {
			t.Errorf("ValidCrewTranscriptPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
