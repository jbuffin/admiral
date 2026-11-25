package crewapi_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/crewapi"
)

func TestParseImportRefAcceptsEveryForm(t *testing.T) {
	member := func(name, ref string) crewapi.ImportRef {
		return crewapi.ImportRef{Owner: "octocat", Repo: "admiral-crew", Name: name, Ref: ref}
	}
	source := crewapi.ImportRef{Owner: "octocat", Repo: "admiral-crew", Source: true}
	cases := []struct {
		in   string
		want crewapi.ImportRef
	}{
		{"octocat/admiral-crew/reviewer", member("reviewer", "")},
		{"octocat/admiral-crew/reviewer@v1", member("reviewer", "v1")},
		{"octocat/admiral-crew/reviewer@feature/x", member("reviewer", "feature/x")},
		{"octocat/admiral-crew/reviewer@" + sha40(), member("reviewer", sha40())},
		{"octocat/admiral-crew/reviewer.md", member("reviewer", "")},
		{"octocat/admiral-crew/reviewer.md@v1", member("reviewer", "v1")},
		{"  octocat/admiral-crew/reviewer\n", member("reviewer", "")},
		{"https://github.com/octocat/admiral-crew/blob/main/crew/reviewer.md", member("reviewer", "main")},
		{"https://github.com/octocat/admiral-crew/blob/feature/x/crew/reviewer.md", member("reviewer", "feature/x")},
		{"https://github.com/octocat/admiral-crew/blob/" + sha40() + "/crew/reviewer.md", member("reviewer", sha40())},
		{"https://github.com/octocat/admiral-crew/blob/crew/crew/reviewer.md", member("reviewer", "crew")},
		{"https://github.com/octocat/admiral-crew/tree/main/crew", source},
		{"https://github.com/octocat/admiral-crew/tree/feature/x/crew", source},
		{"https://github.com/octocat/admiral-crew", source},
		{"https://github.com/octocat/admiral-crew/", source},
		{"https://github.com/octocat/admiral-crew.git", source},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := crewapi.ParseImportRef(c.in)
			if err != nil || got != c.want {
				t.Errorf("ParseImportRef(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
			}
		})
	}
}

func TestParseImportRefRefusesEveryBadFormNamingTheAcceptedOnes(t *testing.T) {
	bad := []string{
		"", "a/b", "a/b/c/d", "a/b/Bad Name", "a/b/c@..", "a/b/c@-x", "a/b/c@", "a/b/c@.", "a/b/c@a/./b", "a/b/c@a//b",
		"a/b/c@/x", "a/b/c@x/", "a/b/c@a..b", "a/b/c@a b", "a/b/c@ü", "a/b/c@" + strings.Repeat("x", 256),
		"../r/name", "./r/name", "o/../name", "o/./name", "o r/r/name", "a/b/-lead", "a/b/.md",
		"https://gitlab.com/a/b/blob/main/crew/c.md", "https://example.com/a/b", "http://github.com/a/b", "https://github.com/a",
		"https://github.com/a/b/blob/main/other/c.md", "https://github.com/a/b/blob/main/crew/Bad Name.md",
		"https://github.com/a/b/blob/../crew/c.md", "https://github.com/a/b/blob/a/./b/crew/c.md",
		"https://github.com/a/b/issues/1", "https://github.com/a/b/tree/main/other",
	}
	for _, in := range bad {
		t.Run(in, func(t *testing.T) {
			got, err := crewapi.ParseImportRef(in)
			if err == nil {
				t.Fatalf("ParseImportRef(%q) = %+v; want an error", in, got)
			}
			if !strings.Contains(err.Error(), "octocat/admiral-crew/reviewer@v1") {
				t.Errorf("error %q does not name the accepted forms", err)
			}
		})
	}
}

func TestParseImportRefTakesARefOf255BytesAndNoMore(t *testing.T) {
	ok := strings.Repeat("x", 255)
	if got, err := crewapi.ParseImportRef("a/b/c@" + ok); err != nil || got.Ref != ok {
		t.Errorf("255-byte ref = %+v, %v; want accepted", got, err)
	}
	if _, err := crewapi.ParseImportRef("a/b/c@" + ok + "x"); err == nil {
		t.Error("256-byte ref accepted")
	}
}

func sha40() string { return "0123456789abcdef0123456789abcdef01234567" }

func TestWriteImportedWritesTheBytesOnceAndLeavesNoTempFile(t *testing.T) {
	f := newFixture(t)
	if err := crewapi.WriteImported(f.home, "work", "bosun", "---\nname: bosun\n---\nhi\n"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(f.crewDir(), "bosun.md")); got != "---\nname: bosun\n---\nhi\n" {
		t.Errorf("file = %q", got)
	}
	entries, _ := os.ReadDir(f.crewDir())
	for _, e := range entries {
		if e.Name() != "bosun.md" {
			t.Errorf("leftover %s in the crew directory", e.Name())
		}
	}
}

func TestWriteImportedNeverReplacesAFileEvenWithIdenticalBytes(t *testing.T) {
	for _, existing := range []string{"other bytes\n", "same bytes\n"} {
		f := newFixture(t)
		path := filepath.Join(f.crewDir(), "bosun.md")
		writeFile(t, path, existing)
		err := crewapi.WriteImported(f.home, "work", "bosun", "same bytes\n")
		if !errors.Is(err, crewapi.ErrNameTaken) {
			t.Errorf("existing %q: err = %v; want ErrNameTaken", existing, err)
		}
		if got := readFile(t, path); got != existing {
			t.Errorf("existing %q was replaced by %q", existing, got)
		}
	}
}
