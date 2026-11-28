package crewapi_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/crewapi"
)

const commit40 = "0123456789abcdef0123456789abcdef01234567"

func hexSum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// sidecarFor is a readable sidecar for the member bytes, with an upstream_name and an unknown field the app must keep.
func sidecarFor(memberBytes string) string {
	b, _ := json.Marshal(map[string]any{
		"version": 1, "host": "github.com", "repo": "octocat/admiral-crew", "path": "crew/bosun.md",
		"upstream_name": "bosun", "commit": commit40, "tag": nil, "upstream_blob": strings.Repeat("a", 40),
		"content_sha256": hexSum(memberBytes), "imported_at": "2026-11-20T10:00:00Z", "updated_at": "2026-11-20T10:00:00Z",
		"from_elsewhere": "keep me",
	})
	return string(b)
}

func (f *fixture) sidecarPath(name string) string {
	return filepath.Join(f.crewDir(), name+".shared.json")
}

// shared puts a fleet member and an attached sidecar beside it, and returns the member's bytes.
func (f *fixture) shared(name string) string {
	f.t.Helper()
	f.fleetMember(name, "triager", "Shared "+name+".", "")
	text := readFile(f.t, filepath.Join(f.crewDir(), name+".md"))
	writeFile(f.t, f.sidecarPath(name), sidecarFor(text))
	return text
}

