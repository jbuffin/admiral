package github

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// prReadyGH is a gh that records the argv of every call but auth and fails with the content of $FAKEGH_REST/fail.
const prReadyGH = `#!/bin/sh
d="$FAKEGH_REST"
if [ "$1" = auth ]; then echo fake-token-work-2; exit 0; fi
mkdir -p "$d/calls"
n=$(ls "$d/calls" | wc -l)
printf '%s\0' "$@" > "$d/calls/$n.argv"
if [ -e "$d/fail" ]; then cat "$d/fail" >&2; exit 1; fi
`

func TestMarkReadyForReviewRunsGhPrReadyWithTheURLAndNothingElse(t *testing.T) {
	g := newRestGH(t, "")
	if err := os.WriteFile(g.gh, []byte(prReadyGH), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := g.client("github.com", "").MarkReadyForReview(context.Background(), prA); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(g.dir, "calls", "*.argv"))
	if len(files) != 1 {
		t.Fatalf("gh ran %d times, want once", len(files))
	}
	raw, _ := os.ReadFile(files[0])
	if got, want := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"), []string{"pr", "ready", prA}; !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestMarkReadyForReviewReturnsGhsMessageWhenGhFails(t *testing.T) {
	g := newRestGH(t, "")
	if err := os.WriteFile(g.gh, []byte(prReadyGH), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g.dir, "fail"), []byte("gh: Pull request #7 is not a draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := g.client("github.com", "").MarkReadyForReview(context.Background(), prA)
	if err == nil || !strings.Contains(err.Error(), "is not a draft") {
		t.Fatalf("MarkReadyForReview = %v, want gh's message", err)
	}
}

func TestTheSearchAsksWhetherAPullRequestIsADraft(t *testing.T) {
	f := newFake(t)
	f.answer(t, map[string]string{"json": `{"data":{"search":{"nodes":[]}}}`})
	if _, err := NewClient("github.com", "", nil, f.env).Search(context.Background(), "fake-token-work-2", "is:open"); err != nil {
		t.Fatal(err)
	}
	if q := strings.Join(f.only(t, "api").argv, " "); !strings.Contains(q, "isDraft") {
		t.Errorf("the search does not ask for isDraft: %s", q)
	}
}
