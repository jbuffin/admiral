package skillindex

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// describe reads the description from a SKILL.md's frontmatter: a plain,
// quoted or block scalar on one key. It is for showing, so anything it cannot
// read is "", never an error.
func describe(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return ""
	}
	var parts []string
	block, folded := false, true
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if line == "---" {
			break
		}
		if block {
			if line == "" || line[0] == ' ' || line[0] == '\t' {
				if line != "" || !folded {
					parts = append(parts, strings.TrimSpace(line))
				}
				continue
			}
			break
		}
		rest, ok := strings.CutPrefix(line, "description:")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest != "" && (rest[0] == '>' || rest[0] == '|') {
			block, folded = true, rest[0] == '>'
			continue
		}
		return unquote(rest)
	}
	sep := " "
	if !folded {
		sep = "\n"
	}
	return strings.TrimSpace(strings.Join(parts, sep))
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		inner := s[1 : len(s)-1]
		if s[0] == '\'' {
			return strings.ReplaceAll(inner, "''", "'")
		}
		return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(inner)
	}
	return s
}
