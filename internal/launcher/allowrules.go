package launcher

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"

	"github.com/jbuffin/admiral/internal/allowrules"
	"github.com/jbuffin/admiral/internal/config"
)

// RepoKey is the repo an allow rule names: the origin's owner/repo, else the checkout's path.
func RepoKey(dir string) string {
	if slug, _ := OriginSlug(dir); slug != "" {
		return slug
	}
	return filepath.Clean(dir)
}

// AllowRules is the fleets' allow rule store; a change to a fleet's rules rewrites the Codex rules files.
func (l *Launcher) AllowRules() *allowrules.Store {
	s := allowrules.Open(l.Home)
	s.OnChange = func(string) {
		if err := SyncCodexRules(l.Home, l.Config(), ""); err != nil {
			l.allowWarn(err)
		}
	}
	return s
}

func (l *Launcher) allowWarn(err error) {
	msg := "allow rules: " + err.Error()
	if l.Warn != nil {
		l.Warn(msg)
		return
	}
	log.Printf("admiral: %s", msg)
}

// ruledFlags are the hook flags of a ship or crew boat of harness in fleet, working in the checkout dir,
// with the fleet's allow rules added. A rule that can't be written is left out and the launch goes on.
func (l *Launcher) ruledFlags(fleet config.Fleet, harness, dir string) []string {
	flags, _ := l.hookFlags(harness)
	switch harness {
	case "claude":
		store := allowrules.Open(l.Home)
		all, err := store.List(fleet.Key)
		if err != nil {
			l.allowWarn(err)
		}
		if !slices.ContainsFunc(all.Rules, func(r allowrules.Rule) bool { return r.Harness == harness }) {
			return flags // no git call for a fleet with no Claude rules
		}
		rules, _ := store.For(fleet.Key, harness, RepoKey(dir))
		if len(rules) == 0 {
			return flags
		}
		path, err := writeClaudeRuled(l.Home, rules)
		if err != nil {
			l.allowWarn(err)
			return flags
		}
		return []string{"--settings", path}
	case "codex":
		if dock, ok := fleet.Harness(harness); ok {
			if err := SyncCodexRules(l.Home, l.Config(), config.IdentityDir(harness, dock.Env)); err != nil {
				l.allowWarn(err)
			}
		}
	}
	return flags
}

// writeClaudeRuled writes the ship settings with rules as permissions.allow, which Claude Code adds to the
// engineer's own list. The file is named for its content, so a live ship's file never changes under it.
func writeClaudeRuled(home string, rules []allowrules.Rule) (string, error) {
	allow := []any{}
	for _, r := range rules {
		allow = append(allow, r.Rule)
	}
	settings := claudeSettings().(map[string]any)
	settings["permissions"] = map[string]any{"allow": allow}
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	sum := sha256.Sum256(raw)
	path := filepath.Join(home, "harness", "claude-settings-"+hex.EncodeToString(sum[:8])+".json")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, raw) {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return path, allowrules.WriteFile(path, raw)
}

// SyncCodexRules writes each fleet's Codex rules into the admiral.rules of its Codex dock's CODEX_HOME;
// fleets that share a CODEX_HOME share the file. With only set, it writes that one CODEX_HOME, and only
// when it has rules: a launch never removes a file.
func SyncCodexRules(home string, cfg *config.Config, only string) error {
	if cfg == nil {
		return nil
	}
	store := allowrules.Open(home)
	byDir := map[string][]allowrules.Rule{}
	var dirs []string
	var errs []error
	for _, f := range cfg.Fleets {
		dock, ok := f.Harness("codex")
		if !ok {
			continue
		}
		dir := config.IdentityDir("codex", dock.Env)
		if only != "" && dir != only {
			continue
		}
		rules, err := store.For(f.Key, "codex", "")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, seen := byDir[dir]; !seen {
			dirs = append(dirs, dir)
		}
		byDir[dir] = append(byDir[dir], rules...)
	}
	if len(errs) > 0 {
		// A store that can't be read says nothing about which rules to drop.
		return errors.Join(errs...)
	}
	for _, dir := range dirs {
		if only != "" && len(byDir[dir]) == 0 {
			continue
		}
		errs = append(errs, allowrules.WriteCodexRules(dir, byDir[dir]))
	}
	return errors.Join(errs...)
}