// rawMember returns the member's JSON view as a map, so a missing key shows as missing.
func (f *fixture) rawMember(name string) map[string]any {
	f.t.Helper()
	status, body := f.do("GET", "/api/fleets/work/crew", nil)
	if status != 200 {
		f.t.Fatalf("GET crew: %d %s", status, body)
	}
	var got struct {
		Members []map[string]any `json:"members"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		f.t.Fatal(err)
	}
	for _, m := range got.Members {
		if m["name"] == name {
			return m
		}
	}
	f.t.Fatalf("no member %s in %s", name, body)
	return nil
}

func (f *fixture) wantShared(name string, attached, detached bool) {
	f.t.Helper()
	m := f.rawMember(name)
	v, present := m["shared"]
	if !present {
		f.t.Errorf("%s has no shared key", name)
		return
	}
	if !attached {
		if v != nil {
			f.t.Errorf("%s shared = %v, want null", name, v)
		}
		return
	}
	s, _ := v.(map[string]any)
	if s == nil || s["owner"] != "octocat" || s["repo"] != "admiral-crew" || s["commit"] != commit40 || s["detached"] != detached {
		f.t.Errorf("%s shared = %v, want octocat/admiral-crew detached=%v", name, v, detached)
	}
}

func (f *fixture) makeMine(name string) (int, string) {
	f.t.Helper()
	return f.do("POST", "/api/fleets/work/crew/members/"+name+"/make-mine", nil)
}

func TestMemberViewWithNoSidecarHasNullSharedAndReadable(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	f.wantShared("bosun", false, false)
	if v, ok := f.rawMember("bosun")["sidecar_unreadable"]; !ok || v != false {
		t.Errorf("sidecar_unreadable = %v (present %v), want false", v, ok)
	}
}

func TestMemberViewShowsAnAttachedSidecar(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.shared("bosun")
	f.wantShared("bosun", true, false)
	if v := f.rawMember("bosun")["sidecar_unreadable"]; v != false {
		t.Errorf("sidecar_unreadable = %v, want false", v)
	}
}

func TestMemberViewShowsADetachedSidecarAfterALocalEditAndAttachedAgainWhenUndone(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	text := f.shared("bosun")
	writeFile(t, filepath.Join(f.crewDir(), "bosun.md"), strings.Replace(text, "Do the work.", "Do other work.", 1))
	f.wantShared("bosun", true, true)
	writeFile(t, filepath.Join(f.crewDir(), "bosun.md"), text)
	f.wantShared("bosun", true, false)
}

func TestMemberViewMarksAnUnreadableSidecar(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	writeFile(t, f.sidecarPath("bosun"), `{"version":2}`)
	f.wantShared("bosun", false, false)
	if v := f.rawMember("bosun")["sidecar_unreadable"]; v != true {
		t.Errorf("sidecar_unreadable = %v, want true", v)
	}
}

func TestAnOrphanSidecarIsNotAMember(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	writeFile(t, f.sidecarPath("ghost"), sidecarFor("x"))
	if _, ok := f.listed()["ghost"]; ok {
		t.Error("an orphan sidecar listed a member")
	}
}

func TestMakeMineDeletesTheSidecarAndKeepsTheMemberFile(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	text := f.shared("bosun")
	if status, out := f.makeMine("bosun"); status != 200 && status != 204 {
		t.Fatalf("make-mine: %d %s", status, out)
	}
	if exists(f.sidecarPath("bosun")) {
		t.Error("the sidecar is still there")
	}
	if got := readFile(t, filepath.Join(f.crewDir(), "bosun.md")); got != text {
		t.Errorf("bosun.md changed:\n%s", got)
	}
	f.wantShared("bosun", false, false)
}

func TestMakeMineOnADetachedAndAnUnreadableSidecarWorks(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	writeFile(t, f.sidecarPath("bosun"), sidecarFor("other bytes"))
	f.fleetMember("cook", "scribe", "Fleet cook.", "")
	writeFile(t, f.sidecarPath("cook"), "not json")
	for _, name := range []string{"bosun", "cook"} {
		if status, out := f.makeMine(name); status != 200 && status != 204 {
			t.Errorf("make-mine %s: %d %s", name, status, out)
		}
		if exists(f.sidecarPath(name)) {
			t.Errorf("%s's sidecar is still there", name)
		}
	}
}

func TestMakeMineRemovesADanglingLinkAndLeavesItsTarget(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	outside := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.Symlink(outside, f.sidecarPath("bosun")); err != nil {
		t.Fatal(err)
	}
	if status, out := f.makeMine("bosun"); status != 200 && status != 204 {
		t.Fatalf("make-mine: %d %s", status, out)
	}
	if exists(f.sidecarPath("bosun")) {
		t.Error("the dangling link is still there")
	}

	// A link to a real file goes, and the file it pointed at stays.
	target := filepath.Join(t.TempDir(), "real.json")
	writeFile(t, target, "precious")
	f.fleetMember("cook", "scribe", "Fleet cook.", "")
	if err := os.Symlink(target, f.sidecarPath("cook")); err != nil {
		t.Fatal(err)
	}
	if status, out := f.makeMine("cook"); status != 200 && status != 204 {
		t.Fatalf("make-mine: %d %s", status, out)
	}
	if exists(f.sidecarPath("cook")) || readFile(t, target) != "precious" {
		t.Error("the link stayed, or its target changed")
	}
}

func TestMakeMineWithNoSidecarOrNoMemberIs404(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	if status, out := f.makeMine("bosun"); status != 404 {
		t.Errorf("no sidecar: %d %s, want 404", status, out)
	}
	writeFile(t, f.sidecarPath("ghost"), sidecarFor("x"))
	if status, out := f.makeMine("ghost"); status != 404 {
		t.Errorf("no member: %d %s, want 404", status, out)
	}
	if !exists(f.sidecarPath("ghost")) {
		t.Error("make-mine removed an orphan's sidecar")
	}
}

func TestMakeMineOfADirectoryAtTheSidecarName(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	if err := os.Mkdir(f.sidecarPath("bosun"), 0o755); err != nil {
		t.Fatal(err)
	}
	if status, out := f.makeMine("bosun"); status != 200 && status != 204 {
		t.Errorf("empty directory: %d %s", status, out)
	}
	if exists(f.sidecarPath("bosun")) {
		t.Error("the empty directory is still there")
	}

	f.fleetMember("cook", "scribe", "Fleet cook.", "")
	writeFile(t, filepath.Join(f.sidecarPath("cook"), "keep"), "x")
	if status, out := f.makeMine("cook"); status != 409 {
		t.Errorf("non-empty directory: %d %s, want 409", status, out)
	}
	if !exists(filepath.Join(f.sidecarPath("cook"), "keep")) {
		t.Error("make-mine removed a non-empty directory")
	}
}

func TestMakeMineCallsNoGhAndWorksWithSharingOff(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "gh.log")
	writeFile(t, filepath.Join(bin, "gh"), "#!/bin/sh\necho called >> "+log+"\nexit 1\n")
	if err := os.Chmod(filepath.Join(bin, "gh"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.shared("bosun")
	if status, out := f.makeMine("bosun"); status != 200 && status != 204 {
		t.Fatalf("make-mine: %d %s", status, out)
	}
	if exists(log) {
		t.Error("make-mine ran gh")
	}
	if exists(f.sidecarPath("bosun")) {
		t.Error("the sidecar is still there")
	}
}

func TestRenameMovesTheSidecarAndKeepsAnAttachedMemberAttached(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.shared("bosun")
	if status, out := rename(f, "bosun", map[string]any{"to": "mate-two"}); status != 200 {
		t.Fatalf("rename: %d %s", status, out)
	}
	if exists(f.sidecarPath("bosun")) || !exists(f.sidecarPath("mate-two")) {
		t.Fatal("the sidecar did not move with the member")
	}
	f.wantShared("mate-two", true, false)
	var got map[string]any
	if err := json.Unmarshal([]byte(readFile(t, f.sidecarPath("mate-two"))), &got); err != nil {
		t.Fatal(err)
	}
	if got["upstream_name"] != "bosun" || got["from_elsewhere"] != "keep me" {
		t.Errorf("sidecar = %v, want upstream_name bosun and the unknown field kept", got)
	}
	if want := hexSum(readFile(t, filepath.Join(f.crewDir(), "mate-two.md"))); got["content_sha256"] != want {
		t.Errorf("content_sha256 = %v, want the renamed file's hash %s", got["content_sha256"], want)
	}
}

func TestRenameMovesADetachedOrUnreadableSidecarByteForByte(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	for name, sidecar := range map[string]string{"bosun": sidecarFor("other bytes"), "cook": "not json {"} {
		f.fleetMember(name, "scribe", "Fleet "+name+".", "")
		writeFile(t, f.sidecarPath(name), sidecar)
		if status, out := rename(f, name, map[string]any{"to": name + "-two"}); status != 200 {
			t.Fatalf("rename %s: %d %s", name, status, out)
		}
		if got := slurp(f.sidecarPath(name + "-two")); got != sidecar || exists(f.sidecarPath(name)) {
			t.Errorf("%s's sidecar = %q, want it moved unchanged", name, got)
		}
	}
}

func TestRenameKeepsAKeyThatDiffersOnlyInCaseAndLeavesTheMemberAttached(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	fixture := filepath.Join("..", "crew", "testdata", "crew-resolution", "cases", "shared-key-case", "layers", "fleet")
	writeFile(t, filepath.Join(f.crewDir(), "reviewer.md"), readFile(t, filepath.Join(fixture, "reviewer.md")))
	writeFile(t, f.sidecarPath("reviewer"), readFile(t, filepath.Join(fixture, "reviewer.shared.json")))
	if status, out := rename(f, "reviewer", map[string]any{"to": "mate-two"}); status != 200 {
		t.Fatalf("rename: %d %s", status, out)
	}
	got := readSidecar(t, f.crewDir(), "mate-two")
	if got["version"] != float64(1) || got["tag"] != "v1" || got["Version"] != float64(2) || got["TAG"] != "v9" {
		t.Errorf("sidecar = %v, want version 1, tag v1 and the upper-case keys as they were", got)
	}
	m := f.rawMember("mate-two")
	s, _ := m["shared"].(map[string]any)
	if m["sidecar_unreadable"] != false || s == nil || s["tag"] != "v1" || s["detached"] != false {
		t.Errorf("mate-two = %v, want attached with tag v1", m)
	}
}

func TestRenameKeepsASymlinkedAttachedSidecarAttached(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	text := f.shared("bosun")
	real := filepath.Join(f.crewDir(), "store", "bosun.json")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, real, sidecarFor(text))
	if err := os.Remove(f.sidecarPath("bosun")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, f.sidecarPath("bosun")); err != nil {
		t.Fatal(err)
	}
	f.wantShared("bosun", true, false)
	if status, out := rename(f, "bosun", map[string]any{"to": "mate-two"}); status != 200 {
		t.Fatalf("rename: %d %s", status, out)
	}
	f.wantShared("mate-two", true, false)
	if exists(f.sidecarPath("bosun")) {
		t.Error("the old link is still there")
	}
}

func TestRenameMovesADirectoryALinkAndAnOversizedSidecarAsTheEntriesTheyAre(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("dir", "scribe", "Fleet dir.", "")
	writeFile(t, filepath.Join(f.sidecarPath("dir"), "keep"), "x")
	f.fleetMember("link", "scribe", "Fleet link.", "")
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.Symlink(target, f.sidecarPath("link")); err != nil {
		t.Fatal(err)
	}
	f.fleetMember("big", "scribe", "Fleet big.", "")
	big := strings.Repeat("x", 70000)
	writeFile(t, f.sidecarPath("big"), big)
	for _, name := range []string{"dir", "link", "big"} {
		if status, out := rename(f, name, map[string]any{"to": name + "-two"}); status != 200 {
			t.Fatalf("rename %s: %d %s", name, status, out)
		}
		if _, err := os.Lstat(f.sidecarPath(name)); err == nil {
			t.Errorf("%s's sidecar is still at the old name", name)
		}
	}
	if slurp(filepath.Join(f.sidecarPath("dir-two"), "keep")) != "x" {
		t.Error("the directory sidecar didn't move whole")
	}
	if dest, err := os.Readlink(f.sidecarPath("link-two")); err != nil || dest != target {
		t.Errorf("link-two = %q, %v, want a link to %s", dest, err, target)
	}
	if slurp(f.sidecarPath("big-two")) != big {
		t.Error("the oversized sidecar changed")
	}
}

func TestRenameRollsBackTheSidecarAndTheNotesWhenTheOldMemberCantBeRemoved(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.shared("bosun")
	notes := f.fleetNotes("bosun", "- "+note(1))
	// A non-empty directory at the aside name makes removing the old member fail after the new one is in place.
	writeFile(t, filepath.Join(f.crewDir(), ".bosun.shared.json.removing", "keep"), "x")
	if status, out := rename(f, "bosun", map[string]any{"to": "mate-two", "carry_notes": true}); status == 200 {
		t.Fatalf("rename: %d %s, want it to fail", status, out)
	}
	if !exists(filepath.Join(f.crewDir(), "bosun.md")) || !exists(f.sidecarPath("bosun")) {
		t.Error("the old member or its sidecar is gone")
	}
	if exists(filepath.Join(f.crewDir(), "mate-two.md")) || exists(f.sidecarPath("mate-two")) {
		t.Error("the new member or its sidecar was left behind")
	}
	if readFile(t, notes) != "- "+note(1)+"\n" {
		t.Error("the notes didn't come back to bosun")
	}
}

func TestRenameOntoAnOrphanSidecarIs409AndMovesNothing(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	text := f.shared("bosun")
	writeFile(t, f.sidecarPath("mate-two"), "orphan")
	status, out := rename(f, "bosun", map[string]any{"to": "mate-two"})
	if status != 409 || !strings.Contains(out, "mate-two.shared.json") {
		t.Errorf("rename: %d %s, want 409 naming mate-two.shared.json", status, out)
	}
	if slurp(filepath.Join(f.crewDir(), "bosun.md")) != text || !exists(f.sidecarPath("bosun")) || exists(filepath.Join(f.crewDir(), "mate-two.md")) || slurp(f.sidecarPath("mate-two")) != "orphan" {
		t.Error("a file moved or changed")
	}
}

func TestDeleteRemovesTheSidecarToo(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.shared("bosun")
	if status, out := f.do("DELETE", "/api/fleets/work/crew/members/bosun", nil); status != 200 && status != 204 {
		t.Fatalf("delete: %d %s", status, out)
	}
	if exists(filepath.Join(f.crewDir(), "bosun.md")) || exists(f.sidecarPath("bosun")) {
		t.Error("the member or its sidecar is still there")
	}
}

func TestCreateAtAnOrphanSidecarsNameIs409AndWritesNothing(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	writeFile(t, f.sidecarPath("bosun"), "orphan")
	status, out := f.create(newMember("bosun"))
	if status != 409 {
		t.Errorf("create: %d %s, want 409", status, out)
	}
	if exists(filepath.Join(f.crewDir(), "bosun.md")) || slurp(f.sidecarPath("bosun")) != "orphan" {
		t.Error("create wrote a member or touched the orphan")
	}
}

func readSidecar(t *testing.T, dir, name string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, name+".shared.json"))), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWriteSidecarWritesTheSchemaPrivatelyAndLeavesNoTempFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tag := "v1.2"
	s := crewapi.Sidecar{Version: 1, Host: "github.com", Repo: "octocat/admiral-crew", Path: "crew/bosun.md", UpstreamName: "bosun", Commit: commit40, Tag: &tag, UpstreamBlob: strings.Repeat("b", 40), ContentSHA256: hexSum("x"), ImportedAt: "2026-11-20T10:00:00Z", UpdatedAt: "2026-11-20T10:00:00Z"}
	if err := crewapi.WriteSidecar(dir, "bosun", s); err != nil {
		t.Fatalf("WriteSidecar: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "bosun.shared.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	got := readSidecar(t, dir, "bosun")
	if got["version"] != float64(1) || got["repo"] != "octocat/admiral-crew" || got["tag"] != "v1.2" || got["commit"] != commit40 || got["upstream_name"] != "bosun" || got["content_sha256"] != hexSum("x") {
		t.Errorf("sidecar = %v", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only the sidecar (no temp file)", len(entries))
	}
}

func TestWriteSidecarKeepsTheUnknownFieldsOfOneAlreadyThere(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bosun.shared.json"), sidecarFor("old"))
	s := crewapi.Sidecar{Version: 1, Host: "github.com", Repo: "octocat/admiral-crew", Path: "crew/bosun.md", UpstreamName: "bosun", Commit: commit40, UpstreamBlob: strings.Repeat("b", 40), ContentSHA256: hexSum("new"), ImportedAt: "2026-11-20T10:00:00Z", UpdatedAt: "2026-11-21T10:00:00Z"}
	if err := crewapi.WriteSidecar(dir, "bosun", s); err != nil {
		t.Fatalf("WriteSidecar: %v", err)
	}
	got := readSidecar(t, dir, "bosun")
	if got["from_elsewhere"] != "keep me" || got["content_sha256"] != hexSum("new") {
		t.Errorf("sidecar = %v, want the unknown field kept and the hash replaced", got)
	}
}

func TestWriteSidecarRefusesAFileThatIsNotAJSONObjectAndLeavesItAlone(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{"an array": "[1]", "null": "null", "garbage": "not json"} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "bosun.shared.json"), content)
		s := crewapi.Sidecar{Version: 1, Host: "github.com", Repo: "octocat/admiral-crew", Path: "crew/bosun.md", UpstreamName: "bosun", Commit: commit40, UpstreamBlob: strings.Repeat("b", 40), ContentSHA256: hexSum("new")}
		if err := crewapi.WriteSidecar(dir, "bosun", s); err == nil {
			t.Errorf("%s: WriteSidecar replaced the file, want an error", name)
		}
		if raw, _ := os.ReadFile(filepath.Join(dir, "bosun.shared.json")); string(raw) != content {
			t.Errorf("%s: the file is now %q, want it untouched", name, raw)
		}
	}
}

func TestSidecarPresentSeesAnythingAtTheName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if got, err := crewapi.SidecarPresent(dir, "bosun"); err != nil || got {
		t.Errorf("empty dir: %v, %v, want false", got, err)
	}
	writeFile(t, filepath.Join(dir, "a.shared.json"), "{}")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "b.shared.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "c.shared.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if got, err := crewapi.SidecarPresent(dir, name); err != nil || !got {
			t.Errorf("%s: %v, %v, want true", name, got, err)
		}
	}
}

func resolvedMember(t *testing.T, file string) crew.Member {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bosun.md"), file)
	res := crew.Resolve(crew.Input{Admiral: true, Layers: map[string]string{crew.LayerFleet: dir}})
	if len(res.Members) != 1 {
		t.Fatalf("resolved %+v", res)
	}
	return res.Members[0]
}

func TestCanonicalBytesDropUnknownFieldsAndAdHocMarks(t *testing.T) {
	t.Parallel()
	m := resolvedMember(t, "---\nname: bosun\nrole: triager\npick-me-when: Fleet bosun.\nx-extra: secret-ish\n---\n\nDo the work.\n")
	run := "run-1"
	m.AdHoc, m.AdHocRun = true, &run
	got := string(crewapi.CanonicalMemberBytes(m))
	if !strings.HasPrefix(got, "---\nname: bosun\nrole: \"triager\"\n") || !strings.Contains(got, "Do the work.") {
		t.Fatalf("canonical bytes = %q, want the member's name, role and body", got)
	}
	for _, banned := range []string{"x-extra", "ad-hoc"} {
		if strings.Contains(got, banned) {
			t.Errorf("canonical bytes carry %q:\n%s", banned, got)
		}
	}
}

func TestCheckPublishAcceptsACleanFileWithNewlinesAndTabs(t *testing.T) {
	t.Parallel()
	if r := crewapi.CheckPublish([]byte("---\nname: bosun\n---\n\n\tIndented line.\n"), crewapi.PublishPaths{AdmiralHome: "/Users/ann/.admiral", Home: "/Users/ann"}); r != nil {
		t.Errorf("refused a clean file: %+v", r)
	}
}

func TestCheckPublishRefusesEachHiddenRuneAndNamesTheCodePointAndLine(t *testing.T) {
	t.Parallel()
	for r, code := range map[rune]string{
		0x202E: "U+202E", 0x200B: "U+200B", 0xFEFF: "U+FEFF", 0x0007: "U+0007", 0xE0041: "U+E0041", 0xFE0F: "U+FE0F",
		0xE0100: "U+E0100", 0x115F: "U+115F", 0x1160: "U+1160", 0x3164: "U+3164", 0xFFA0: "U+FFA0", 0x2800: "U+2800",
		0x2028: "U+2028", 0x2029: "U+2029", 0xE000: "U+E000", 0x0378: "U+0378",
		0x034F: "U+034F", 0x180B: "U+180B", 0x17B4: "U+17B4",
	} {
		file := "1\n2\n3\n4\n5\n6\nseven " + string(r) + " here\n"
		got := crewapi.CheckPublish([]byte(file), crewapi.PublishPaths{})
		if got == nil || got.Check != crewapi.CheckHidden || got.Line != 7 || !strings.Contains(got.Message, code+" on line 7") {
			t.Errorf("%s: got %+v, want a hidden refusal reading %q", code, got, code+" on line 7")
		}
	}
}

func TestCheckPublishRefusesEveryFormOfAMacPath(t *testing.T) {
	t.Parallel()
	p := crewapi.PublishPaths{AdmiralHome: "/Users/ann/admiral-home", Home: "/Users/ann", CloneRoot: "/Volumes/code/clones", SearchRoots: []string{"/Volumes/work/repos"}}
	for _, line := range []string{
		"see /Users/ann/admiral-home/fleets/work",
		"see /Users/ann/notes",
		"clone at /Volumes/code/clones/api",
		"search /Volumes/work/repos/api",
		"see ~/notes/api",
		"see ~/",
		"open /Users/ann",
	} {
		got := crewapi.CheckPublish([]byte("one\n"+line+"\n"), p)
		if got == nil || got.Check != crewapi.CheckPaths || got.Line != 2 {
			t.Errorf("%q: got %+v, want a path refusal on line 2", line, got)
		}
	}
}

func TestCheckPublishRefusesTheHomeBaseNameSegment(t *testing.T) {
	t.Parallel()
	got := crewapi.CheckPublish([]byte("work from /Volumes/backup/ann/project\n"), crewapi.PublishPaths{Home: "/Users/ann"})
	if got == nil || got.Check != crewapi.CheckPaths {
		t.Errorf("got %+v, want a path refusal on the home's base name", got)
	}
}

func TestCheckPublishRefusesEachWayToSpellAHomePath(t *testing.T) {
	t.Parallel()
	p := crewapi.PublishPaths{AdmiralHome: "/Users/ann/admiral-home", Home: "/Users/ann/", CloneRoot: "/Users/ann//clones/"}
	for name, line := range map[string]string{
		"$HOME":            "see $HOME/github_repos/private-repo",
		"${HOME}":          "see ${HOME}/github_repos/private-repo",
		"after =":          "dir=/opt/ann/x",
		"after :":          "PATH=/usr/bin:/srv/ann/bin",
		"base name case":   "see /users/ANN/x",
		"unclean root":     "clone at /Users/ann/clones/api",
		"~ with a slashed": "see ~/admiral-home/fleets",
	} {
		got := crewapi.CheckPublish([]byte("one\n"+line+"\n"), p)
		if got == nil || got.Check != crewapi.CheckPaths || got.Line != 2 {
			t.Errorf("%s: %q: got %+v, want a path refusal on line 2", name, line, got)
		}
	}
}

func TestCheckPublishLeavesAnUnrelatedPathAlone(t *testing.T) {
	t.Parallel()
	if got := crewapi.CheckPublish([]byte("use /usr/local/bin and ./relative/path and https://example.com/a\n"), crewapi.PublishPaths{AdmiralHome: "/Users/ann/admiral-home", Home: "/Users/ann"}); got != nil {
		t.Errorf("refused: %+v", got)
	}
}

func TestCheckPublishRefusesGitleaksAllowAndNamesTheLine(t *testing.T) {
	t.Parallel()
	got := crewapi.CheckPublish([]byte("a\nb\nkey = value # gitleaks:allow\n"), crewapi.PublishPaths{})
	if got == nil || got.Check != crewapi.CheckGitleaksAllow || got.Line != 3 {
		t.Errorf("got %+v, want a gitleaks:allow refusal on line 3", got)
	}
}

func TestCheckPublishReportsPathsBeforeHiddenBeforeGitleaksAllow(t *testing.T) {
	t.Parallel()
	p := crewapi.PublishPaths{Home: "/Users/ann"}
	all := "/Users/ann/x \u202e gitleaks:allow\n"
	if got := crewapi.CheckPublish([]byte(all), p); got == nil || got.Check != crewapi.CheckPaths {
		t.Errorf("all three: got %+v, want the path check first", got)
	}
	if got := crewapi.CheckPublish([]byte("\u202e gitleaks:allow\n"), p); got == nil || got.Check != crewapi.CheckHidden {
		t.Errorf("hidden and allow: got %+v, want the hidden check first", got)
	}
}

// slurp reads a file whose absence is itself the failure being reported.
func slurp(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}
