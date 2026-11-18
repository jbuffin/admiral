package allowrules

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jbuffin/admiral/internal/permlog"
	"github.com/jbuffin/admiral/internal/standing"
)

// Prompt is a logged prompt with what the log can't say about the launch it came in.
type Prompt struct {
	permlog.Record
	Restricted bool
	// Tier is the trigger tier of the event that launched the ship; zero for a launch the engineer made.
	Tier standing.Tier
}

// Candidate is a rule worth proposing to a fleet, with the prompts it came from.
type Candidate struct {
	Fleet   string
	Rule    Rule
	Prompts []permlog.Record
}

// Count is how many prompts the rule would have answered.
func (c Candidate) Count() int { return len(c.Prompts) }

// Refusal is a prompt, or one rule drawn from it, that will not be proposed, and why.
type Refusal struct {
	Prompt permlog.Record
	Rule   Rule // empty when the prompt gave no rule
	Reason string
}

// Candidates turns logged prompts into the allow rules worth proposing, in the order their first prompt came.
// fleets holds each fleet's rules and skips; home is Admiral's home, whose report and inbox paths a ship may write.
// Every prompt that yields no candidate comes back as a refusal with its reason.
func Candidates(prompts []Prompt, fleets map[string]Fleet, home string) ([]Candidate, []Refusal) {
	var out []Candidate
	var refused []Refusal
	seen := map[[2]string]bool{}
	for _, p := range prompts {
		rec := p.Record
		key := [2]string{rec.Ship, rec.ID}
		if seen[key] {
			continue
		}
		seen[key] = true
		if why := untrusted(p); why != "" {
			refused = append(refused, Refusal{Prompt: rec, Reason: why})
			continue
		}
		rules, why := rulesFor(rec, home)
		if why != "" {
			refused = append(refused, Refusal{Prompt: rec, Reason: why})
			continue
		}
		for _, r := range rules {
			if why := r.reason; why != "" {
				refused = append(refused, Refusal{Prompt: rec, Rule: r.Rule, Reason: why})
				continue
			}
			if err := r.Check(); err != nil {
				refused = append(refused, Refusal{Prompt: rec, Rule: r.Rule, Reason: err.Error()})
				continue
			}
			fl := fleets[rec.Fleet]
			switch {
			case slices.ContainsFunc(fl.Rules, r.Same):
				refused = append(refused, Refusal{Prompt: rec, Rule: r.Rule, Reason: "already allowed"})
				continue
			case slices.ContainsFunc(fl.Skipped, r.Same):
				refused = append(refused, Refusal{Prompt: rec, Rule: r.Rule, Reason: "the engineer skipped it"})
				continue
			}
			i := slices.IndexFunc(out, func(c Candidate) bool { return c.Fleet == rec.Fleet && c.Rule.Same(r.Rule) })
			if i < 0 {
				out = append(out, Candidate{Fleet: rec.Fleet, Rule: r.Rule})
				i = len(out) - 1
			}
			out[i].Prompts = append(out[i].Prompts, rec)
		}
	}
	return out, refused
}

func untrusted(p Prompt) string {
	switch {
	case p.By == permlog.Timeout:
		return "the reviewer timed out"
	case p.Answer == permlog.Deny:
		return "it was denied"
	case p.Answer != permlog.Allow:
		return "it was never allowed"
	case p.By != permlog.Person:
		return "a person did not allow it by hand"
	case p.Restricted:
		return "it came from a restricted ship"
	case p.Tier >= standing.T2:
		return "its ship was launched by a T2–T4 event"
	case p.Fleet == "":
		return "the log names no fleet"
	}
	return ""
}

// drawn is one rule drawn from a prompt, or why the never list refuses it.
type drawn struct {
	Rule
	reason string
}

// rulesFor is the narrowest rules the prompt's harness itself offers for it, or why it offers none.
func rulesFor(rec permlog.Record, home string) ([]drawn, string) {
	switch rec.Harness {
	case "claude":
		if rec.Repo == "" {
			return nil, "the log names no repo"
		}
		return claudeRules(rec, home)
	case "copilot":
		if rec.Repo == "" {
			return nil, "the log names no repo"
		}
		return copilotRules(rec, home)
	case "codex":
		return codexRule(rec, home)
	}
	return nil, "no allow rules for harness " + rec.Harness
}

