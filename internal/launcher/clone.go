package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/repofinder"
)

const cloneTimeout = 10 * time.Minute

// CloneTarget is where a launch for the hinted repo would clone, ok false when gh cannot clone it here.
func CloneTarget(cfg *config.Config, fleet config.Fleet, repo, text string) (id, path string, ok bool) {
	id, ok = repofinder.Hint(repo, text)
	if !ok {
		return "", "", false
	}
	name, err := repofinder.CloneName(id, GitHubHosts(cfg, fleet))
	if err != nil {
		return "", "", false
	}
	home, _ := os.UserHomeDir()
	root := repofinder.CloneRoot(fleet.CloneRoot, fleet.SearchRoots, home)
	if root == "" {
		return "", "", false
	}
	return id, filepath.Join(root, name), true
}

// GitHubHosts are github.com and the host of each GitHub source the fleet has.
func GitHubHosts(cfg *config.Config, fleet config.Fleet) []string {
	hosts := []string{github.DefaultHost}
	for _, s := range cfg.SourceList {
		if s.Plugin != "github" || s.Fleet != fleet.Key {
			continue
		}
		var set struct {
			Host string `json:"host"`
		}
		if json.Unmarshal(s.Settings, &set) == nil {
			if h := strings.ToLower(strings.TrimSpace(set.Host)); h != "" && !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
	}
	return hosts
}

// CloneCheck is what a clone launch is about to do, once CheckClone has accepted it.
type CloneCheck struct {
	Fleet  config.Fleet
	Hint   string
	Target string
}

// CheckClone runs every refusal of Clone that needs no clone: the repo, its host, the form's repo, an existing checkout and a place to clone into.
func CheckClone(cfg *config.Config, req Request, repo, text string) (CloneCheck, *Error) {
	fleet, ok := cfg.Fleet(req.Fleet)
	if !ok {
		return CloneCheck{}, refuse(http.StatusNotFound, "fleet %q not found", req.Fleet)
	}
	if _, ok := fleet.Harness(req.Harness); !ok {
		return CloneCheck{}, refuse(http.StatusBadRequest, "fleet %s has no %s dock", fleet.Name, req.Harness)
	}
	if !fleet.CanLaunch() {
		return CloneCheck{}, refuse(http.StatusBadRequest, "fleet %s has no search roots, so nothing can be launched from it", fleet.Name)
	}
	hint, ok := repofinder.Hint(repo, text)
	if !ok {
		return CloneCheck{}, refuse(http.StatusBadRequest, "no repository to clone: the order names none")
	}
	if _, err := repofinder.CloneName(hint, GitHubHosts(cfg, fleet)); err != nil {
		return CloneCheck{}, refuse(http.StatusBadRequest, "%s", err)
	}
	if want := strings.ToLower(strings.TrimSpace(req.CloneRepo)); want != "" && want != hint {
		return CloneCheck{}, refuse(http.StatusConflict, "the order's repo changed to %s; reopen the form", hint)
	}
	for _, e := range repofinder.List(repofinder.FleetRoots(fleet.SearchRoots, fleet.CloneRoot)) {
		if slices.Contains(e.Remotes, hint) {
			return CloneCheck{}, refuse(http.StatusConflict, "%s already has a checkout, %s: launch there", hint, e.Path)
		}
	}
	_, target, ok := CloneTarget(cfg, fleet, repo, text)
	if !ok {
		return CloneCheck{}, refuse(http.StatusBadRequest, "fleet %s has no place to clone into", fleet.Name)
	}
	return CloneCheck{Fleet: fleet, Hint: hint, Target: target}, nil
}

// Clone runs outside the launch lock; the no-replace rename keeps an existing folder untouched and lets one of two racing clones win.
func (l *Launcher) Clone(ctx context.Context, req Request, repo, text string) (string, error) {
	chk, lerr := CheckClone(l.Config(), req, repo, text)
	if lerr != nil {
		return "", lerr
	}
	fleet, hint, target := chk.Fleet, chk.Hint, chk.Target
	root := filepath.Dir(target)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", refuse(http.StatusInternalServerError, "could not create %s: %v", root, err)
	}
	if _, err := os.Lstat(target); err == nil {
		return "", refuse(http.StatusConflict, "%s already exists, so %s was not cloned there", target, hint)
	}
	// Only a folder Admiral made is ever deleted: gh clones into a child of it.
	tmp, err := os.MkdirTemp(root, ".admiral-clone-")
	if err != nil {
		return "", refuse(http.StatusInternalServerError, "could not create a folder in %s: %v", root, err)
	}
	defer os.RemoveAll(tmp)
	child := filepath.Join(tmp, filepath.Base(target))
	if msg := runClone(ctx, fleet, hint, child); msg != "" {
		return "", refuse(http.StatusBadGateway, "%s", msg)
	}
	// The rename refuses a target that appeared while gh ran, so nothing is ever overwritten.
	if err := renameNoReplace(child, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", refuse(http.StatusConflict, "%s already exists, so %s was not cloned there", target, hint)
		}
		return "", refuse(http.StatusInternalServerError, "could not move the clone into %s: %v", target, err)
	}
	return target, nil
}

// runClone runs gh and returns why it failed, on one line, or "" for success.
func runClone(ctx context.Context, fleet config.Fleet, id, dir string) string {
	env := append(fleet.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	bin, err := github.FindGh(env)
	if err != nil {
		return "gh was not found on PATH, so " + id + " cannot be cloned"
	}
	tctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, bin, "repo", "clone", id, dir)
	cmd.Env = env
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	if err == nil {
		return ""
	}
	switch {
	case ctx.Err() != nil:
		return "the clone of " + id + " was cancelled"
	case tctx.Err() != nil:
		return "gh took longer than " + cloneTimeout.String() + " to clone " + id
	}
	if msg := scrubStderr(errOut.String()); msg != "" {
		return "gh repo clone " + id + " failed: " + msg
	}
	return "gh repo clone " + id + " failed: " + err.Error()
}

var userinfo = regexp.MustCompile(`://[^\s@]*@`)

// scrubStderr is gh's stderr on one line, with the credentials of any URL in it hidden.
func scrubStderr(s string) string {
	return userinfo.ReplaceAllString(strings.Join(strings.Fields(s), " "), "://***@")
}
