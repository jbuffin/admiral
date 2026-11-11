package crew

import (
	"bytes"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	keyLine   = regexp.MustCompile(`^([a-z][a-z0-9-]*):(.*)$`)
	itemLine  = regexp.MustCompile(`^ +-( .*)?$`)
	tabIndent = regexp.MustCompile(`^ *\t`)
)

type memberError struct {
	code, field, message string
}

func (e *memberError) Error() string { return e.message }

func (e *memberError) diagnostic(layer, file string) Diagnostic {
	return newDiag(layer, file, e.code, e.field, e.message)
}

func badFrontmatter(message string) *memberError {
	return &memberError{code: "bad-frontmatter", message: message}
}

// frontmatter holds the parsed fields: a value is nil (key with no value), a bool, a string or a []string.
type frontmatter struct {
	keys   []string
	values map[string]any
}

func (f frontmatter) get(key string) any { return f.values[key] }

func trimBlank(s string) string { return strings.Trim(s, " \t") }

func isQuote(s string) bool { return s != "" && (s[0] == '"' || s[0] == '\'') }

func parseQuoted(text string) (string, string, *memberError) {
	quote := text[0]
	var out strings.Builder
	for i := 1; i < len(text); {
		c := text[i]
		switch {
		case quote == '"' && c == '\\' && i+1 < len(text) && (text[i+1] == '"' || text[i+1] == '\\'):
			out.WriteByte(text[i+1])
			i += 2
		case quote == '\'' && c == '\'' && i+1 < len(text) && text[i+1] == '\'':
			out.WriteByte('\'')
			i += 2
		case c == quote:
			return out.String(), text[i+1:], nil
		default:
			out.WriteByte(c)
			i++
		}
	}
	return "", "", badFrontmatter("unclosed quote")
}

func parseScalar(text string) (string, *memberError) {
	text = trimBlank(text)
	if !isQuote(text) {
		return text, nil
	}
	value, rest, err := parseQuoted(text)
	if err != nil {
		return "", err
	}
	if trimBlank(rest) != "" {
		return "", badFrontmatter("text after a closing quote")
	}
	return value, nil
}

func parseItem(text string) (string, *memberError) {
	text = trimBlank(text)
	if text == "" {
		return "", badFrontmatter("empty list item")
	}
	if text[0] == '[' {
		return "", badFrontmatter("nested list")
	}
	return parseScalar(text)
}

func parseFlowList(text string) ([]string, *memberError) {
	if !strings.HasSuffix(text, "]") {
		return nil, badFrontmatter("unclosed flow list")
	}
	inner := text[1 : len(text)-1]
	items := []string{}
	if trimBlank(inner) == "" {
		return items, nil
	}
	for _, piece := range strings.Split(inner, ",") {
		item, err := parseItem(piece)
		if err != nil {
			return nil, err
		}
		if isQuote(trimBlank(piece)) && strings.ContainsAny(item, "[]") {
			return nil, badFrontmatter("quoted list item holds a bracket")
		}
		items = append(items, item)
	}
	return items, nil
}

func parseValue(text string) (any, *memberError) {
	text = trimBlank(text)
	switch {
	case text == "":
		return nil, nil
	case text[0] == '[':
		return parseFlowList(text)
	case text == "true":
		return true, nil
	case text == "false":
		return false, nil
	}
	return parseScalar(text)
}

func parseFrontmatter(lines []string) (frontmatter, *memberError) {
	fm := frontmatter{values: map[string]any{}}
	openKey := "" // the key directly above, while it holds no value yet or only block list items
	for _, line := range lines {
		if trimBlank(line) == "" || strings.HasPrefix(strings.TrimLeft(line, " "), "#") {
			continue
		}
		if tabIndent.MatchString(line) {
			return fm, badFrontmatter("tab in indentation")
		}
		if m := itemLine.FindStringSubmatch(line); m != nil {
			if openKey == "" {
				return fm, badFrontmatter("list item without an empty key above it")
			}
			item, err := parseItem(m[1])
			if err != nil {
				return fm, err
			}
			list, _ := fm.values[openKey].([]string)
			fm.values[openKey] = append(list, item)
			continue
		}
		m := keyLine.FindStringSubmatch(line)
		if m == nil {
			return fm, badFrontmatter("not a key or a list item")
		}
		name := m[1]
		if _, dup := fm.values[name]; dup {
			return fm, badFrontmatter("repeated key " + name)
		}
		value, err := parseValue(m[2])
		if err != nil {
			return fm, err
		}
		fm.keys = append(fm.keys, name)
		fm.values[name] = value
		openKey = ""
		if value == nil {
			openKey = name
		}
	}
	return fm, nil
}

func parseMember(raw []byte) (frontmatter, string, *memberError) {
	var none frontmatter
	if !utf8.Valid(raw) {
		return none, "", badFrontmatter("not valid UTF-8")
	}
	if bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
		return none, "", badFrontmatter("starts with a byte order mark")
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if lines[0] != "---" {
		return none, "", badFrontmatter("line 1 is not ---")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return none, "", badFrontmatter("no closing ---")
	}
	fm, err := parseFrontmatter(lines[1:end])
	if err != nil {
		return none, "", err
	}
	body := lines[end+1:]
	for len(body) > 0 && trimBlank(body[0]) == "" {
		body = body[1:]
	}
	for len(body) > 0 && trimBlank(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	return fm, strings.Join(body, "\n"), nil
}
