package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type changesFile struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

type changesBody struct {
	State     string        `json:"state"`
	Files     []changesFile `json:"files"`
	Diff      string        `json:"diff"`
	Truncated bool          `json:"truncated"`
}

func changesWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.writeCue("exit 0")
	w.start()
	return w
}

func (w *world) changes(session string) (int, changesBody) {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/ships/"+url.PathEscape(session)+"/changes", nil)
	var b changesBody
	_ = json.Unmarshal([]byte(body), &b)
	return code, b
}

func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// featureRepo is a checkout on a branch one commit ahead of main, with an
// uncommitted edit and an untracked file.
func (w *world) featureRepo(name string) string {
	w.t.Helper()
	d := w.checkout(name)
	w.git(d, "checkout", "-q", "-b", "main")
	writeRepoFile(w.t, d, "base.txt", "one\ntwo\nthree\n")
	writeRepoFile(w.t, d, "edit.txt", "keep\nold\n")
	w.git(d, "add", ".")
	w.git(d, "commit", "-q", "-m", "base")
	w.git(d, "checkout", "-q", "-b", "feature")
	writeRepoFile(w.t, d, "committed.txt", "c1\nc2\n")
	w.git(d, "add", ".")
	w.git(d, "commit", "-q", "-m", "committed")
	writeRepoFile(w.t, d, "edit.txt", "keep\nnew\nmore\n")
	writeRepoFile(w.t, d, "untracked.txt", "u1\n")
	return d
}

func byPath(files []changesFile) map[string]changesFile {
	m := map[string]changesFile{}
	for _, f := range files {
		m[f.Path] = f
	}
	return m
}

// Changes is the checkout against its merge-base with main: the committed file,
// the uncommitted edit and the untracked file, each with its lines in the diff.
func TestShipChanges_CommittedUncommittedAndUntracked(t *testing.T) {
	t.Parallel()
	w := changesWorld(t)
	d := w.featureRepo("alpha")
	s := w.launch(map[string]any{"fleet": "work", "harness": "claude", "dir": d, "skill": noSkill, "order_id": "o-alpha", "text": "go"})

	code, b := w.changes(s.Session)
	if code != http.StatusOK || b.State != "ok" {
		t.Fatalf("changes = %d state %q, want 200 ok", code, b.State)
	}
	files := byPath(b.Files)
	if len(files) != 3 {
		t.Fatalf("files = %+v, want committed.txt, edit.txt and untracked.txt only", b.Files)
	}
	if f := files["committed.txt"]; f.Status != "added" || f.Added != 2 || f.Removed != 0 {
		t.Errorf("committed.txt = %+v, want added +2 -0", f)
	}
	if f := files["edit.txt"]; f.Status != "modified" || f.Added != 2 || f.Removed != 1 {
		t.Errorf("edit.txt = %+v, want modified +2 -1", f)
	}
	if f := files["untracked.txt"]; f.Status != "untracked" || f.Added != 1 || f.Removed != 0 {
		t.Errorf("untracked.txt = %+v, want untracked +1 -0", f)
	}
	for _, want := range []string{"+c1", "+c2", "-old", "+new", "+more", "+u1", "committed.txt", "untracked.txt"} {
		if !strings.Contains(b.Diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, b.Diff)
		}
	}
	if strings.Contains(b.Diff, "base.txt") || b.Truncated {
		t.Errorf("diff mentions an unchanged file or says truncated (%v):\n%s", b.Truncated, b.Diff)
	}
	if code, _ := w.changes("no-such-ship"); code != http.StatusNotFound {
		t.Errorf("unknown ship = %d, want 404", code)
	}
}

// A ship in a directory that is not a checkout gets an empty state, not an error.
func TestShipChanges_NotAGitCheckoutIsEmptyState(t *testing.T) {
	t.Parallel()
	w := changesWorld(t)
	d := w.checkout("plain")
	s := w.launch(map[string]any{"fleet": "work", "harness": "claude", "dir": d, "skill": noSkill, "order_id": "o-plain", "text": "go"})
	if err := os.RemoveAll(filepath.Join(d, ".git")); err != nil {
		t.Fatal(err)
	}
	code, b := w.changes(s.Session)
	if code != http.StatusOK || b.State != "none" || len(b.Files) != 0 || b.Diff != "" {
		t.Errorf("changes = %d %+v, want 200 with state none and nothing in it", code, b)
	}
}

// A diff past the size cap comes back cut, and says so.
func TestShipChanges_OversizedDiffSaysItTruncated(t *testing.T) {
	t.Parallel()
	w := changesWorld(t)
	d := w.featureRepo("big")
	writeRepoFile(t, d, "huge.txt", strings.Repeat("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n", 400000))
	s := w.launch(map[string]any{"fleet": "work", "harness": "claude", "dir": d, "skill": noSkill, "order_id": "o-big", "text": "go"})
	code, b := w.changes(s.Session)
	if code != http.StatusOK || !b.Truncated || len(b.Diff) >= 25_000_000 || len(b.Diff) == 0 {
		t.Errorf("changes = %d truncated=%v with a %d-byte diff, want 200, truncated=true and a diff cut below the 25 MB file", code, b.Truncated, len(b.Diff))
	}
	if _, ok := byPath(b.Files)["huge.txt"]; !ok {
		t.Errorf("files = %+v, want huge.txt still listed", b.Files)
	}
}

// Config and attributes in the checkout are the ship's to write: no program they
// name runs when Changes reads the diff.
func TestShipChanges_NeverRunsAProgramTheCheckoutNames(t *testing.T) {
	t.Parallel()
	w := changesWorld(t)
	d := w.featureRepo("trap")
	marker := filepath.Join(w.root, "MARKER")
	script := filepath.Join(w.root, "trap.sh")
	writeRepoFile(t, w.root, "trap.sh", "#!/bin/sh\necho ran >> "+marker+"\ncat \"$@\" 2>/dev/null\nexit 0\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	w.git(d, "config", "diff.external", script)
	w.git(d, "config", "diff.trap.textconv", script)
	w.git(d, "config", "core.fsmonitor", script)
	w.git(d, "config", "core.pager", script)
	writeRepoFile(t, d, ".gitattributes", "*.txt diff=trap\n")
	s := w.launch(map[string]any{"fleet": "work", "harness": "claude", "dir": d, "skill": noSkill, "order_id": "o-trap", "text": "go"})
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	code, b := w.changes(s.Session)
	if code != http.StatusOK || b.State != "ok" || !strings.Contains(b.Diff, "+c1") || !strings.Contains(b.Diff, "+new") {
		t.Fatalf("changes = %d state %q diff %q, want the diff despite the config", code, b.State, b.Diff)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("a program named by the checkout's config ran: %s exists", marker)
	}
}

// The capability list tells a client the routes exist.
func TestShipChanges_CapabilityListed(t *testing.T) {
	t.Parallel()
	w := changesWorld(t)
	var caps struct {
		Capabilities []string `json:"capabilities"`
	}
	_, body := send(t, http.MethodGet, w.in.LocalURL+"/api/version", nil)
	if json.Unmarshal([]byte(body), &caps) != nil || !strings.Contains(","+strings.Join(caps.Capabilities, ",")+",", ",changes,") {
		t.Errorf("GET /api/version = %s, want the changes capability", body)
	}
}
