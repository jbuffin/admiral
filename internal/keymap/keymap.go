// Package keymap holds the keys that answer a harness's permission and question menus.
package keymap

import (
	"strconv"
	"strings"
)

// Map gives the raw bytes a pane's TUI takes as an answer; ok is false for an answer the menu has no key for.
type Map struct {
	permission map[string]string
	question   func(option int) string
}

func (m Map) Permission(choice string) (keys string, ok bool) {
	keys, ok = m.permission[choice]
	return keys, ok
}

func (m Map) Question(option int) (keys string, ok bool) {
	if option < 1 || option > 9 {
		return "", false
	}
	return m.question(option), true
}

// For is false for a harness whose menus have no recorded keys.
func For(harness string) (Map, bool) {
	switch harness {
	case "claude":
		return claude, true
	case "copilot":
		return copilot, true
	case "codex":
		return codex, true
	}
	return Map{}, false
}

// Allow once is item 1. Deny is Esc, the menu's own cancel: unlike item keys it does nothing at an idle prompt,
// where Up and Enter would resubmit the last prompt.
var claude = Map{
	permission: map[string]string{"allow": "1", "deny": "\x1b"},
	question:   strconv.Itoa,
}

// Copilot's menus: digit 1 allows once (2 saves an approval, so it is never sent) and Esc rejects. Its question form
// ignores digits and takes Down to the item, then Enter; the form's own Other item comes after the listed options.
var copilot = Map{
	permission: map[string]string{"allow": "1", "deny": "\x1b"},
	question:   func(option int) string { return strings.Repeat("\x1b[B", option-1) + "\r" },
}

// Codex's menus take the digit at once, and "Yes, proceed" is item 1 on the command and the patch menu. Deny is Esc,
// the menu's own cancel: Codex ends the turn on it, and at an idle prompt one Esc only hints "esc again to edit previous
// message" and submits nothing.
var codex = Map{
	permission: map[string]string{"allow": "1", "deny": "\x1b"},
	question:   strconv.Itoa,
}
