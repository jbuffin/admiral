// Package crew resolves crew members from layered Markdown directories.
package crew

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"
)

const (
	layerBuiltIn    = "built-in"
	layerFleet      = "fleet"
	layerUserGlobal = "user-global"
	layerRepo       = "repo"
	layerRun        = "run"
	layerAdHoc      = "ad-hoc"
	layerMemory     = "memory"

	maxBytes = 65536
)

var layerOrder = []string{layerBuiltIn, layerFleet, layerUserGlobal, layerRepo, layerRun, layerAdHoc, layerMemory}

// Input is everything the resolver reads: it touches no environment variable and knows no Admiral path.
type Input struct {
	Admiral bool
	Layers  map[string]string // by layer label; a missing label is an empty layer
	Run     string            // the run id ad hoc members are filtered by
	Repo    string            // <owner>/<repo>, for repo notes
}

type Notes struct {
	Fleet []string `json:"fleet"`
	Repo  []string `json:"repo"`
}

type Member struct {
	Name         string   `json:"name"`
	Role         string   `json:"role"`
	BasedOn      *string  `json:"based-on"`
	Skills       []string `json:"skills"`
	Model        *string  `json:"model"`
	Harness      *string  `json:"harness"`
	PickMeWhen   string   `json:"pick-me-when"`
	AdHoc        bool     `json:"ad-hoc"`
	AdHocRun     *string  `json:"ad-hoc-run"`
	Instructions string   `json:"instructions"`
	Layer        string   `json:"layer"`
	File         string   `json:"file"`
	Notes        Notes    `json:"notes"`
}

type Diagnostic struct {
	Layer   string  `json:"layer"`
	File    *string `json:"file"`
	Code    string  `json:"code"`
	Field   *string `json:"field"`
	Message string  `json:"message"`
}

// Result serialises to the object the shared fixtures expect: members when Exit is 0, errors when it is 1.
type Result struct {
	Exit     int
	Members  []Member
	Errors   []Diagnostic
	Warnings []Diagnostic
}

func (r Result) MarshalJSON() ([]byte, error) {
	warnings := orEmpty(r.Warnings)
	if r.Exit == 0 {
		return json.Marshal(struct {
			Exit     int          `json:"exit"`
			Members  []Member     `json:"members"`
			Warnings []Diagnostic `json:"warnings"`
		}{r.Exit, orEmpty(r.Members), warnings})
	}
	return json.Marshal(struct {
		Exit     int          `json:"exit"`
		Errors   []Diagnostic `json:"errors"`
		Warnings []Diagnostic `json:"warnings"`
	}{r.Exit, r.Errors, warnings})
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func Resolve(in Input) Result {
	standing := map[string]*Member{}
	var errs, warns []Diagnostic
	for _, label := range layerOrder {
		dir := in.Layers[label]
		if dir == "" || label == layerMemory || ignoredLayer(label, in.Admiral) {
			continue
		}
		if label == layerAdHoc {
			if in.Run != "" {
				warns = append(warns, readAdHoc(dir, in.Run, standing)...)
			}
			continue
		}
		e, w := readLayer(label, dir, standing)
		errs = append(errs, e...)
		warns = append(warns, w...)
	}

	members := make([]Member, 0, len(standing))
	for _, m := range standing {
		members = append(members, *m)
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].Role != members[j].Role {
			return members[i].Role < members[j].Role
		}
		return members[i].Name < members[j].Name
	})
	if len(errs) == 0 {
		warns = append(warns, attachNotes(members, in.Layers[layerMemory], in.Repo)...)
	}
	sortDiagnostics(errs)
	sortDiagnostics(warns)
	exit := 0
	if len(errs) > 0 {
		exit = 1
	}
	return Result{Exit: exit, Members: members, Errors: errs, Warnings: warns}
}

func ignoredLayer(label string, admiral bool) bool {
	return (admiral && label == layerUserGlobal) || (!admiral && label == layerFleet)
}

func readLayer(label, dir string, standing map[string]*Member) (errs, warns []Diagnostic) {
	layerReal := realPath(dir)
	names, err := memberFiles(dir)
	if err != nil {
		return []Diagnostic{newDiag(label, "", "unreadable", "", "layer directory can't be listed: "+strerror(err))}, nil
	}
	for _, name := range names {
		stem := strings.TrimSuffix(name, ".md")
		m, found, refused, err := readMember(filepath.Join(dir, name), layerReal, label, stem)
		if err != nil {
			errs = append(errs, err.diagnostic(label, name))
			continue
		}
		warns = append(warns, found...)
		if earlier := standing[m.Name]; earlier != nil && !refused && (earlier.Role != m.Role || !sameString(earlier.BasedOn, m.BasedOn)) {
			field := "based-on"
			if earlier.Role != m.Role {
				field = "role"
			}
			warns = append(warns, newDiag(label, name, "role-change", field,
				field+" differs from the "+earlier.Layer+" member, member refused"))
			refused = true
		}
		if !refused {
			standing[m.Name] = &m
		}
	}
	return errs, warns
}

func usableName(name string) bool {
	return strings.HasSuffix(name, ".md") && !strings.HasPrefix(name, ".") && utf8.ValidString(name)
}

func memberFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !usableName(e.Name()) {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, e.Name())); err == nil && info.Mode().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func sameString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func newDiag(layer, file, code, field, message string) Diagnostic {
	return Diagnostic{Layer: layer, File: strPtr(file), Code: code, Field: strPtr(field), Message: message}
}

func layerIndex(label string) int {
	for i, l := range layerOrder {
		if l == label {
			return i
		}
	}
	return len(layerOrder)
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func sortDiagnostics(d []Diagnostic) {
	sort.SliceStable(d, func(i, j int) bool {
		a, b := d[i], d[j]
		switch {
		case layerIndex(a.Layer) != layerIndex(b.Layer):
			return layerIndex(a.Layer) < layerIndex(b.Layer)
		case (a.File != nil) != (b.File != nil):
			return a.File == nil
		case derefString(a.File) != derefString(b.File):
			return derefString(a.File) < derefString(b.File)
		case a.Code != b.Code:
			return a.Code < b.Code
		case (a.Field != nil) != (b.Field != nil):
			return a.Field == nil
		case derefString(a.Field) != derefString(b.Field):
			return derefString(a.Field) < derefString(b.Field)
		}
		return a.Message < b.Message
	})
}

// realPath resolves symlinks without cleaning the path first, so a ".." after a symlink leaves its target.
func realPath(p string) string {
	if !filepath.IsAbs(p) {
		if wd, err := os.Getwd(); err == nil {
			p = wd + string(filepath.Separator) + p
		}
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")
}

func strerror(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return err.Error()
}
