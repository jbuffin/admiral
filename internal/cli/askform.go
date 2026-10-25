package cli

import (
	"encoding/json"
	"strings"
)

// askForm turns the line form of a question into the JSON `chat ask` reads. Claude
// Code's command parser refuses a heredoc whose body has a { and a ", so under
// dontAsk a JSON body never matches the allow rule; this form has neither.
//
//	title: Where should this order go?
//	question: panel | Which panel?
//	multi: false
//	option: Freeform | your own list
//	option: Work queue
//
// A body that starts with { is JSON and passes through. The result is checked
// where JSON is, so nothing here validates ids, counts or lengths.
func askForm(b []byte) ([]byte, error) {
	if t := strings.TrimSpace(string(b)); t == "" || t[0] == '{' {
		return b, nil
	}
	type option struct {
		Label       string `json:"label"`
		Description string `json:"description,omitempty"`
	}
	type question struct {
		ID      string   `json:"id"`
		Text    string   `json:"text"`
		Multi   bool     `json:"multi"`
		Options []option `json:"options"`
	}
	var title string
	qs := []question{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		key, val, ok := strings.Cut(line, ":")
		if line == "" {
			continue
		}
		if !ok {
			return nil, usagef("the question form has a line without a key: %q", line)
		}
		key, val = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(val)
		first, rest, _ := strings.Cut(val, "|")
		first, rest = strings.TrimSpace(first), strings.TrimSpace(rest)
		if key != "title" && key != "question" && len(qs) == 0 {
			return nil, usagef("the question form has %q before any question: line", key)
		}
		switch key {
		case "title":
			title = val
		case "question":
			qs = append(qs, question{ID: first, Text: rest, Options: []option{}})
		case "multi":
			qs[len(qs)-1].Multi = strings.EqualFold(val, "true")
		case "option":
			q := &qs[len(qs)-1]
			q.Options = append(q.Options, option{Label: first, Description: rest})
		default:
			return nil, usagef("the question form has an unknown key %q (title, question, multi, option)", key)
		}
	}
	return json.Marshal(map[string]any{"title": title, "questions": qs})
}
