package crew

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type diag map[string]any

func d(layer string, file any, code string, field any) diag {
	return diag{"layer": layer, "file": file, "code": code, "field": field}
}

func memberText(name, role, extra, body string) string {
	return fmt.Sprintf("---\nname: %s\nrole: %s\npick-me-when: Pick %s.\n%s---\n\n%s\n", name, role, name, extra, body)
}

func adHocText(name, role, run string) string {
	return memberText(name, role, fmt.Sprintf("ad-hoc: true\nad-hoc-run: %q\n", run), "Ad hoc body.")
}

// layerDir writes files (string or []byte) into a fresh directory named for the layer.
func layerDir(t *testing.T, name string, files map[string]any) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, content := range files {
		put(t, filepath.Join(dir, file), content)
	}
	return dir
}

func put(t *testing.T, path string, content any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	switch c := content.(type) {
	case string:
		raw = []byte(c)
	case []byte:
		raw = c
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func lock(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their mode")
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

func members(t *testing.T, got map[string]any) []string {
	t.Helper()
	var names []string
	items, _ := got["members"].([]any)
	for _, item := range items {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	return names
}

func diags(t *testing.T, got map[string]any, key string) []diag {
	t.Helper()
	out := []diag{}
	items, _ := got[key].([]any)
	for _, item := range items {
		out = append(out, diag(item.(map[string]any)))
	}
	return out
}

func wantDiags(t *testing.T, got map[string]any, key string, want []diag) {
	t.Helper()
	have := diags(t, got, key)
	if !reflect.DeepEqual(normalize(t, have), normalize(t, want)) {
		t.Errorf("%s differ\ngot:\n%s\nwant:\n%s", key, pretty(have), pretty(want))
	}
}

func wantExit(t *testing.T, got map[string]any, want int) {
	t.Helper()
	if exit, ok := got["exit"].(float64); !ok || int(exit) != want {
		t.Errorf("exit = %v, want %d", got["exit"], want)
	}
}

func repoOnly(dir string) Input {
	return Input{Layers: map[string]string{"repo": dir}}
}

func TestNonUTF8MemberIsBadFrontmatter(t *testing.T) {
	dir := layerDir(t, "repo", map[string]any{
		"good.md":         memberText("good", "scribe", "", "Body."),
		"latin1-body.md":  []byte("---\nname: latin1-body\nrole: scribe\npick-me-when: P\n---\n\nbody \xff\xfe\n"),
		"latin1-front.md": []byte("---\nname: latin1-front\nrole: scribe\npick-me-when: caf\xe9\n---\n"),
	})
	got := resolveJSON(t, repoOnly(dir))
	wantExit(t, got, 1)
	wantDiags(t, got, "errors", []diag{
		d("repo", "latin1-body.md", "bad-frontmatter", nil),
		d("repo", "latin1-front.md", "bad-frontmatter", nil),
	})
	wantDiags(t, got, "warnings", []diag{})
}

func TestByteOrderMarkIsBadFrontmatter(t *testing.T) {
	dir := layerDir(t, "repo", map[string]any{
		"bom.md": append([]byte("\xef\xbb\xbf"), memberText("bom", "scribe", "", "Body.")...),
	})
	got := resolveJSON(t, repoOnly(dir))
	wantExit(t, got, 1)
	wantDiags(t, got, "errors", []diag{d("repo", "bom.md", "bad-frontmatter", nil)})
}

func TestCRLFResolvesLikeItsLFTwin(t *testing.T) {
	text := memberText("twin", "scribe", "skills: [a, b]\n", "line one\n\nline two")
	crlf := layerDir(t, "crlf", map[string]any{"twin.md": strings.ReplaceAll(text, "\n", "\r\n")})
	lf := layerDir(t, "lf", map[string]any{"twin.md": text})
	gotCRLF := resolveJSON(t, repoOnly(crlf))
	gotLF := resolveJSON(t, repoOnly(lf))
	if !reflect.DeepEqual(normalize(t, gotCRLF), normalize(t, gotLF)) {
		t.Errorf("CRLF and LF differ\nCRLF:\n%s\nLF:\n%s", pretty(gotCRLF), pretty(gotLF))
	}
	wantExit(t, gotLF, 0)
	ms, _ := gotLF["members"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["instructions"] != "line one\n\nline two" {
		t.Errorf("members = %s", pretty(gotLF["members"]))
	}
}

func TestOnlyLowercaseMDFilesAreMembers(t *testing.T) {
	dir := layerDir(t, "repo", map[string]any{
		"UPPER.MD": "garbage\n",
		"Mixed.Md": "garbage\n",
		"keep.md":  memberText("keep", "scribe", "", "Body."),
	})
	got := resolveJSON(t, repoOnly(dir))
	wantExit(t, got, 0)
	if !reflect.DeepEqual(members(t, got), []string{"keep"}) {
		t.Errorf("members = %v", members(t, got))
	}
}

func TestNonUTF8FileNameIsSkippedSilently(t *testing.T) {
	dir := layerDir(t, "repo", map[string]any{"keep.md": memberText("keep", "scribe", "", "Body.")})
	if err := os.WriteFile(filepath.Join(dir, "bad\xff.md"), []byte("garbage\n"), 0o644); err != nil {
		t.Skipf("file system refuses non-UTF-8 names: %v", err)
	}
	got := resolveJSON(t, repoOnly(dir))
	wantExit(t, got, 0)
	if !reflect.DeepEqual(members(t, got), []string{"keep"}) {
		t.Errorf("members = %v", members(t, got))
	}
	wantDiags(t, got, "warnings", []diag{})
}

func TestSymlinkedLayerDirectoryIsAllowed(t *testing.T) {
	real := layerDir(t, "real", map[string]any{
		"x.md":       memberText("x", "scribe", "", "Body."),
		"store/y.md": memberText("y", "scribe", "", "Body."),
	})
	if err := os.Symlink("store/y.md", filepath.Join(real, "y.md")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got := resolveJSON(t, repoOnly(link))
	wantExit(t, got, 0)
	if !reflect.DeepEqual(members(t, got), []string{"x", "y"}) {
		t.Errorf("members = %v", members(t, got))
	}
	wantDiags(t, got, "warnings", []diag{})
}

func TestUnreadableFileAndUnlistableLayerAreErrors(t *testing.T) {
	repo := layerDir(t, "repo", map[string]any{
		"locked.md": memberText("locked", "scribe", "", "Body."),
		"fine.md":   memberText("fine", "scribe", "", "Body."),
	})
	lock(t, filepath.Join(repo, "locked.md"))
	hidden := layerDir(t, "user-global", map[string]any{"x.md": memberText("x", "scribe", "", "Body.")})
	lock(t, hidden)
	got := resolveJSON(t, Input{Layers: map[string]string{"user-global": hidden, "repo": repo}})
	wantExit(t, got, 1)
	wantDiags(t, got, "errors", []diag{
		d("user-global", nil, "unreadable", nil),
		d("repo", "locked.md", "unreadable", nil),
	})
	wantDiags(t, got, "warnings", []diag{})
}

func TestUnreadableAdHocDirectoryIsAWarning(t *testing.T) {
	repo := layerDir(t, "repo", map[string]any{"base.md": memberText("base", "scribe", "", "Body.")})
	adHoc := layerDir(t, "ad-hoc", map[string]any{"mine.md": adHocText("mine", "scribe", "run-1")})
	lock(t, adHoc)
	got := resolveJSON(t, Input{Layers: map[string]string{"repo": repo, "ad-hoc": adHoc}, Run: "run-1"})
	wantExit(t, got, 0)
	wantDiags(t, got, "warnings", []diag{d("ad-hoc", nil, "ad-hoc-unreadable", nil)})
	if !reflect.DeepEqual(members(t, got), []string{"base"}) {
		t.Errorf("members = %v", members(t, got))
	}
}

func TestAdHocDirectoryUnderCommodoreIsRefused(t *testing.T) {
	repo := layerDir(t, "repo", map[string]any{"base.md": memberText("base", "scribe", "", "Body.")})
	adHoc := filepath.Join(t.TempDir(), ".commodore", "ad-hoc")
	put(t, filepath.Join(adHoc, "mine.md"), adHocText("mine", "scribe", "run-1"))
	got := resolveJSON(t, Input{Layers: map[string]string{"repo": repo, "ad-hoc": adHoc}, Run: "run-1"})
	wantExit(t, got, 0)
	wantDiags(t, got, "warnings", []diag{d("ad-hoc", nil, "ad-hoc-dir-refused", nil)})
	if !reflect.DeepEqual(members(t, got), []string{"base"}) {
		t.Errorf("members = %v", members(t, got))
	}
}

func TestAdHocDirectoryReachedThroughACommodoreSymlinkIsRefused(t *testing.T) {
	repo := layerDir(t, "repo", map[string]any{"base.md": memberText("base", "scribe", "", "Body.")})
	root := t.TempDir()
	put(t, filepath.Join(root, ".commodore", "real", "mine.md"), adHocText("mine", "scribe", "run-1"))
	link := filepath.Join(root, "ad-hoc")
	if err := os.Symlink(filepath.Join(root, ".commodore", "real"), link); err != nil {
		t.Fatal(err)
	}
	got := resolveJSON(t, Input{Layers: map[string]string{"repo": repo, "ad-hoc": link}, Run: "run-1"})
	wantExit(t, got, 0)
	wantDiags(t, got, "warnings", []diag{d("ad-hoc", nil, "ad-hoc-dir-refused", nil)})
}

// notesWorld builds a layer with one implementer and a memory root with fleet and repo notes.
func notesWorld(t *testing.T) (in Input, memory string) {
	t.Helper()
	repo := layerDir(t, "repo", map[string]any{"implementer.md": memberText("implementer", "implementer", "", "Body.")})
	memory = layerDir(t, "memory", map[string]any{
		"implementer/fleet.md":          "- 2026-11-01 (run-a): Run `make test`.\n",
		"implementer/repos/acme/app.md": "- 2026-11-02 (run-a): Needs Node 22.\n",
	})
	return Input{Layers: map[string]string{"repo": repo, "memory": memory}, Repo: "acme/app"}, memory
}

func implementerNotes(t *testing.T, got map[string]any) map[string]any {
	t.Helper()
	ms, _ := got["members"].([]any)
	if len(ms) != 1 {
		t.Fatalf("members = %s", pretty(got["members"]))
	}
	return ms[0].(map[string]any)["notes"].(map[string]any)
}

var (
	fleetNote = "2026-11-01 (run-a): Run `make test`."
	repoNote  = "2026-11-02 (run-a): Needs Node 22."
)

func TestNoteFileThatIsNotTextIsUnreadable(t *testing.T) {
	for label, raw := range map[string][]byte{
		"not UTF-8":  []byte("- 2026-11-01 (r): caf\xe9\n"),
		"a NUL byte": []byte("- 2026-11-01 (r): a\x00\n"),
	} {
		t.Run(label, func(t *testing.T) {
			in, memory := notesWorld(t)
			put(t, filepath.Join(memory, "implementer", "fleet.md"), raw)
			got := resolveJSON(t, in)
			wantExit(t, got, 0)
			wantDiags(t, got, "warnings", []diag{d("memory", "implementer/fleet.md", "memory-unreadable", nil)})
			want := map[string]any{"fleet": []any{}, "repo": []any{repoNote}}
			if !reflect.DeepEqual(normalize(t, implementerNotes(t, got)), normalize(t, want)) {
				t.Errorf("notes = %s", pretty(implementerNotes(t, got)))
			}
		})
	}
}

func TestNoteFileOver65536BytesIsUnreadable(t *testing.T) {
	in, memory := notesWorld(t)
	head := "- 2026-11-01 (run-a): "
	exact := head + strings.Repeat("x", 65536-len(head)-1) + "\n"
	put(t, filepath.Join(memory, "implementer", "fleet.md"), exact)
	got := resolveJSON(t, in)
	wantDiags(t, got, "warnings", []diag{})
	notes := implementerNotes(t, got)
	if fleet, _ := notes["fleet"].([]any); len(fleet) != 1 || fleet[0] != exact[2:len(exact)-1] {
		t.Errorf("a note file of exactly 65536 bytes should load, fleet = %v", notes["fleet"])
	}
	put(t, filepath.Join(memory, "implementer", "fleet.md"), exact+"\n")
	got = resolveJSON(t, in)
	wantDiags(t, got, "warnings", []diag{d("memory", "implementer/fleet.md", "memory-unreadable", nil)})
}

func TestNoteFileWithNoReadPermissionIsUnreadable(t *testing.T) {
	in, memory := notesWorld(t)
	lock(t, filepath.Join(memory, "implementer", "repos", "acme", "app.md"))
	got := resolveJSON(t, in)
	wantDiags(t, got, "warnings", []diag{d("memory", "implementer/repos/acme/app.md", "memory-unreadable", nil)})
	want := map[string]any{"fleet": []any{fleetNote}, "repo": []any{}}
	if !reflect.DeepEqual(normalize(t, implementerNotes(t, got)), normalize(t, want)) {
		t.Errorf("notes = %s", pretty(implementerNotes(t, got)))
	}
}

func TestMemoryRootThatIsAFileIsUnreadable(t *testing.T) {
	in, _ := notesWorld(t)
	file := filepath.Join(t.TempDir(), "memory")
	put(t, file, "not a directory\n")
	in.Layers["memory"] = file
	got := resolveJSON(t, in)
	wantDiags(t, got, "warnings", []diag{d("memory", nil, "memory-unreadable", nil)})
}

func TestSymlinkedMemoryRootIsRefused(t *testing.T) {
	in, memory := notesWorld(t)
	link := filepath.Join(t.TempDir(), "memory")
	if err := os.Symlink(memory, link); err != nil {
		t.Fatal(err)
	}
	in.Layers["memory"] = link
	got := resolveJSON(t, in)
	wantDiags(t, got, "warnings", []diag{d("memory", nil, "memory-refused", nil)})
	want := map[string]any{"fleet": []any{}, "repo": []any{}}
	if !reflect.DeepEqual(normalize(t, implementerNotes(t, got)), normalize(t, want)) {
		t.Errorf("notes = %s", pretty(implementerNotes(t, got)))
	}
}

func TestSymlinkedMemberMemoryDirectoryIsRefused(t *testing.T) {
	in, memory := notesWorld(t)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Rename(filepath.Join(memory, "implementer"), elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(memory, "implementer")); err != nil {
		t.Fatal(err)
	}
	got := resolveJSON(t, in)
	wantDiags(t, got, "warnings", []diag{d("memory", "implementer", "memory-refused", nil)})
	want := map[string]any{"fleet": []any{}, "repo": []any{}}
	if !reflect.DeepEqual(normalize(t, implementerNotes(t, got)), normalize(t, want)) {
		t.Errorf("notes = %s", pretty(implementerNotes(t, got)))
	}
}

func TestUnreadableMemoryRootAndMemberDirectoryAreWarnings(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		in, memory := notesWorld(t)
		lock(t, memory)
		got := resolveJSON(t, in)
		wantExit(t, got, 0)
		// Pinned from crew.py: a root with no search permission fails at the member directory, not the root.
		wantDiags(t, got, "warnings", []diag{d("memory", "implementer", "memory-unreadable", nil)})
	})
	t.Run("member directory", func(t *testing.T) {
		in, memory := notesWorld(t)
		lock(t, filepath.Join(memory, "implementer"))
		got := resolveJSON(t, in)
		wantExit(t, got, 0)
		wantDiags(t, got, "warnings", []diag{
			d("memory", "implementer/fleet.md", "memory-unreadable", nil),
			d("memory", "implementer/repos", "memory-unreadable", nil),
		})
	})
}
