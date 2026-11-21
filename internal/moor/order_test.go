package moor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteOrderPutsTheTextInTheCheckoutKeepsItOutOfCommitsAndReplacesAnEarlierOne(t *testing.T) {
	checkout := t.TempDir()
	want := filepath.Join(checkout, ".admiral", "untrusted", "order.md")
	for _, text := range []string{"first [a link](https://example.com)\n", "second"} {
		got, err := WriteOrder(checkout, text)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if b, _ := os.ReadFile(want); string(b) != text {
			t.Errorf("order.md = %q, want %q", b, text)
		}
	}
	if fi, err := os.Stat(want); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("order.md mode = %v (%v), want 0600", fi, err)
	}
	if b, _ := os.ReadFile(filepath.Join(checkout, ".admiral", ".gitignore")); strings.TrimSpace(string(b)) != "*" {
		t.Errorf(".admiral/.gitignore = %q, want *", b)
	}
}

func TestWriteOrderDoesNotFollowALinkPlantedAtTheFileOrTheDirectories(t *testing.T) {
	for _, c := range []struct {
		name  string
		plant func(checkout, target string)
	}{
		{"at .admiral", func(checkout, target string) { mustLink(t, target, filepath.Join(checkout, ".admiral")) }},
		{"at the file", func(checkout, target string) {
			mustMkdir(t, filepath.Join(checkout, ".admiral", "untrusted"))
			mustLink(t, filepath.Join(target, "hook"), filepath.Join(checkout, ".admiral", "untrusted", "order.md"))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkout := t.TempDir()
			target := filepath.Join(checkout, ".git", "hooks")
			mustMkdir(t, target)
			if err := os.WriteFile(filepath.Join(target, "hook"), []byte("original"), 0o700); err != nil {
				t.Fatal(err)
			}
			c.plant(checkout, target)
			if _, err := WriteOrder(checkout, "planted text"); err == nil {
				t.Error("WriteOrder followed a link, want it refused")
			}
			entries, _ := os.ReadDir(target)
			for _, e := range entries {
				b, _ := os.ReadFile(filepath.Join(target, e.Name()))
				if e.Name() != "hook" || string(b) != "original" {
					t.Errorf("the link's target now holds %s = %q, want only the original hook", e.Name(), b)
				}
			}
		})
	}
}
