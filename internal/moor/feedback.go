package moor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// WriteFeedback puts a round's feedback text in <checkout>/.admiral/untrusted/feedback-<round>.md and returns its path.
// The ship's checkout is its own to write in, so a link planted at .admiral, untrusted or the file is refused, not followed.
func WriteFeedback(checkout string, round int, text string) (string, error) {
	root, err := os.OpenRoot(checkout)
	if err != nil {
		return "", err
	}
	defer root.Close()
	for _, d := range []string{".admiral", filepath.Join(".admiral", "untrusted")} {
		fi, err := root.Lstat(d)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := root.Mkdir(d, 0o700); err != nil {
				return "", err
			}
		case err != nil:
			return "", err
		case !fi.IsDir():
			return "", fmt.Errorf("moor: %s is not a directory", filepath.Join(checkout, d))
		}
	}
	// Absent only: an engineer's own .gitignore stays. It keeps the feedback out of the ship's commits.
	ignore, err := root.OpenFile(filepath.Join(".admiral", ".gitignore"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, werr := ignore.WriteString("*\n")
		if cerr := ignore.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return "", werr
		}
	} else if !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	rel := filepath.Join(".admiral", "untrusted", "feedback-"+strconv.Itoa(round)+".md")
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(text)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", werr
	}
	return filepath.Join(checkout, rel), nil
}
