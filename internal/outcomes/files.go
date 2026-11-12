package outcomes

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

var monthFile = regexp.MustCompile(`^(\d{4}-\d{2})\.jsonl$`)

// MonthFile is one log file, named for the UTC month its records were written in.
type MonthFile struct {
	Path string
	// Start is the first instant of the file's UTC month; zero when the name is not a real month.
	Start time.Time
}

// End is the first instant after the file's month. A file of unknown month has none, so it is never skipped.
func (m MonthFile) End() time.Time {
	if m.Start.IsZero() {
		return time.Time{}
	}
	return m.Start.AddDate(0, 1, 0)
}

// MonthFiles lists the log files under dir, oldest first. A FIFO or a device is left out: reading it would hang.
func MonthFiles(dir string) ([]MonthFile, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []MonthFile
	for _, p := range paths {
		m := monthFile.FindStringSubmatch(filepath.Base(p))
		if fi, err := os.Lstat(p); m == nil || err != nil || !fi.Mode().IsRegular() {
			continue
		}
		start, _ := time.Parse("2006-01", m[1])
		out = append(out, MonthFile{Path: p, Start: start})
	}
	return out, nil
}
