package crew

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// NoteCap is how many notes one scope loads; ADR 0010's cap is per scope.
const NoteCap = noteCap

// FixChecker is the role whose members have no memory and no skills or instructions.
const FixChecker = "fix-checker"

// ValidNote reports whether line ("- YYYY-MM-DD (<run>): <fact>", without a trailing "\r") is a note the
// resolver would load.
func ValidNote(line string) bool {
	m := notePattern.FindStringSubmatch(line)
	return m != nil && realDate(atoi(m[1]), atoi(m[2]), atoi(m[3])) && !hiddenChar(line)
}

// ParseNotes returns the notes of a scope file's lines as the resolver loads them: at most NoteCap, without the dash.
func ParseNotes(lines []string) []string {
	var ignored []Diagnostic
	return parseNotes(lines, "", &ignored)
}

// SplitRepo splits "<owner>/<repo>" into two segments that are safe in a path.
func SplitRepo(slug string) (owner, repo string, ok bool) {
	owner, repo, found := strings.Cut(slug, "/")
	return owner, repo, found && segmentsOK(owner, repo)
}

// MemberNotes loads every scope of one member's memory under root: fleet.md and each repos/<owner>/<repo>.md
// that holds a note. A link, a non-directory or a name that is no path segment is skipped with a warning.
func MemberNotes(root, name string, warns *[]Diagnostic) (fleet []string, repos map[string][]string) {
	fleet, repos = []string{}, map[string][]string{}
	dir := filepath.Join(root, name)
	ok, prob := lookDir(dir, name)
	if prob != nil {
		*warns = append(*warns, prob.warning(name))
		return fleet, repos
	}
	if !ok {
		return fleet, repos
	}
	fleet = loadScope(filepath.Join(dir, "fleet.md"), name+"/fleet.md", warns)
	reposDir, reposRel := filepath.Join(dir, "repos"), name+"/repos"
	if ok, prob := lookDir(reposDir, reposRel); prob != nil {
		*warns = append(*warns, prob.warning(reposRel))
		return fleet, repos
	} else if !ok {
		return fleet, repos
	}
	for _, owner := range dirNames(reposDir) {
		ownerDir, ownerRel := filepath.Join(reposDir, owner), reposRel+"/"+owner
		if !segmentsOK(owner) {
			*warns = append(*warns, memoryWarning("memory-refused", ownerRel, ownerRel+" is not a valid name, skipped"))
			continue
		}
		if ok, prob := lookDir(ownerDir, ownerRel); prob != nil {
			*warns = append(*warns, prob.warning(ownerRel))
			continue
		} else if !ok {
			continue
		}
		for _, file := range dirNames(ownerDir) {
			repo, isNotes := strings.CutSuffix(file, ".md")
			if !isNotes {
				continue
			}
			rel := ownerRel + "/" + file
			if !segmentsOK(repo) {
				*warns = append(*warns, memoryWarning("memory-refused", rel, rel+" is not a valid name, skipped"))
				continue
			}
			if notes := loadScope(filepath.Join(ownerDir, file), rel, warns); len(notes) > 0 {
				repos[owner+"/"+repo] = notes
			}
		}
	}
	return fleet, repos
}

func dirNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// A FieldError names the member field a file is refused for; Field is empty when no one field is at fault.
type FieldError struct{ Field, Message string }

func (e *FieldError) Error() string { return e.Message }

// CheckFile reads raw as the resolver would read the fleet layer's file stem.md: the parser, the field checks
// and the review that refuses a member. It returns the member as the resolver would list it.
func CheckFile(raw []byte, stem string) (Member, *FieldError) {
	return checkMember(raw, stem, LayerFleet)
}

// CheckAdHoc reads raw as the ad hoc layer's file stem.md. The member must say ad-hoc: true and can't be the fix checker.
func CheckAdHoc(raw []byte, stem string) (Member, *FieldError) {
	m, ferr := checkMember(raw, stem, LayerAdHoc)
	switch {
	case ferr != nil:
		return m, ferr
	case !m.AdHoc:
		return Member{}, &FieldError{Field: "ad-hoc", Message: "ad-hoc: true is missing"}
	case m.Role == FixChecker:
		return Member{}, &FieldError{Field: "role", Message: "an ad hoc member can't be the fix checker"}
	}
	return m, nil
}

func checkMember(raw []byte, stem, layer string) (Member, *FieldError) {
	var none Member
	if len(raw) > maxBytes {
		return none, &FieldError{Field: "instructions", Message: "file is over 65536 bytes"}
	}
	fm, body, perr := parseMember(raw)
	if perr == nil {
		perr = checkFields(fm, stem)
	}
	if perr != nil {
		return none, &FieldError{Field: perr.field, Message: perr.message}
	}
	var refusal *FieldError
	skills, instructions, refused := reviewFields(fm, body, layer, func(code, field, message string) {
		if refusal == nil && slices.Contains([]string{"bad-based-on", "misplaced-ad-hoc"}, code) {
			refusal = &FieldError{Field: field, Message: message}
		}
	})
	if refused && refusal != nil {
		return none, refusal
	}
	return Member{
		Name:         stem,
		Role:         fm.get("role").(string),
		BasedOn:      stringOrNil(fm.get("based-on")),
		Skills:       skills,
		Model:        stringOrNil(fm.get("model")),
		Harness:      stringOrNil(fm.get("harness")),
		PickMeWhen:   fm.get("pick-me-when").(string),
		AdHoc:        fm.get("ad-hoc") == true,
		AdHocRun:     stringOrNil(fm.get("ad-hoc-run")),
		Instructions: instructions,
		Layer:        layer,
		File:         stem + ".md",
	}, nil
}
