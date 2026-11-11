package ships

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemberRefusalsComeBeforeTheClone(t *testing.T) {
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
	for _, c := range []struct{ name, extra, msg string }{
		{"a member on a Commodore launch", `"skill":{"kind":"commodore"},"member":"scout"`, "Just launch"},
		{"a member next to crew pins", `"skill":{"kind":"none"},"member":"scout","crew":["mate"]`, "crew"},
		{"a member whose name breaks the rule", `"skill":{"kind":"none"},"member":"Not A Name"`, "Not A Name"},
	} {
		body := `{"fleet":"work","harness":"claude","clone":true,` + c.extra + `,` + text + `}`
		rec := launchPost(t, cloneService(t, root, ""), body)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), c.msg) {
			t.Errorf("%s: %d %s, want a 400 naming %q", c.name, rec.Code, rec.Body, c.msg)
		}
	}
}
