package sessionhost

import (
	"strings"
	"testing"
)

func transcriptRow(session, crew, transcript string) string {
	f := make([]string, fixedFields+len(optionNames)+tailFields)
	f[0] = session
	for i := range optionNames {
		f[fixedFields+i] = "v"
	}
	tail := fixedFields + len(optionNames)
	f[tail], f[tail+1], f[tail+2], f[tail+5] = crew, "%1", "@1", transcript
	return strings.Join(f, sep)
}

// The reader checks the path again: a tmux option can hold whatever was set on it.
func TestParseCrewReadsTranscript(t *testing.T) {
	known := map[string]bool{"ship": true}
	for _, c := range []struct{ name, in, want string }{
		{"a valid path, spaces allowed", "/Users/me/My Projects/x/abc.jsonl", "/Users/me/My Projects/x/abc.jsonl"},
		{"a tmux format", "/a/#{pane_id}.jsonl", ""},
	} {
		crew := parseCrew([]string{transcriptRow("ship", "mate", c.in)}, known)["ship"]
		if len(crew) != 1 {
			t.Fatalf("%s: crew = %+v, want one boat, mate", c.name, crew)
		}
		if got := crew[0].Transcript; got != c.want {
			t.Errorf("%s: Transcript = %q, want %q", c.name, got, c.want)
		}
	}
}

// A crew path is quoted inside a nested tmux command and shown to drivers, so it refuses
// more than the chat's rule.
func TestValidCrewTranscriptPath(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"/a/My Projects/bé.jsonl", true},
		{"/a/it's.jsonl", false},
		{"/a/b‮c.jsonl", false},
	} {
		if got := ValidCrewTranscriptPath(c.in); got != c.want {
			t.Errorf("ValidCrewTranscriptPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
