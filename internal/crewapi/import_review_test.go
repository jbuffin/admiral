package crewapi_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/crewapi"
)

// paddedTo is a member file named name that is exactly size bytes long.
func paddedTo(name string, size int) string {
	text := memberFile(name, "")
	return text + strings.Repeat("x", size-len(text)-1) + "\n"
}

func TestARenamedImportThatGrowsPastTheSizeLimitIsRefusedAndNothingIsWritten(t *testing.T) {
	p := newImp(t, impOpts{})
	u := scout
	u.text = paddedTo("scout", 65536)
	p.gh.seed(u)
	status, pv := p.importPreview("hubot/crew/scout")
	wantStatus(t, "preview", status, 200, pv)

	status, out := p.importFrom(pv, map[string]any{"name": "scout-hubot"})
	wantStatus(t, "import", status, 422, out)
	wantText(t, "import", out, "65536")
	p.wantNothingImported("a renamed file over the limit")
}

func TestAnImportThroughALinkedCrewDirectoryIsRefusedAndNothingLandsThroughIt(t *testing.T) {
	p := newImp(t, impOpts{})
	p.gh.seed(scout)
	status, pv := p.importPreview("hubot/crew/scout")
	wantStatus(t, "preview", status, 200, pv)

	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(p.crewDir()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, p.crewDir()); err != nil {
		t.Fatal(err)
	}
	status, out := p.importFrom(pv, nil)
	wantStatus(t, "import", status, 409, out)
	wantText(t, "import", out, "a link is in the path", "nothing was changed")
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("%d files landed through the link", len(entries))
	}
}

func TestMissingSkillsHonoursTheHarnessTheCrewPageIsOn(t *testing.T) {
	p := newImp(t, impOpts{installed: []string{"claude-only"}}) // the claude dock sees it; the codex dock doesn't
	u := scout
	u.text = memberFile("scout", "skills:\n  - claude-only\n")
	p.gh.seed(u)

	for harness, want := range map[string][]any{"claude": {}, "codex": {"claude-only"}} {
		status, pv := p.post("/imports/preview?harness="+harness, map[string]any{"ref": "hubot/crew/scout"})
		wantStatus(t, "preview on "+harness, status, 200, pv)
		if got := pv["missing_skills"]; !reflect.DeepEqual(got, want) {
			t.Errorf("preview on %s: missing_skills = %v, want %v", harness, got, want)
		}
	}
}

func TestAFailedSidecarWriteLeavesTheMemberAsAPlainFleetMember(t *testing.T) {
	p := newImp(t, impOpts{})
	p.gh.seed(scout)
	status, pv := p.importPreview("hubot/crew/scout")
	wantStatus(t, "preview", status, 200, pv)

	crewapi.FailSidecarWrites(t)
	status, out := p.importFrom(pv, nil)
	if status < 400 {
		t.Fatalf("import: status %d, want an error (%v)", status, out)
	}
	wantText(t, "import", out, "the disk is full")
	if got := slurp(filepath.Join(p.crewDir(), "scout.md")); got != scout.text {
		t.Errorf("scout.md = %q, want the imported file kept as it was", got)
	}
	if exists(filepath.Join(p.crewDir(), "scout.shared.json")) {
		t.Error("a sidecar is there although its write failed")
	}
}

func TestATagWithAHiddenCharacterOrAnUnusableRefIsNoTag(t *testing.T) {
	for name, tag := range map[string]string{"hidden character": "v1‮", "unusable ref": "v 1"} {
		t.Run(name, func(t *testing.T) {
			p := newImp(t, impOpts{})
			u := scout
			u.tags = []string{tag}
			p.gh.seed(u)
			status, pv := p.importPreview("hubot/crew/scout")
			wantStatus(t, "preview", status, 200, pv)
			if pv["tag"] != nil {
				t.Errorf("preview tag = %v, want none", pv["tag"])
			}
			status, out := p.importFrom(pv, nil)
			wantStatus(t, "import", status, 200, out)
			side := readSidecarMap(t, filepath.Join(p.crewDir(), "scout.shared.json"))
			if side["tag"] != nil {
				t.Errorf("sidecar tag = %v, want null", side["tag"])
			}
		})
	}
}

func TestAFileThatChangedToAnInvalidOneIsAConflictNotARefusal(t *testing.T) {
	p := newImp(t, impOpts{})
	p.gh.seed(scout)
	status, pv := p.importPreview("hubot/crew/scout")
	wantStatus(t, "preview", status, 200, pv)

	changed := scout
	changed.text = memberFile("scout", "") + "hidden \u202e here\n"
	p.gh.unseed()
	p.gh.seed(changed)
	status, out := p.importFrom(pv, nil)
	wantStatus(t, "import", status, 409, out)
	wantText(t, "import", out, "The file changed since the preview")
	if out["kind"] != "changed" {
		t.Errorf("kind = %v, want changed", out["kind"])
	}
	p.wantNothingImported("a file that changed")
}
