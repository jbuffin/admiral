package launcher

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/skillindex"
)

// CampaignRefFile is where a drafting ship writes the URL of the parent issue it files, in its ship directory.
const CampaignRefFile = "campaign-ref"

// checkDrafting refuses a drafting launch that cannot draft and answers the one installed name that carries the skill.
func checkDrafting(req Request, dock config.Harness, skill string) (string, *Error) {
	if _, err := os.Lstat(filepath.Join(req.Dir, ".git")); err != nil {
		return "", refuse(http.StatusBadRequest, "a drafting ship needs its own worktree, and %s is not a git checkout", req.Dir)
	}
	names := skillindex.Carrying(req.Harness, dock.Environ(), req.Dir, skill)
	switch len(names) {
	case 0:
		return "", refuse(http.StatusBadRequest, "no installed %s skill carries the name %q for %s", req.Harness, skill, req.Dir)
	case 1:
		return names[0], nil
	}
	return "", refuse(http.StatusBadRequest, "more than one installed %s skill carries the name %q: %s", req.Harness, skill, strings.Join(names, ", "))
}

func withCampaignRef(prompt, path string) string {
	return prompt + "\n\nWhen you have filed the parent issue of the campaign, write its URL, and nothing else, to " + path + "."
}