func claudeRules(rec permlog.Record, home string) ([]drawn, string) {
	var sugs []struct {
		Type     string `json:"type"`
		Behavior string `json:"behavior"`
		Rules    []struct {
			Tool    string `json:"toolName"`
			Content string `json:"ruleContent"`
		} `json:"rules"`
	}
	if len(rec.Call.Suggestions) == 0 || json.Unmarshal(rec.Call.Suggestions, &sugs) != nil {
		return nil, "Claude Code offered no rule"
	}
	var out []drawn
	for _, s := range sugs {
		if s.Type != "addRules" || s.Behavior != "allow" {
			continue
		}
		for _, r := range s.Rules {
			text := r.Tool
			if r.Content != "" {
				text += "(" + r.Content + ")"
			}
			out = append(out, drawn{
				Rule:   Rule{Harness: "claude", Repo: rec.Repo, Rule: text},
				reason: refuseClaude(r.Tool, r.Content, home),
			})
		}
	}
	if len(out) == 0 {
		return nil, "Claude Code offered no rule"
	}
	return out, ""
}

func refuseClaude(tool, content, home string) string {
	switch {
	case tool == "WebFetch" || tool == "WebSearch":
		return refusal(NeverNetwork, tool)
	case strings.HasPrefix(tool, "mcp__"):
		return "an MCP tool's effects can't be checked against the never list"
	case content == "":
		return "it would allow every " + tool + " call"
	case tool == "Bash":
		r, ok := claudeShell(content)
		if !ok {
			return "Admiral can't read the rule's command: " + content
		}
		return refuseShell(r, home)
	case tool == "Read" || tool == "Edit" || tool == "Write" || tool == "MultiEdit" || tool == "NotebookEdit":
		// Claude Code reads //path as absolute, /path as relative to the settings file, and a bare path as relative.
		switch {
		case strings.HasPrefix(content, "//"):
			return refusePath("", content[1:], home)
		case strings.HasPrefix(content, "/"):
			return refusal(NeverOutside, content)
		}
		return refusePath(content, "", home)
	}
	return "no narrow rule form for " + tool
}

// claudeShell reads a Bash rule's content: "cmd:*" and "cmd *" allow every command that starts with cmd.
func claudeShell(content string) (shellRule, bool) {
	prefix := false
	for _, suffix := range []string{":*", " *"} {
		if s, ok := strings.CutSuffix(content, suffix); ok {
			content, prefix = s, true
			break
		}
	}
	if strings.ContainsAny(content, "*?[") {
		return shellRule{}, false
	}
	words, ok := splitWords(content)
	return shellRule{words: words, prefix: prefix}, ok
}

func copilotRules(rec permlog.Record, home string) ([]drawn, string) {
	c := rec.Call
	switch c.Kind {
	case "shell":
		if len(c.Identifiers) == 0 {
			return nil, "Copilot offered no command identifier"
		}
		var out []drawn
		for _, id := range c.Identifiers {
			d := drawn{Rule: Rule{Harness: "copilot", Repo: rec.Repo, Rule: "shell(" + id + ")"}}
			// Copilot's shell(id) allows every command under id, so the never list judges id as a prefix.
			words, ok := splitWords(id)
			switch {
			case !ok || strings.ContainsAny(id, ":*()"):
				d.reason = "Admiral can't read the identifier: " + id
			default:
				d.reason = refuseShell(shellRule{words: words, prefix: true}, home)
			}
			out = append(out, d)
		}
		return out, ""
	case "write":
		if len(c.Paths) != 1 || !path.IsAbs(c.Paths[0]) {
			// A relative write(name) would match that name in every directory.
			return nil, "Copilot named no single absolute file"
		}
		p := c.Paths[0]
		return []drawn{{Rule: Rule{Harness: "copilot", Repo: rec.Repo, Rule: "write(" + p + ")"},
			reason: refusePath("", p, home)}}, ""
	case "url":
		return nil, refusal(NeverNetwork, strings.Join(c.URLs, " "))
	case "mcp":
		return nil, "an MCP tool's effects can't be checked against the never list"
	}
	return nil, "no narrow rule form for a Copilot " + c.Kind + " request"
}

var plainWord = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var programWord = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+-]*$`)

// codexRule is the command's argv up to its first argument that names a path or a value, as a prefix rule.
func codexRule(rec permlog.Record, home string) ([]drawn, string) {
	if rec.Call.Kind != "shell" {
		return nil, "Codex rules cover commands only"
	}
	words, ok := splitWords(rec.Call.Command)
	if ok && len(words) == 3 && slices.Contains([]string{"bash", "sh", "zsh"}, path.Base(words[0])) &&
		(words[1] == "-c" || words[1] == "-lc") {
		words, ok = splitWords(words[2])
	}
	if !ok {
		return nil, "it runs more than one plain command"
	}
	if !programWord.MatchString(words[0]) {
		return nil, "the command names its program by path: " + words[0]
	}
	n := 1
	for n < len(words) && plainWord.MatchString(words[n]) {
		n++
	}
	pattern := slices.Clone(words[:n])
	return []drawn{{Rule: Rule{Harness: "codex", Pattern: pattern},
		reason: refuseShell(shellRule{words: pattern, prefix: true}, home)}}, ""
}
