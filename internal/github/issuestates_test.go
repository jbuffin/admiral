package github

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordingGH writes every gh call's argv to a file as NUL-separated words, two NULs ending a call.
func recordingGH(t *testing.T) (bin, rec string) {
	t.Helper()
	dir := t.TempDir()
	rec = filepath.Join(dir, "argv")
	bin = filepath.Join(dir, "gh")
	script := `#!/bin/sh
if [ "$1" = auth ]; then echo fake-token-work-2; exit 0; fi
for a in "$@"; do printf '%s\0' "$a" >> "$REC"; done
printf '\0\0' >> "$REC"
echo '{"data":{"viewer":{"login":"fake-viewer"}}}'
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, rec
}

func recordedCalls(t *testing.T, rec string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(rec)
	if err != nil {
		return nil
	}
	var calls [][]string
	// A call's last word ends in a NUL and the script adds two more, so a call's separator leaves one NUL on the next.
	for _, c := range strings.Split(string(raw), "\x00\x00") {
		if c = strings.TrimPrefix(c, "\x00"); c != "" {
			calls = append(calls, strings.Split(strings.TrimSuffix(c, "\x00"), "\x00"))
		}
	}
	return calls
}

func TestIssueStatesNeverPutsAnOwnerOrRepoWhereGhReadsAFlag(t *testing.T) {
	bin, rec := recordingGH(t)
	cl := NewClient("github.com", "", nil, append(os.Environ(), "REC="+rec))
	cl.Gh = bin
	urls := []string{
		"https://github.com/-x/widgets/issues/3",
		"https://github.com/acme/--web/issues/4",
	}
	_, _ = cl.IssueStates(context.Background(), urls)

	calls := recordedCalls(t, rec)
	if len(calls) == 0 {
		t.Fatalf("IssueStates ran no gh call for %v", urls)
	}
	for _, argv := range calls {
		for i, a := range argv {
			for _, bad := range []string{"-x", "--web"} {
				if a == bad || strings.HasPrefix(a, bad+"=") {
					t.Errorf("argv %q has %q as a word of its own: gh would read it as a flag", argv, a)
				}
				if strings.Contains(a, bad) && !strings.HasPrefix(a, "query=") && !(i > 0 && (argv[i-1] == "-f" || argv[i-1] == "-F")) && !strings.HasPrefix(a, "https://") {
					t.Errorf("argv %q carries %q in %q, outside a query, a -f value or a URL", argv, bad, a)
				}
			}
		}
	}
	joined := strings.Join(calls[0], " ")
	if !strings.Contains(joined, "-x") || !strings.Contains(joined, "--web") {
		t.Errorf("argv %q never names the issues' owner or repo: the lookup asks about nothing", calls[0])
	}
}

func TestIssueStatesAsksOnceForEveryURL(t *testing.T) {
	bin, rec := recordingGH(t)
	cl := NewClient("github.com", "", nil, append(os.Environ(), "REC="+rec))
	cl.Gh = bin
	_, _ = cl.IssueStates(context.Background(), []string{"https://github.com/acme/widgets/issues/3", "https://github.com/acme/widgets/issues/5", "https://github.com/globex/app/issues/1"})
	calls := recordedCalls(t, rec)
	if len(calls) != 1 {
		t.Fatalf("%d gh calls (%q), want one batched call", len(calls), calls)
	}
	q := strings.Join(calls[0], " ")
	for _, want := range []string{"widgets", "globex", "3", "5", "1"} {
		if !strings.Contains(q, want) {
			t.Errorf("the one call %q does not mention %q", q, want)
		}
	}
}
