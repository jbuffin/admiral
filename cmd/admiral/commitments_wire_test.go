package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
)

func checkout(t *testing.T, root, name, url string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = " + url + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestACampaignChildLaunchesInItsOwnReposCheckout(t *testing.T) {
	root := t.TempDir()
	work := checkout(t, root, "web", "https://github.com/acme/web.git")
	checkout(t, root, "docs", "https://github.com/acme/docs.git")
	req := launcher.Request{Clone: true, CloneRepo: "github.com/acme/web", Text: "https://github.com/acme/docs/issues/3 https://github.com/acme/web/issues/21"}
	if got, ok := checkoutFor(req, []string{root}); !ok || got != work {
		t.Errorf("checkout = %q %v, want %q", got, ok, work)
	}
}

func TestACampaignChildClonesItsRepoWhenOnlyTheMapsRepoIsCheckedOut(t *testing.T) {
	root := t.TempDir()
	checkout(t, root, "docs", "https://github.com/acme/docs.git")
	req := launcher.Request{Clone: true, CloneRepo: "github.com/acme/web", Text: "https://github.com/acme/docs/issues/3 https://github.com/acme/web/issues/21"}
	if got, ok := checkoutFor(req, []string{root}); ok {
		t.Errorf("checkout = %q, want none so the child clones its own repo", got)
	}
}

func TestACloneLaunchWithNoNamedRepoStillFindsTheCheckoutItsTextNames(t *testing.T) {
	root := t.TempDir()
	dir := checkout(t, root, "web", "https://github.com/acme/web.git")
	req := launcher.Request{Clone: true, Text: "fix https://github.com/acme/web/issues/21"}
	if got, ok := checkoutFor(req, []string{root}); !ok || got != dir {
		t.Errorf("checkout = %q %v, want %q", got, ok, dir)
	}
}
