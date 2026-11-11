// Package keymap holds the keys that answer a harness's permission and question menus.
package keymap

import "strconv"

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
	if harness != "claude" {
		return Map{}, false
	}
	return claude, true
}

// Allow once is item 1. Deny is Esc, the menu's own cancel: unlike item keys it does nothing at an idle prompt,
// where Up and Enter would resubmit the last prompt.
var claude = Map{
	permission: map[string]string{"allow": "1", "deny": "\x1b"},
	question:   strconv.Itoa,
}
