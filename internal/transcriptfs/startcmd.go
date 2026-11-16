package transcriptfs

import (
	"regexp"
	"strings"
)

// splitStart splits a pane's start command into its arguments the way a shell would: tmux shows the argv
// single-quoted when it started one, and plain when it started a string.
func splitStart(cmd string) []string {
	var (
		args  []string
		cur   strings.Builder
		in    bool // a word is open, even an empty quoted one
		quote rune
	)
	flush := func() {
		if in {
			args = append(args, cur.String())
			cur.Reset()
			in = false
		}
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case quote == '"':
			switch {
			case c == '"':
				quote = 0
			case c == '\\' && i+1 < len(rs) && strings.ContainsRune(`"\$`+"`", rs[i+1]):
				i++
				cur.WriteRune(rs[i])
			default:
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			in = true
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			cur.WriteRune(c)
			in = true
		}
	}
	flush()
	return args
}

// Flags whose next argument is an order's text, so it is not read as one of ours.
var takesOrder = map[string]bool{"-i": true, "--interactive": true, "-p": true, "--prompt": true}

// StartFlag finds a flag Admiral put in the start command: before the first "--", and never inside an order's text.
func StartFlag(cmd, name string) (value string, present bool) {
	args := splitStart(cmd)
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if takesOrder[a] {
			i++
			continue
		}
		if a == name {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

var sessionUUID = regexp.MustCompile(`^[0-9a-f-]{36}$`)

// StartedSession is the session id the launch gave the harness, read from the start command the pane cannot rewrite.
// A resumed Claude ship keeps its conversation id, so --resume names it as --session-id does; Copilot reads --session-id only.
func StartedSession(harness, startCmd string) (id string, ok bool) {
	flags := []string{"--session-id"}
	if harness == "claude" {
		flags = append(flags, "--resume")
	}
	for _, f := range flags {
		if v, present := StartFlag(startCmd, f); present && sessionUUID.MatchString(v) {
			return v, true
		}
	}
	return "", false
}
