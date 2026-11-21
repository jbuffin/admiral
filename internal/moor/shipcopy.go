package moor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// maxCopiedFile skips, never truncates, a report or inbox file larger than this.
const maxCopiedFile = 1 << 20

const (
	lastReportFile = "last-report.json"
	inboxCopyDir   = "inbox"
)

// ShipCopy is what a wake can carry from a ship's report and inbox into its checkout. Admiral's launch clears the
// originals, so the woken ship reads these copies.
type ShipCopy struct {
	Report    string   // the report file to copy, "" when there is none
	InboxDir  string   // the inbox directory the Items sit in
	InboxFile []string // base names of the .json files to copy
}

// LastReportPath is where the copy of the ship's last report lives in its checkout.
func LastReportPath(checkout string) string {
	return filepath.Join(checkout, ".admiral", "untrusted", lastReportFile)
}

// InboxCopyPath is the directory holding the copies of the ship's inbox items and answers.
func InboxCopyPath(checkout string) string {
	return filepath.Join(checkout, ".admiral", "untrusted", inboxCopyDir)
}

func copyable(fi fs.FileInfo) bool { return fi.Mode().IsRegular() && fi.Size() <= maxCopiedFile }

// PlanShipCopy lists the regular, small enough files a wake would copy; a link or a missing file is left out.
func PlanShipCopy(report, inboxDir string) ShipCopy {
	p := ShipCopy{InboxDir: inboxDir}
	if fi, err := os.Lstat(report); err == nil && copyable(fi) {
		p.Report = report
	}
	if fi, err := os.Lstat(inboxDir); err != nil || !fi.IsDir() {
		return p
	}
	ents, err := os.ReadDir(inboxDir)
	if err != nil {
		return p
	}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(inboxDir, e.Name())); err == nil && copyable(fi) {
			p.InboxFile = append(p.InboxFile, e.Name())
		}
	}
	return p
}

// WriteShipCopy replaces the copies of an earlier round with the planned ones in <checkout>/.admiral/untrusted, through
// the root writeUntrusted uses: a link planted at the copy directory or a file is refused, not followed. A source that
// vanished since the plan is skipped.
func WriteShipCopy(checkout string, p ShipCopy) error {
	// writeUntrusted makes the directories and .gitignore; it also drops the earlier report copy.
	if _, err := writeUntrusted(checkout, lastReportFile, ""); err != nil {
		return err
	}
	root, err := os.OpenRoot(checkout)
	if err != nil {
		return err
	}
	defer root.Close()
	rel := filepath.Join(".admiral", "untrusted", lastReportFile)
	dir := filepath.Join(".admiral", "untrusted", inboxCopyDir)
	if err := root.Remove(rel); err != nil {
		return err
	}
	switch fi, err := root.Lstat(dir); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case !fi.IsDir():
		return fmt.Errorf("moor: %s is not a directory", filepath.Join(checkout, dir))
	default:
		if err := root.RemoveAll(dir); err != nil {
			return err
		}
	}
	if p.Report != "" {
		if err := copyInto(root, rel, p.Report); err != nil {
			return err
		}
	}
	if len(p.InboxFile) == 0 {
		return nil
	}
	if err := root.Mkdir(dir, 0o700); err != nil {
		return err
	}
	for _, name := range p.InboxFile {
		if err := copyInto(root, filepath.Join(dir, name), filepath.Join(p.InboxDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func copyInto(root *os.Root, rel, src string) error {
	in, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return nil // a link swapped in since the plan is skipped
	}
	defer in.Close()
	if fi, err := in.Stat(); err != nil || !copyable(fi) {
		return nil
	}
	out, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, io.LimitReader(in, maxCopiedFile))
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	return cerr
}
