package github

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func scriptGH(t *testing.T, answer string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gh")
	script := "#!/bin/sh\nif [ \"$1\" = auth ]; then echo fake-token-work-2; exit 0; fi\nprintf '%s' '" + answer + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestItemURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/o/r/pull/7":              "https://github.com/o/r/pull/7",
		"https://GitHub.com/o/r/pull/7/files?w=1":    "https://github.com/o/r/pull/7",
		"https://github.com/o/r/issues/12#issue-1":   "https://github.com/o/r/issues/12",
		"https://github.com/o/r/blob/main/README.md": "",
		"https://ghe.example.com/o/r/pull/7":         "",
		"https://github.com/o/r/pull/7 trailing":     "",
	} {
		got, ok := ItemURL(in, "github.com")
		if got != want || ok != (want != "") {
			t.Errorf("ItemURL(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestItemTitle(t *testing.T) {
	cl := NewClient("github.com", "", nil, os.Environ())
	cl.Gh = scriptGH(t, `{"data":{"r":{"title":"  Fix the thing "}}}`)
	title, found, err := cl.ItemTitle(context.Background(), "https://github.com/o/r/pull/7")
	if err != nil || !found || title != "Fix the thing" {
		t.Errorf("ItemTitle = %q, %v, %v", title, found, err)
	}

	cl.Gh = scriptGH(t, `{"data":{"r":null},"errors":[{"type":"NOT_FOUND","path":["r","x"]}]}`)
	if title, found, err := cl.ItemTitle(context.Background(), "https://github.com/o/r/issues/8"); err != nil || found || title != "" {
		t.Errorf("ItemTitle of a gone issue = %q, %v, %v; want not found", title, found, err)
	}
}
