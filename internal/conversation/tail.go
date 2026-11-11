package conversation

import (
	"strings"
	"unicode/utf8"
)

const tailMax = 200

// TailItem reports whether an item can give a ship's tail; a Reader that only feeds Tail keeps just these.
func TailItem(it Item) bool {
	switch it.Kind {
	case KindAssistant, KindToolCall:
		return true
	case KindQuestion:
		return it.Question != nil && it.Question.Pending
	}
	return false
}

// Tail is the one line a ship's row shows from its conversation: the latest pending question, tool call
// or assistant text. A pending question wins only while it is the latest of those, so one an
// interrupted turn left pending cannot pin the tail.
func Tail(items []Item) string {
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		if !TailItem(it) {
			continue
		}
		var text string
		switch it.Kind {
		case KindQuestion:
			text = it.Question.Text
		case KindToolCall:
			if it.Tool != nil {
				text = it.Tool.Summary
				if strings.TrimSpace(text) == "" {
					text = it.Tool.Name
				}
			}
		default:
			text = it.Text
		}
		return oneLine(text)
	}
	return ""
}

func oneLine(s string) string {
	line := ""
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
			break
		}
	}
	if utf8.RuneCountInString(line) > tailMax {
		line = string([]rune(line)[:tailMax])
	}
	return line
}
