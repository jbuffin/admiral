// Package repofinder lists the git checkouts up to two levels below a fleet's
// search roots, then the roots, and matches them to an order by remote (ADR 0009).
package repofinder

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const (
	KindCheckout   = "checkout"
	KindSearchRoot = "search_root"
)

type Entry struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Remotes []string `json:"remotes"` // normalized, see Identity
}

// List reads the disk on every call and runs no git.
func List(roots []string) []Entry {
	out := []Entry{}
	seen := map[string]bool{}
	add := func(p, kind string) {
		if seen[p+"\x00"+kind] {
			return
		}
		seen[p+"\x00"+kind] = true
		out = append(out, Entry{Path: p, Name: filepath.Base(p), Kind: kind, Remotes: Remotes(p)})
	}
	for _, root := range roots {
		root = filepath.Clean(root)
		if isCheckout(root) {
			continue // never search inside a checkout
		}
		for _, c := range subdirs(root) {
			if isCheckout(c) {
				add(c, KindCheckout)
				continue
			}
			for _, g := range subdirs(c) {
				if isCheckout(g) {
					add(g, KindCheckout)
				}
			}
		}
	}
	for _, root := range roots {
		if root = filepath.Clean(root); isDir(root) {
			add(root, KindSearchRoot)
		}
	}
	return out
}

func Contains(entries []Entry, dir string) (string, bool) {
	dir = filepath.Clean(dir)
	for _, e := range entries {
		if e.Path == dir {
			return dir, true
		}
	}
	return dir, false
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func subdirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if p := filepath.Join(dir, e.Name()); isDir(p) {
			out = append(out, p)
		}
	}
	return out
}

func isCheckout(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// A worktree's .git file leads through its gitdir and commondir to the main
// checkout's config.
func Remotes(dir string) []string {
	cfg := configPath(dir)
	if cfg == "" {
		return []string{}
	}
	data, err := os.ReadFile(cfg)
	if err != nil {
		return []string{}
	}
	out := []string{}
	seen := map[string]bool{}
	inRemote := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "["):
			inRemote = strings.HasPrefix(line, "[remote ")
		case inRemote:
			k, v, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(k) != "url" {
				continue
			}
			if id, ok := Identity(strings.Trim(strings.TrimSpace(v), `"`)); ok && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

func configPath(dir string) string {
	dotgit := filepath.Join(dir, ".git")
	st, err := os.Lstat(dotgit)
	if err != nil {
		return ""
	}
	gitdir := dotgit
	if !st.IsDir() {
		raw, err := os.ReadFile(dotgit)
		if err != nil {
			return ""
		}
		rest, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
		if !ok {
			return ""
		}
		gitdir = strings.TrimSpace(rest)
		if !filepath.IsAbs(gitdir) {
			gitdir = filepath.Join(dir, gitdir)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		common := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitdir, common)
		}
		gitdir = common
	}
	return filepath.Join(gitdir, "config")
}

