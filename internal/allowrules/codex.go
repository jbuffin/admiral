package allowrules

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// CodexRulesPath is the one file Admiral writes in a CODEX_HOME. Codex reads every *.rules file in
// rules/, so the engineer's own files there stay theirs.
func CodexRulesPath(codexHome string) string {
	return filepath.Join(codexHome, "rules", "admiral.rules")
}

// CodexRules is the rules file for rules, one prefix_rule each in order, with repeats dropped.
func CodexRules(rules []Rule) string {
	var b strings.Builder
	b.WriteString("# Admiral's allow rules. Admiral rewrites this file whenever they change, so an edit here is lost.\n")
	var seen [][]string
	for _, r := range rules {
		if r.Harness != "codex" || len(r.Pattern) == 0 || slices.ContainsFunc(seen, func(p []string) bool { return slices.Equal(p, r.Pattern) }) {
			continue
		}
		seen = append(seen, r.Pattern)
		quoted := make([]string, len(r.Pattern))
		for i, tok := range r.Pattern {
			quoted[i] = starlarkString(tok)
		}
		b.WriteString("prefix_rule(pattern=[" + strings.Join(quoted, ", ") + `], decision="allow")` + "\n")
	}
	return b.String()
}

// Check has already refused control characters, so a quote and a backslash are all there is to escape.
func starlarkString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// WriteCodexRules makes codexHome's admiral.rules hold exactly rules, and removes it when there are none.
func WriteCodexRules(codexHome string, rules []Rule) error {
	path := CodexRulesPath(codexHome)
	fi, err := os.Lstat(path)
	switch {
	case err == nil && !fi.Mode().IsRegular():
		return errors.New(path + " is not a regular file, so Admiral leaves it alone")
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if !slices.ContainsFunc(rules, func(r Rule) bool { return r.Harness == "codex" }) {
		if err == nil {
			return os.Remove(path)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return WriteFile(path, []byte(CodexRules(rules)))
}
