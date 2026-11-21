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
	return writeUntrusted(checkout, "feedback-"+strconv.Itoa(round)+".md", text)
}

// OrderPath is where a ship's order text lives in its checkout.
func OrderPath(checkout string) string {
	return filepath.Join(checkout, ".admiral", "untrusted", orderFile)
}

// RemoveOrder deletes a ship's order file through the same guarded root as WriteOrder; a missing file is fine.
func RemoveOrder(checkout string) error {
	root, err := os.OpenRoot(checkout)
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.Remove(filepath.Join(".admiral", "untrusted", orderFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

const orderFile = "order.md"

// WriteOrder puts a ship's order text in <checkout>/.admiral/untrusted/order.md the way WriteFeedback writes a round's
// feedback, and returns its path.
func WriteOrder(checkout, text string) (string, error) {
	return writeUntrusted(checkout, orderFile, text)
}

func writeUntrusted(checkout, name, text string) (string, error) {
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
	// Absent only: an engineer's own .gitignore stays. It keeps the untrusted files out of the ship's commits.
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
	rel := filepath.Join(".admiral", "untrusted", name)
	// A fresh file, so a hard link planted at the name is dropped, not written through.
	switch fi, err := root.Lstat(rel); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return "", err
	case !fi.Mode().IsRegular():
		return "", fmt.Errorf("moor: %s is not a regular file", filepath.Join(checkout, rel))
	default:
		if err := root.Remove(rel); err != nil {
			return "", err
		}
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
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
