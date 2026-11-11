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

// Allow once is item 1. Deny is Up then Enter: Up from the first item wraps to "No", the last, whatever the menu's length.
var claude = Map{
	permission: map[string]string{"allow": "1", "deny": "\x1b[A\r"},
	question:   strconv.Itoa,
}
