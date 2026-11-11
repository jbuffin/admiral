package crew

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func adHocWarning(code, file, field, message string) Diagnostic {
	return newDiag(LayerAdHoc, file, code, field, message)
}

func adHocProblem(dir string) string {
	if info, err := os.Lstat(dir); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return dir + " is a symlink"
	}
	for _, part := range strings.Split(realPath(dir), string(filepath.Separator)) {
		if part == ".commodore" {
			return dir + " is inside a .commodore directory"
		}
	}
	return ""
}

// A file that can't be read holds no ad-hoc-run line.
func namesThisRun(path, layerReal, runID string) bool {
	if !within(realPath(path), layerReal) {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return false
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line := strings.TrimRight(string(line), "\r")
		if line == `ad-hoc-run: "`+runID+`"` || line == "ad-hoc-run: "+runID {
			return true
		}
	}
	return false
}

func acceptAdHoc(m Member, standing map[string]*Member) *Diagnostic {
	if earlier := standing[m.Name]; earlier != nil {
		w := adHocWarning("ad-hoc-name-taken", m.File, "name",
			"name "+m.Name+" is already a "+earlier.Layer+" member; an ad hoc member never replaces one, member refused")
		return &w
	}
	if m.Role == fixCheckerKey {
		w := adHocWarning("ad-hoc-fix-checker", m.File, "role", "an ad hoc member can't be the fix checker, member refused")
		return &w
	}
	return nil
}

// Files of other runs are skipped silently.
func readAdHoc(dir, runID string, standing map[string]*Member) []Diagnostic {
	if _, err := os.Lstat(dir); err != nil {
		return nil
	}
	if problem := adHocProblem(dir); problem != "" {
		return []Diagnostic{adHocWarning("ad-hoc-dir-refused", "", "", "ad hoc directory skipped: "+problem)}
	}
	names, err := memberFiles(dir)
	if err != nil {
		return []Diagnostic{adHocWarning("ad-hoc-unreadable", "", "", "ad hoc directory can't be listed: "+strerror(err))}
	}
	layerReal := realPath(dir)
	var warns []Diagnostic
	for _, name := range names {
		path, stem, reason := filepath.Join(dir, name), strings.TrimSuffix(name, ".md"), "ad-hoc: true is missing"
		m, found, refused, merr := readMember(path, layerReal, LayerAdHoc, stem)
		if merr != nil {
			reason = merr.message
		}
		if merr != nil || !m.AdHoc || m.AdHocRun == nil || *m.AdHocRun != runID {
			if namesThisRun(path, layerReal, runID) {
				warns = append(warns, adHocWarning("ad-hoc-unreadable", name, "", "member of this run is unusable: "+reason))
			}
			continue
		}
		warns = append(warns, found...)
		if refused {
			continue
		}
		if refusal := acceptAdHoc(m, standing); refusal != nil {
			warns = append(warns, *refusal)
		} else {
			standing[m.Name] = &m
		}
	}
	return warns
}
