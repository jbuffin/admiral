package launcher

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxTempLink keeps Claude Code's sandbox $TMPDIR on the ship's own dir: a CLAUDE_CODE_TMPDIR much longer than this
// makes it fall back to the shared per-user temp dir.
const maxTempLink = 28

func restrictedTempDir(home, session string) string {
	return filepath.Join(home, "restricted", session+"-tmp")
}

func restrictedTempLinkFile(home, session string) string {
	return filepath.Join(home, "restricted", session+"-tmp.link")
}

// RestrictedTempEnv points Claude Code's temp dir, and so the sandbox's $TMPDIR, at the ship's temp link.
func RestrictedTempEnv(link string) map[string]string {
	return map[string]string{"CLAUDE_CODE_TMPDIR": link, "TMPDIR": link}
}

func linkRoot() string {
	if fi, err := os.Stat("/private/tmp"); err == nil && fi.IsDir() {
		return "/private/tmp"
	}
	return "/tmp"
}

// MakeRestrictedTemp makes a ship's own temp dir under home and a short link to it, and returns the link. The link is
// short because Claude Code only follows CLAUDE_CODE_TMPDIR into the sandbox when it is.
func MakeRestrictedTemp(home, session string) (string, error) {
	if _, _, ok := ShipFiles(home, session); !ok {
		return "", fmt.Errorf("%q is not a name a ship can have", session)
	}
	RemoveRestrictedTemp(home, session)
	dir := restrictedTempDir(home, session)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	root := linkRoot()
	for range 10 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		link := filepath.Join(root, "adm-"+hex.EncodeToString(b[:]))
		if len(link) > maxTempLink {
			return "", fmt.Errorf("the temp link %s is longer than %d characters", link, maxTempLink)
		}
		// Symlink fails when the name exists and never follows what is there.
		err := os.Symlink(target, link)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(restrictedTempLinkFile(home, session), []byte(link+"\n"), 0o600); err != nil {
			_ = os.Remove(link)
			return "", err
		}
		return link, nil
	}
	return "", errors.New("could not find a free name for the ship's temp link")
}

// RemoveRestrictedTemp removes a ship's temp link, only while it still points at the ship's own dir, and the dir.
func RemoveRestrictedTemp(home, session string) {
	if _, _, ok := ShipFiles(home, session); !ok {
		return
	}
	// A relative home would never equal the absolute target MakeRestrictedTemp wrote into the link.
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	file := restrictedTempLinkFile(home, session)
	if b, err := os.ReadFile(file); err == nil {
		link := strings.TrimSpace(string(b))
		target, _ := filepath.EvalSymlinks(restrictedTempDir(home, session))
		if got, err := os.Readlink(link); err == nil && target != "" && got == target && filepath.Dir(link) == linkRoot() {
			_ = os.Remove(link)
		}
	}
	_ = os.Remove(file)
	root, err := os.OpenRoot(home)
	if err != nil {
		return
	}
	defer root.Close()
	rel := filepath.Join("restricted", session+"-tmp")
	if realDir(root, "restricted") {
		if fi, err := root.Lstat(rel); err == nil && fi.IsDir() {
			_ = root.RemoveAll(rel)
		} else if err == nil {
			_ = root.Remove(rel)
		}
	}
}

// SweepRestrictedLinks removes the temp links a crashed Admiral left: those in the link root whose target is gone and
// was under this home's restricted dir. A link anywhere else is another Admiral's or not ours, and stays.
func SweepRestrictedLinks(home string) {
	abs, err := filepath.Abs(home)
	if err != nil {
		return
	}
	prefixes := []string{filepath.Join(abs, "restricted") + string(filepath.Separator)}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		prefixes = append(prefixes, filepath.Join(real, "restricted")+string(filepath.Separator))
	}
	root := linkRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "adm-") || e.Type()&os.ModeSymlink == 0 {
			continue
		}
		link := filepath.Join(root, e.Name())
		target, err := os.Readlink(link)
		if err != nil {
			continue
		}
		if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(target, p) {
				_ = os.Remove(link)
				break
			}
		}
	}
}
