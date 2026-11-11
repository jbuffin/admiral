package ships

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

type repoOrder struct{ repo string }

func (o repoOrder) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	return "", "title", o.repo, "", "p", nil
}
func (repoOrder) Launched(source, id string) {}

func cloneService(t *testing.T, root, orderRepo string) *Service {
	t.Helper()
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{root},
		Harnesses: []config.Harness{{Name: "claude"}}}}}
	return &Service{
		Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"),
		Orders:   repoOrder{orderRepo},
		Launcher: &launcher.Launcher{Config: func() *config.Config { return cfg }},
	}
}

// Each refusal comes before gh runs: none of these services could clone.
func TestCloneLaunchRefusals(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "widget")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = https://github.com/acme/widget.git\n"
	if err := os.WriteFile(filepath.Join(checkout, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	base := `"fleet":"work","harness":"claude","skill":{"kind":"none"},"clone":true`
	cases := []struct {
		name, body, orderRepo string
		status                int
		msg                   string
	}{
		{"clone with dir", `{` + base + `,"dir":"` + root + `"}`, "", 400, "dir or clone, not both"},
		{"a repo that has a checkout, from text", `{` + base + `,"text":"see https://github.com/acme/widget/issues/1"}`, "", 409, checkout},
		{"a repo that has a checkout, from an order's repo field", `{` + base + `,"source":"s","order_id":"o1"}`, "https://github.com/acme/widget", 409, checkout},
		{"the repo changed since the form showed it", `{` + base + `,"clone_repo":"github.com/acme/other","text":"https://github.com/acme/widget"}`, "", 409, "reopen the form"},
		// a campaign launch names the repo as host/owner/repo, the launcher's identity form, never as a URL
		{"clone_repo in the identity form is the repo the order shows", `{` + base + `,"clone_repo":"github.com/acme/widget","source":"s","order_id":"o1"}`, "https://github.com/acme/widget", 409, checkout},
		{"clone_repo as a URL is not the identity", `{` + base + `,"clone_repo":"https://github.com/acme/widget","source":"s","order_id":"o1"}`, "https://github.com/acme/widget", 409, "reopen the form"},
		{"no repo", `{` + base + `,"text":"nothing here"}`, "", 400, "no repository"},
	}
	for _, c := range cases {
		rec := launchPost(t, cloneService(t, root, c.orderRepo), c.body)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.msg) {
			t.Errorf("%s: %d %s, want %d naming %q", c.name, rec.Code, rec.Body, c.status, c.msg)
		}
	}
}
