package crew

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePinMember(t *testing.T, dir, name, role string) {
	t.Helper()
	body := "---\nname: " + name + "\nrole: " + role + "\npick-me-when: Pick.\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadFileRefusesWhatChangedAfterResolving(t *testing.T) {
	dir := t.TempDir()
	writePinMember(t, dir, "scout", "scribe")
	m := Member{Name: "scout", Role: "scribe", File: "scout.md"}
	if _, err := ReadFile(dir, m); err != nil {
		t.Fatalf("ReadFile of the resolved file: %v", err)
	}
	writePinMember(t, dir, "scout", "reviewer")
	if _, err := ReadFile(dir, m); err == nil {
		t.Error("ReadFile after the role changed = nil, want a refusal")
	}
	huge := "---\nname: scout\nrole: scribe\npick-me-when: Pick.\n---\n" + strings.Repeat("x", maxBytes)
	if err := os.WriteFile(filepath.Join(dir, "scout.md"), []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(dir, m); err == nil {
		t.Error("ReadFile of an oversize file = nil, want a refusal")
	}
}

func TestReadFileRefusesALinkOutsideTheLayer(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	writePinMember(t, other, "scout", "scribe")
	if err := os.Symlink(filepath.Join(other, "scout.md"), filepath.Join(dir, "scout.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(dir, Member{Name: "scout", Role: "scribe", File: "scout.md"}); err == nil {
		t.Error("ReadFile through a link out of the layer = nil, want a refusal")
	}
}
