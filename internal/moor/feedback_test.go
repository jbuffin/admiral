package moor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFeedbackPutsTheTextInTheCheckoutAndKeepsItOutOfCommits(t *testing.T) {
	checkout := t.TempDir()
	got, err := WriteFeedback(checkout, 2, "line one\n[a link](https://example.com)\n")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(checkout, ".admiral", "untrusted", "feedback-2.md")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if b, _ := os.ReadFile(want); string(b) != "line one\n[a link](https://example.com)\n" {
		t.Errorf("content = %q, want the text as given", b)
	}
	for path, mode := range map[string]os.FileMode{
		filepath.Join(checkout, ".admiral"):              0o700,
		filepath.Join(checkout, ".admiral", "untrusted"): 0o700,
		want: 0o600,
	} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != mode {
			t.Errorf("%s mode = %v (%v), want %v", path, fi, err, mode)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(checkout, ".admiral", ".gitignore")); strings.TrimSpace(string(b)) != "*" {
		t.Errorf(".admiral/.gitignore = %q, want *", b)
	}
}

func TestWriteFeedbackLeavesAnExistingGitignoreAndEarlierRoundsAlone(t *testing.T) {
	checkout := t.TempDir()
	if _, err := WriteFeedback(checkout, 1, "first"); err != nil {
		t.Fatal(err)
	}
	ignore := filepath.Join(checkout, ".admiral", ".gitignore")
	if err := os.WriteFile(ignore, []byte("*\n!keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFeedback(checkout, 2, "second"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(ignore); string(b) != "*\n!keep\n" {
		t.Errorf(".admiral/.gitignore = %q, want the engineer's file untouched", b)
	}
	for round, text := range map[string]string{"1": "first", "2": "second"} {
		if b, _ := os.ReadFile(filepath.Join(checkout, ".admiral", "untrusted", "feedback-"+round+".md")); string(b) != text {
			t.Errorf("feedback-%s.md = %q, want %q", round, b, text)
		}
	}
}

// A link that stays inside the checkout is the case a plain root would follow: the target is a file the
// ship's commits or hooks would carry.
func TestWriteFeedbackDoesNotFollowALinkPlantedInTheCheckout(t *testing.T) {
	for _, c := range []struct {
		name  string
		plant func(checkout, target string)
	}{
		{"at .admiral", func(checkout, target string) {
			mustLink(t, target, filepath.Join(checkout, ".admiral"))
		}},
		{"at untrusted", func(checkout, target string) {
			mustMkdir(t, filepath.Join(checkout, ".admiral"))
			mustLink(t, target, filepath.Join(checkout, ".admiral", "untrusted"))
		}},
		{"at the file", func(checkout, target string) {
			mustMkdir(t, filepath.Join(checkout, ".admiral", "untrusted"))
			mustLink(t, filepath.Join(target, "hook"), filepath.Join(checkout, ".admiral", "untrusted", "feedback-1.md"))
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
			if _, err := WriteFeedback(checkout, 1, "planted text"); err == nil {
				t.Error("WriteFeedback followed a link, want it refused")
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

func TestWriteFeedbackDoesNotFollowALinkOutOfTheCheckout(t *testing.T) {
	checkout, outside := t.TempDir(), t.TempDir()
	mustLink(t, outside, filepath.Join(checkout, ".admiral"))
	if _, err := WriteFeedback(checkout, 1, "planted text"); err == nil {
		t.Error("WriteFeedback followed a link out of the checkout, want it refused")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("the directory outside the checkout got %d entries, want none", len(entries))
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustLink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFeedbackDoesNotWriteThroughAHardLinkPlantedAtTheFile(t *testing.T) {
	checkout, outside := t.TempDir(), filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(checkout, ".admiral", "untrusted"))
	if err := os.Link(outside, filepath.Join(checkout, ".admiral", "untrusted", "feedback-1.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFeedback(checkout, 1, "text"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Errorf("the linked file now holds %q, want it untouched", b)
	}
	if b, _ := os.ReadFile(filepath.Join(checkout, ".admiral", "untrusted", "feedback-1.md")); string(b) != "text" {
		t.Errorf("feedback-1.md = %q, want the text", b)
	}
}
