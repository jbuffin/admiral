package crew

import (
	"os"
	"regexp"
	"slices"
)

var (
	roles         = []string{"implementer", "test-writer", "reviewer", "security-reviewer", "brief-checker", "triager", "target-steward", "scribe", "fix-checker"}
	harnesses     = []string{"claude", "codex", "copilot"}
	knownFields   = []string{"name", "role", "pick-me-when", "model", "harness", "ad-hoc", "ad-hoc-run", "based-on", "skills"}
	slugPattern   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)
	skillPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	fixCheckerKey = "fix-checker"
)

func isSlug(v any) bool {
	s, ok := v.(string)
	return ok && slugPattern.MatchString(s)
}

func stringOrNil(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func checkFields(fm frontmatter, stem string) *memberError {
	name := fm.get("name")
	switch {
	case name == nil:
		return &memberError{"missing-field", "name", "name is required"}
	case !isSlug(name):
		return &memberError{"bad-name", "name", "name must be a lowercase slug of at most 64 characters"}
	case name != stem:
		return &memberError{"name-mismatch", "name", "name " + name.(string) + " does not match the file name " + stem}
	}
	role := fm.get("role")
	switch {
	case role == nil:
		return &memberError{"missing-field", "role", "role is required"}
	case !isSlug(role):
		return &memberError{"bad-field", "role", "role must be a lowercase slug"}
	}
	pick := fm.get("pick-me-when")
	if s, ok := pick.(string); pick == nil || (ok && trimBlank(s) == "") {
		return &memberError{"missing-field", "pick-me-when", "pick-me-when is required"}
	} else if !ok {
		return &memberError{"bad-field", "pick-me-when", "pick-me-when must be a string"}
	}
	return checkOptionalFields(fm)
}

func checkOptionalFields(fm frontmatter) *memberError {
	if m := fm.get("model"); m != nil {
		if _, ok := m.(string); !ok {
			return &memberError{"bad-field", "model", "model must be a string"}
		}
	}
	if h := fm.get("harness"); h != nil {
		s, ok := h.(string)
		if !ok {
			return &memberError{"bad-field", "harness", "harness must be a string"}
		}
		if !slices.Contains(harnesses, s) {
			return &memberError{"bad-harness", "harness", "harness must be one of claude, codex, copilot"}
		}
	}
	if v := fm.get("ad-hoc"); v != nil {
		if _, ok := v.(bool); !ok {
			return &memberError{"bad-field", "ad-hoc", "ad-hoc must be true or false"}
		}
	}
	if v := fm.get("ad-hoc-run"); v != nil {
		if _, ok := v.(string); !ok {
			return &memberError{"bad-field", "ad-hoc-run", "ad-hoc-run must be a string"}
		}
	}
	switch fm.get("skills").(type) {
	case nil, string, []string:
	default:
		return &memberError{"bad-field", "skills", "skills must be a list of strings or one string"}
	}
	return nil
}

type warner func(code, field, message string)

func cleanSkills(v any, warn warner) []string {
	var skills []string
	switch s := v.(type) {
	case string:
		skills = []string{s}
	case []string:
		skills = s
	}
	kept := []string{}
	for _, skill := range skills {
		cleaned := skill
		if skill != "" && (skill[0] == '/' || skill[0] == '$') {
			cleaned = skill[1:]
		}
		switch {
		case !skillPattern.MatchString(cleaned):
			warn("bad-skill", "skills", "skill "+skill+" is not a valid skill name, dropped")
		case !slices.Contains(kept, cleaned):
			kept = append(kept, cleaned)
		}
	}
	return kept
}

func reviewFields(fm frontmatter, body, layer string, warn warner) (skills []string, instructions string, refused bool) {
	for _, key := range fm.keys {
		if !slices.Contains(knownFields, key) {
			warn("unknown-field", key, "unknown field "+key+", ignored")
		}
	}
	skills = cleanSkills(fm.get("skills"), warn)
	role := fm.get("role").(string)
	basedOn := fm.get("based-on")
	if role == fixCheckerKey {
		if len(skills) > 0 {
			warn("fix-checker-skills", "skills", "the fix checker takes no role skills, dropped")
			skills = []string{}
		}
		if body != "" {
			warn("fix-checker-instructions", "", "the fix checker takes no instructions, dropped")
			body = ""
		}
	}
	if slices.Contains(roles, role) {
		if basedOn != nil {
			warn("bad-based-on", "based-on", "a built-in role takes no based-on, member refused")
			refused = true
		}
	} else if b, ok := basedOn.(string); !ok || !slices.Contains(roles, b) || b == fixCheckerKey {
		warn("bad-based-on", "based-on", "a custom role needs based-on naming one of the roles but fix-checker")
		refused = true
	}
	if fm.get("ad-hoc") == true && layer != LayerAdHoc {
		warn("misplaced-ad-hoc", "ad-hoc", "ad hoc members live only in ad-hoc/, member refused")
		refused = true
	}
	return skills, body, refused
}

func readMember(path, layerReal, layer, stem string) (Member, []Diagnostic, bool, *memberError) {
	var none Member
	info, err := os.Stat(path)
	if err != nil {
		return none, nil, false, unreadable(err)
	}
	if info.Size() > maxBytes {
		return none, nil, false, &memberError{code: "too-large", message: "file is over 65536 bytes"}
	}
	if !within(realPath(path), layerReal) {
		return none, nil, false, &memberError{code: "outside-layer", message: "file resolves outside its layer directory"}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return none, nil, false, unreadable(err)
	}
	fm, body, perr := parseMember(raw)
	if perr != nil {
		return none, nil, false, perr
	}
	if me := checkFields(fm, stem); me != nil {
		return none, nil, false, me
	}

	file := stem + ".md"
	var warnings []Diagnostic
	warn := func(code, field, message string) {
		warnings = append(warnings, newDiag(layer, file, code, field, message))
	}
	skills, instructions, refused := reviewFields(fm, body, layer, warn)
	member := Member{
		Name:         fm.get("name").(string),
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
		File:         file,
		Notes:        Notes{Fleet: []string{}, Repo: []string{}},
	}
	return member, warnings, refused, nil
}

func unreadable(err error) *memberError {
	return &memberError{code: "unreadable", message: "file can't be read: " + strerror(err)}
}
