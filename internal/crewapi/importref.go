package crewapi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/github"
)

// ImportRef is what the import box's text names: one member, or (Source) a whole crew source.
type ImportRef struct {
	Owner, Repo string
	Name, Ref   string // Name is empty for a source; Ref is empty for the default branch
	Source      bool
}

const githubPrefix = "https://github.com/"

const importForms = "use owner/repo/name, owner/repo/name@ref (a branch, tag or commit), " +
	"a GitHub file URL, or a crew repo URL, such as octocat/admiral-crew/reviewer@v1, " +
	"https://github.com/octocat/admiral-crew/blob/main/crew/reviewer.md or https://github.com/octocat/admiral-crew"

func badImportRef(text string) error {
	return fmt.Errorf("%q is not a crew member to import: %s", text, importForms)
}

var refPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// validImportRef is a branch, tag or commit as one URL segment: ASCII letters, digits and ._/-, never ".." or an empty or "." part.
func validImportRef(ref string) bool {
	if len(ref) > 255 || !refPattern.MatchString(ref) || strings.Contains(ref, "..") || ref[0] == '/' || ref[0] == '-' {
		return false
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || part == "." {
			return false
		}
	}
	return true
}

// ParseImportRef reads a ref or GitHub URL; any other text is an error that names the accepted forms.
func ParseImportRef(text string) (ImportRef, error) {
	text = strings.TrimSpace(text)
	var ref ImportRef
	var ok bool
	switch {
	case strings.HasPrefix(text, githubPrefix):
		ref, ok = parseGitHubURL(strings.TrimPrefix(text, githubPrefix))
	case !strings.Contains(text, "://"):
		ref, ok = parseShortRef(text)
	}
	if !ok {
		return ImportRef{}, badImportRef(text)
	}
	return ref, nil
}

func parseShortRef(text string) (ImportRef, bool) {
	path, ref, hasRef := strings.Cut(text, "@")
	if hasRef && !validImportRef(ref) {
		return ImportRef{}, false
	}
	parts := strings.Split(path, "/")
	if len(parts) != 3 || !github.ValidSegment(parts[0]) || !github.ValidSegment(parts[1]) {
		return ImportRef{}, false
	}
	name := strings.TrimSuffix(parts[2], ".md")
	if !crew.ValidName(name) {
		return ImportRef{}, false
	}
	return ImportRef{Owner: parts[0], Repo: parts[1], Name: name, Ref: ref}, true
}

func parseGitHubURL(path string) (ImportRef, bool) {
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	if len(parts) < 2 {
		return ImportRef{}, false
	}
	owner, repo := parts[0], parts[1]
	if len(parts) == 2 {
		repo = strings.TrimSuffix(repo, ".git")
	}
	if !github.ValidSegment(owner) || !github.ValidSegment(repo) {
		return ImportRef{}, false
	}
	switch {
	case len(parts) == 2:
		return ImportRef{Owner: owner, Repo: repo, Source: true}, true
	case parts[2] == "tree" && len(parts) >= 5 && parts[len(parts)-1] == crewDir:
		return ImportRef{Owner: owner, Repo: repo, Source: true}, validImportRef(strings.Join(parts[3:len(parts)-1], "/"))
	case parts[2] == "blob" && len(parts) >= 6 && parts[len(parts)-2] == crewDir:
		ref := strings.Join(parts[3:len(parts)-2], "/")
		name, isMd := strings.CutSuffix(parts[len(parts)-1], ".md")
		return ImportRef{Owner: owner, Repo: repo, Name: name, Ref: ref}, isMd && crew.ValidName(name) && validImportRef(ref)
	}
	return ImportRef{}, false
}

// ErrNameTaken means a file already stands at the member's name, so nothing was written.
var ErrNameTaken = errors.New("a file with that name is already in the fleet's crew directory")

// WriteImported writes <name>.md into the fleet's crew directory once: a file already there, whatever its bytes, is ErrNameTaken and stays as it is.
func WriteImported(home, key, name, text string) error {
	dir := filepath.Join(home, "fleets", key, crewDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".import-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), filepath.Join(dir, name+".md")); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrNameTaken
		}
		return err
	}
	return nil
}
