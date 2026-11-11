package ships

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These refusals need no disk, so they come before a clone: each request names a repo that has a checkout, which the clone path would refuse with a 409.
func TestPinRefusalsComeBeforeTheClone(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "widget")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = https://github.com/acme/widget.git\n"
	if err := os.WriteFile(filepath.Join(checkout, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	const text = `"text":"see https://github.com/acme/widget/issues/1"`
	for _, c := range []struct{ name, skill, crew, msg string }{
		{"pins on a launch that is not Commodore", `{"kind":"none"}`, `["scout"]`, "Commodore"},
		{"a name that is not a crew member name", `{"kind":"commodore"}`, `["Not A Name"]`, "Not A Name"},
		{"the same member twice", `{"kind":"commodore"}`, `["scout","scout"]`, "scout"},
	} {
		body := `{"fleet":"work","harness":"claude","skill":` + c.skill + `,"clone":true,"crew":` + c.crew + `,` + text + `}`
		rec := launchPost(t, cloneService(t, root, ""), body)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), c.msg) {
			t.Errorf("%s: %d %s, want a 400 naming %q", c.name, rec.Code, rec.Body, c.msg)
		}
	}
}
