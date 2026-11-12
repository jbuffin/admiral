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
	// call answers a whole question call at once; nil where only a single question is answered.
	call func(qs []CallQuestion) (string, bool)
	// tools limits the permission card to the tools whose menus are recorded; nil allows any.
	tools map[string]bool
}

// CardTool is false for a tool whose approval menu has no recorded keys, so it is answered in the terminal.
func (m Map) CardTool(tool string) bool { return m.tools == nil || m.tools[tool] }

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

// CallQuestion is one question of a call and its answer: the listed options chosen (1-based, ascending) and an Other text.
type CallQuestion struct {
	Options int
	Multi   bool
	Choose  []int
	Other   string
}

// Call gives the keys that answer every question of one call, or false where the menu has no key for the answer.
func (m Map) Call(qs []CallQuestion) (keys string, ok bool) {
	if m.call == nil {
		return "", false
	}
	return m.call(qs)
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
	call:       claudeCall,
}

// claudeCall steps through Claude's question tabs. A single-choice question takes its digit (or the Other digit, then the
// text and Enter) and moves to the next tab at once. A multi-select takes Enter on each chosen row, which toggles it, then
// Down to the Other row to type into it and to its Submit row; digits would toggle too, but the cursor only moves with
// arrows. A call that is not one single-choice question ends on a review screen, where 1 submits.
func claudeCall(qs []CallQuestion) (string, bool) {
	const down = "\x1b[B"
	if len(qs) < 1 || len(qs) > 4 {
		return "", false
	}
	var b strings.Builder
	for _, q := range qs {
		if q.Options < 1 || q.Options > 8 {
			return "", false
		}
		if !q.Multi {
			switch {
			case len(q.Choose) == 1 && q.Other == "" && q.Choose[0] >= 1 && q.Choose[0] <= q.Options:
				b.WriteString(strconv.Itoa(q.Choose[0]))
			case len(q.Choose) == 0 && q.Other != "":
				b.WriteString(strconv.Itoa(q.Options+1) + q.Other + "\r")
			default:
				return "", false
			}
			continue
		}
		if len(q.Choose) == 0 && q.Other == "" {
			return "", false
		}
		row, prev := 1, 0
		for _, c := range q.Choose {
			if c <= prev || c > q.Options {
				return "", false
			}
			prev = c
			b.WriteString(strings.Repeat(down, c-row) + "\r")
			row = c
		}
		if q.Other != "" {
			b.WriteString(strings.Repeat(down, q.Options+1-row) + q.Other)
			row = q.Options + 1
		}
		b.WriteString(strings.Repeat(down, q.Options+2-row) + "\r")
	}
	if len(qs) > 1 || qs[0].Multi {
		b.WriteString("1")
	}
	return b.String(), true
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
	// Only the command and patch menus were recorded; other approvals (permissions, MCP tools) have other menus.
	tools: map[string]bool{"Bash": true, "apply_patch": true},
}