var (
	hostRe = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

var scpLike = regexp.MustCompile(`^(?:[^@/\s]+@)?([^:/\s]+):([^/].*)$`)

// Only the first two path segments count, so a link to an issue or a pull
// request names its repository.
func Identity(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	var host, path string
	if scheme, rest, ok := strings.Cut(raw, "://"); ok {
		if scheme == "" || strings.ContainsAny(scheme, "/ ") {
			return "", false
		}
		// The authority ends at the first "/", and the host is after its last
		// "@". Userinfo may hold an unencoded "/" (user:tok/secret@host), which
		// puts the "@" in the first path segment: then the host follows it.
		auth, p, _ := strings.Cut(rest, "/")
		if !strings.Contains(auth, "@") {
			if seg, after, _ := strings.Cut(p, "/"); strings.Contains(seg, "@") {
				auth, p = seg, after
			}
		}
		if i := strings.LastIndex(auth, "@"); i >= 0 {
			auth = auth[i+1:]
		}
		auth, _, _ = strings.Cut(auth, "?")
		auth, _, _ = strings.Cut(auth, "#")
		if i := strings.LastIndex(auth, ":"); i >= 0 && !strings.HasSuffix(auth, "]") {
			auth = auth[:i]
		}
		host, path = auth, p
	} else if m := scpLike.FindStringSubmatch(raw); m != nil {
		host, path = m[1], m[2]
	} else {
		return "", false
	}
	path, _, _ = strings.Cut(path, "?")
	path, _, _ = strings.Cut(path, "#")
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if host == "" || len(segs) < 2 || segs[0] == "" || segs[1] == "" {
		return "", false
	}
	repo := strings.TrimSuffix(segs[1], ".git")
	// Anything that is not a plain host, owner and repo is not an identity, so
	// no fragment of a credential that was parsed wrongly can come back.
	if !hostRe.MatchString(host) || !nameRe.MatchString(segs[0]) || !nameRe.MatchString(repo) {
		return "", false
	}
	return strings.ToLower(host + "/" + segs[0] + "/" + repo), true
}

var urlLike = regexp.MustCompile("[A-Za-z][A-Za-z0-9+.-]*://[^\\s<>\"'`]+|[\\w.-]+@[\\w.-]+:[\\w./~-]+")

func URLs(text string) []string {
	var out []string
	for _, u := range urlLike.FindAllString(text, -1) {
		u = strings.TrimRight(u, ".,;:!?)]}>")
		if id, ok := Identity(u); ok {
			out = append(out, id)
		}
	}
	return out
}

// CloneName is the folder name a clone of identity id gets, or an error when gh must not be
// asked to clone it: id is host/owner/repo, the host one of hosts, no segment starts with "-"
// (gh would read it as a flag) and the folder name is no dot path.
func CloneName(id string, hosts []string) (string, error) {
	segs := strings.Split(id, "/")
	if len(segs) != 3 {
		return "", fmt.Errorf("%q is not host/owner/repo", id)
	}
	host, owner, repo := segs[0], segs[1], segs[2]
	if !hostRe.MatchString(host) || !nameRe.MatchString(owner) || !nameRe.MatchString(repo) {
		return "", fmt.Errorf("%q is not host/owner/repo", id)
	}
	if !slices.Contains(hosts, strings.ToLower(host)) {
		return "", fmt.Errorf("%s is not a GitHub host, so %s cannot be cloned", host, id)
	}
	for _, seg := range segs {
		if strings.HasPrefix(seg, "-") || seg == "." || seg == ".." {
			return "", fmt.Errorf("%q is not a repository name gh can clone", id)
		}
	}
	// A folder that starts with a dot is never listed as a checkout.
	if strings.HasPrefix(repo, ".") {
		return "", fmt.Errorf("a repository named %q would clone into a hidden folder", repo)
	}
	return repo, nil
}

// CloneRoot is where a fleet's clones go: its clone_root with a leading ~ expanded against home,
// or its first search root when none is set. It is empty when there is no absolute place.
func CloneRoot(cloneRoot string, searchRoots []string, home string) string {
	root := strings.TrimSpace(cloneRoot)
	switch {
	case root == "" && len(searchRoots) > 0:
		root = searchRoots[0]
	case root == "~":
		root = home
	case strings.HasPrefix(root, "~/"):
		root = filepath.Join(home, root[2:])
	}
	if !filepath.IsAbs(root) {
		return ""
	}
	return filepath.Clean(root)
}

// FleetRoots are the places a fleet's projects and checkouts are found: its search roots and
// its clone root, so a clone is always a project of the fleet wherever clone_root points.
func FleetRoots(searchRoots []string, cloneRoot string) []string {
	home, _ := os.UserHomeDir()
	roots := slices.Clone(searchRoots)
	if c := CloneRoot(cloneRoot, nil, home); c != "" && !slices.ContainsFunc(roots, func(r string) bool { return filepath.Clean(r) == c }) {
		roots = append(roots, c)
	}
	return roots
}

// Hint is the repository a launch is for: the order's repo, else the first URL in its text.
func Hint(repo, text string) (string, bool) {
	if id, ok := Identity(repo); ok {
		return id, true
	}
	if urls := URLs(text); len(urls) > 0 {
		return urls[0], true
	}
	return "", false
}
