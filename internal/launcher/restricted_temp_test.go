package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRemovingATempLinkWorksWithARelativeHome(t *testing.T) {
	home := t.TempDir()
	link, err := MakeRestrictedTemp(home, "s1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(link) })
	t.Chdir(filepath.Dir(home))
	RemoveRestrictedTemp(filepath.Base(home), "s1")
	if _, err := os.Lstat(link); err == nil {
		t.Errorf("the link %s is still there after removing with a relative home", link)
	}
	if _, err := os.Stat(filepath.Join(home, "restricted", "s1-tmp")); err == nil {
		t.Error("the ship's temp dir is still there")
	}
}

func TestStartingSweepsOnlyDanglingLinksThatPointUnderThisHomesRestrictedDir(t *testing.T) {
	home, other := t.TempDir(), t.TempDir()
	root := linkRoot()
	mk := func(target string) string {
		for i := 0; ; i++ {
			link := filepath.Join(root, fmt.Sprintf("adm-t%d-%d", os.Getpid(), i))
			if err := os.Symlink(target, link); err == nil {
				t.Cleanup(func() { os.Remove(link) })
				return link
			}
		}
	}
	gone := mk(filepath.Join(home, "restricted", "gone-tmp"))
	elsewhere := mk(filepath.Join(other, "restricted", "gone-tmp"))
	outside := mk(filepath.Join(home, "elsewhere", "gone-tmp"))
	live, err := MakeRestrictedTemp(home, "live")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RemoveRestrictedTemp(home, "live") })

	SweepRestrictedLinks(home)

	if _, err := os.Lstat(gone); err == nil {
		t.Errorf("the dangling link %s under this home was not removed", gone)
	}
	for name, l := range map[string]string{"a dangling link pointing under another home": elsewhere, "a dangling link not under restricted/": outside, "a link to a live ship's dir": live} {
		if _, err := os.Lstat(l); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}
