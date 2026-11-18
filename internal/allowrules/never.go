package allowrules

import (
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// The never list's groups, as a refusal names them.
const (
	NeverPushMerge   = "pushes and merges"
	NeverGHAPI       = "gh api writes"
	NeverPublish     = "publishes"
	NeverInterpreter = "interpreters and commands that run arbitrary text"
	NeverDelete      = "deletes and history rewrites"
	NeverNetwork     = "network fetches and new URLs"
	NeverSecret      = "secrets and identity"
	NeverOutside     = "writes outside the worktree"
	NeverWiring      = "changes to permissions or Admiral's wiring"
)

// never is one command the never list refuses: every command that starts with prefix, or, when args is set, only
// those that also carry one of args. A rule that could run such a command is refused.
type never struct {
	group  string
	prefix []string
	args   []string
}

func nv(group string, cmds ...string) []never {
	out := make([]never, len(cmds))
	for i, c := range cmds {
		out[i] = never{group: group, prefix: strings.Fields(c)}
	}
	return out
}

var neverList = slices.Concat(
	nv(NeverPushMerge, "git push", "git merge", "git pull", "gh pr merge", "gh repo sync", "gh merge"),
	nv(NeverGHAPI, "gh api", "gh pr close", "gh pr lock", "gh issue close", "gh issue delete", "gh issue lock",
		"gh issue transfer", "gh repo delete", "gh repo edit", "gh repo rename", "gh repo archive",
		"gh repo unarchive", "gh repo deploy-key", "gh secret", "gh variable", "gh ruleset", "gh workflow run",
		"gh workflow enable", "gh workflow disable", "gh run cancel", "gh run delete", "gh cache delete",
		"gh label delete", "gh project delete", "gh project close", "gh gist"),
	nv(NeverPublish, "npm publish", "npm unpublish", "npm deprecate", "npm dist-tag", "yarn publish", "yarn npm publish",
		"pnpm publish", "bun publish", "cargo publish", "cargo yank", "gem push", "twine", "poetry publish",
		"uv publish", "docker push", "podman push", "gh release", "goreleaser", "vsce", "ovsx", "pod trunk", "flit publish",
		"hatch publish", "dotnet nuget push", "nuget push", "mvn deploy", "gradle publish"),
	nv(NeverInterpreter, "bash", "sh", "zsh", "fish", "dash", "ksh", "csh", "tcsh", "python", "python2", "python3",
		"node", "deno", "bun", "ruby", "irb", "perl", "php", "lua", "tclsh", "osascript", "pwsh", "powershell", "awk",
		"gawk", "sed", "eval", "exec", "source", ".", "xargs", "parallel", "env", "sudo", "su", "doas", "nohup",
		"nice", "timeout", "time", "watch", "command", "builtin", "find", "fd", "script", "expect", "tmux", "screen",
		"npx", "bunx", "uvx", "pnpm dlx", "pnpm exec", "npm exec", "yarn dlx", "yarn exec", "uv run", "pipx",
		"poetry run", "bundle exec", "go run", "git -c", "docker run", "docker exec", "podman run", "podman exec",
		"kubectl exec", "claude", "codex", "copilot", "gemini", "ssh"),
	nv(NeverDelete, "rm", "rmdir", "unlink", "shred", "srm", "trash", "git rm", "git clean", "git reset",
		"git restore", "git rebase", "git filter-branch", "git filter-repo", "git reflog", "git update-ref",
		"git replace", "git gc", "git prune", "git stash drop", "git stash clear", "git worktree remove",
		"git worktree prune"),
	[]never{
		{group: NeverDelete, prefix: []string{"git", "branch"}, args: []string{"-d", "-D", "--delete", "-m", "-M", "--move", "-f", "--force"}},
		{group: NeverDelete, prefix: []string{"git", "tag"}, args: []string{"-d", "--delete", "-f", "--force"}},
		{group: NeverDelete, prefix: []string{"git", "commit"}, args: []string{"--amend"}},
		{group: NeverDelete, prefix: []string{"git", "checkout"}, args: []string{"-f", "--force", "--", "."}},
		{group: NeverDelete, prefix: []string{"git", "switch"}, args: []string{"-f", "--force", "--discard-changes", "-C", "--force-create"}},
	},
	nv(NeverNetwork, "curl", "wget", "http", "https", "xh", "nc", "ncat", "netcat", "socat", "telnet", "scp",
		"sftp", "ftp", "rsync", "git clone", "git fetch", "git remote", "git ls-remote", "git submodule",
		"npm install", "npm i", "npm add", "npm update", "yarn add", "yarn install", "pnpm add", "pnpm install",
		"pnpm i", "bun add", "bun install", "pip", "pip3", "uv pip", "uv add", "poetry add", "go get", "go install",
		"cargo install", "cargo add", "gem install", "brew", "apt", "apt-get", "docker pull", "open"),
	nv(NeverSecret, "security", "gh auth", "git config", "git credential", "ssh-keygen", "ssh-add", "gpg", "op",
		"pass", "printenv", "export", "aws", "gcloud", "az", "npm login", "npm token", "npm adduser", "docker login",
		"vault", "keychain", "cat", "head", "tail", "less", "more", "bat", "strings", "xxd", "od", "hexdump",
		"base64", "grep", "rg", "ag", "git send-email"),
	nv(NeverOutside, "cp", "mv", "tee", "ln", "dd", "install", "truncate", "touch", "mkdir", "ditto", "tar", "unzip",
		"zip", "patch"),
	nv(NeverWiring, "chmod", "chown", "chgrp", "chflags", "xattr", "setfacl", "umask", "launchctl", "crontab",
		"defaults", "git hook", "git init", "admiral setup", "admiral service", "admiral config", "admiral fleet",
		"admiral pair", "admiral devices", "admiral hook", "admiral guard", "admiral mcp", "admiral statusline",
		"admiral update", "admiral completion", "codex-pane"),
)

// interpreterStems catch the versioned names of interpreters, such as python3.12 or node22.
var interpreterStems = []string{"python", "node", "ruby", "perl", "php", "lua", "pypy"}

// shellRule is a command rule as a never-list check reads it: the words it names, and whether it covers every
// command that starts with them or only that exact command.
type shellRule struct {
	words  []string
	prefix bool
}

// refuseShell is the never list's reason to refuse r, or "" when it may become an allow rule.
func refuseShell(r shellRule, home string) string {
	if len(r.words) == 0 {
		return "it would allow every command"
	}
	words := slices.Clone(r.words)
	words[0] = path.Base(words[0])
	if strings.Contains(r.words[0], "=") {
		return refusal(NeverInterpreter, "sets the environment for the command")
	}
	for _, stem := range interpreterStems {
		if strings.HasPrefix(words[0], stem) {
			return refusal(NeverInterpreter, words[0])
		}
	}
	for _, n := range neverList {
		if r.prefix {
			// A prefix rule allows everything under it, so it is refused when it reaches any never-listed command.
			if hasPrefix(words, n.prefix) || hasPrefix(n.prefix, words) {
				return refusal(n.group, strings.Join(n.prefix, " "))
			}
			continue
		}
		if hasPrefix(words, n.prefix) && (len(n.args) == 0 || slices.ContainsFunc(words[len(n.prefix):], func(w string) bool { return carries(w, n.args) })) {
			return refusal(n.group, strings.Join(n.prefix, " "))
		}
	}
	for _, w := range r.words[1:] {
		if why := refusePathWord(w, home); why != "" {
			return why
		}
	}
	return ""
}

func refusal(group, what string) string { return "on the never list (" + group + "): " + what }

func hasPrefix(words, prefix []string) bool {
	return len(words) >= len(prefix) && slices.Equal(words[:len(prefix)], prefix)
}

// carries is whether word is one of flags, as itself, with =value, or inside a cluster of short flags.
func carries(word string, flags []string) bool {
	for _, f := range flags {
		switch {
		case word == f, strings.HasPrefix(f, "--") && strings.HasPrefix(word, f+"="):
			return true
		case len(f) == 2 && f[0] == '-' && f[1] != '-' && len(word) > 2 && word[0] == '-' && word[1] != '-' &&
			strings.ContainsRune(word[1:], rune(f[1])):
			return true
		}
	}
	return false
}

var secretMarks = []string{".ssh", ".aws", ".gnupg", ".netrc", ".git-credentials", ".npmrc", ".pypirc", ".kube",
	".docker", ".config/gh", ".config/gcloud", ".azure", "id_rsa", "id_ed25519", "id_ecdsa", ".env", ".pem", ".p12",
	".pfx", ".key", "credential", "secret", "token", "keychain", "password", "passwd"}

var wiringMarks = []string{".claude", ".codex", ".copilot", ".git/hooks", ".git/config", ".gitconfig", ".admiral",
	"admiral.rules", "allow-rules.json", "settings.json", "permissions-config.json", ".bashrc", ".zshrc", ".profile",
	".zprofile", ".bash_profile", ".zshenv", "launchagents"}

// refusePathWord refuses an argument that names a secret or Admiral's or a harness's wiring.
func refusePathWord(w, home string) string {
	lw := strings.ToLower(w)
	for _, m := range secretMarks {
		if strings.Contains(lw, m) {
			return refusal(NeverSecret, w)
		}
	}
	if adm := admiralPath(w, home); adm == admiralOther {
		return refusal(NeverWiring, w)
	} else if adm != notAdmiral {
		return ""
	}
	for _, m := range wiringMarks {
		if strings.Contains(lw, m) {
			return refusal(NeverWiring, w)
		}
	}
	return ""
}

type admiralPlace int

const (
	notAdmiral   admiralPlace = iota
	admiralOwn                // a report, or a ship's inbox: the files a ship may write
	admiralOther              // anything else in Admiral's home, its proposals included
)

func admiralPath(p, home string) admiralPlace {
	if home == "" || !filepath.IsAbs(p) {
		return notAdmiral
	}
	rel, err := filepath.Rel(filepath.Clean(home), filepath.Clean(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return notAdmiral
	}
	parts := strings.Split(rel, "/")
	switch {
	case len(parts) >= 2 && parts[0] == "reports":
		return admiralOwn
	case len(parts) >= 3 && parts[0] == "inbox" && parts[1] != "_admiral":
		return admiralOwn
	}
	return admiralOther
}

// refusePath refuses a file rule's path unless it stays in the worktree or in Admiral's report and inbox paths.
// rel is a path relative to the ship's working directory; abs is an absolute one. Exactly one is set.
func refusePath(rel, abs, home string) string {
	if abs != "" {
		if why := refusePathWord(abs, home); why != "" {
			return why
		}
		if admiralPath(abs, home) == admiralOwn && !strings.Contains(abs, "..") {
			return ""
		}
		return refusal(NeverOutside, abs)
	}
	if rel == "" || strings.HasPrefix(rel, "~") || strings.HasPrefix(rel, "$") || strings.HasPrefix(rel, "/") {
		return refusal(NeverOutside, rel)
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return refusal(NeverOutside, rel)
		}
	}
	if why := refusePathWord(rel, home); why != "" {
		return why
	}
	if rel == "**" || rel == "*" || rel == "./**" || rel == "." || rel == "./" {
		return "it would allow every file"
	}
	return ""
}

// splitWords splits a command into its words the way a shell would, and refuses one that does more than run one
// command: a pipe, a list, a redirect, a substitution or an expansion.
func splitWords(s string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inWord = true
		case c == '"':
			j := i + 1
			for ; j < len(s) && s[j] != '"'; j++ {
				if strings.IndexByte("$`\\", s[j]) >= 0 {
					return nil, false
				}
			}
			if j == len(s) {
				return nil, false
			}
			cur.WriteString(s[i+1 : j])
			i = j
			inWord = true
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case strings.IndexByte(";&|<>()$`\\\n\r{}!#", c) >= 0:
			return nil, false
		default:
			if c < 0x20 || c == 0x7f {
				return nil, false
			}
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, len(words) > 0
}
