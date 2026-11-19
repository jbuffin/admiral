package ships

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jbuffin/admiral/internal/moor"
)

const wakePRsHead = "\n\n## Your pull requests\n\nYou opened these pull requests and they are still open. Check them before anything else:\n\n"

const wakePRsTail = "\n\nFor each one, run `gh pr view <url> --json headRefName,baseRefName,mergeable,mergeStateStatus,statusCheckRollup`. " +
	"If it is behind or conflicts with its base, run `git fetch origin`, check out its head branch and rebase that branch onto `origin/<baseRefName>`, never local main. " +
	"Resolve any conflicts, fix any failing checks, re-run the tests and push the PR branch."

// wakePRsSection is what a hand wake appends to its prompt: the ship's own open PRs and the problems Admiral last polled
// on them, or "" when it has none.
func (s *Service) wakePRsSection(rec moor.Record) string {
	s.mooredMu.Lock()
	prs := s.prsOf(rec)
	s.mooredMu.Unlock()
	return wakePRsText(prs)
}

// wakePRsText carries URLs and Admiral's fixed vocabulary only, never text GitHub or the ship supplied.
func wakePRsText(prs []moor.PR) string {
	var lines []string
	for _, pr := range prs {
		if pr.Reviewing || pr.State != "OPEN" || !plainPRURL(pr.URL) {
			continue
		}
		line := "- " + pr.URL
		if p := wakePRProblems(pr); len(p) > 0 {
			line += ": " + strings.Join(p, "; ")
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return wakePRsHead + strings.Join(lines, "\n") + wakePRsTail
}

// githubPRURL is GitHub's own owner, repo and number charset, so free text in a ship's report cannot reach the prompt.
var githubPRURL = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9-]+/[A-Za-z0-9._-]+/pull/[0-9]+$`)

func plainPRURL(u string) bool { return githubPRURL.MatchString(u) }

// wakePRReasons are Admiral's own merge reason codes; true means the wake line lists the code, false that its own fields already word it.
var wakePRReasons = map[string]bool{
	"conflicts": false, "behind": false, "checks_failing": false,
	"draft": true, "checks_pending": true, "changes_requested": true, "review_required": true, "unknown": true, "held": true,
}

func wakePRProblems(pr moor.PR) []string {
	var out []string
	if pr.Conflicts || pr.ConflictsWithBase != "" {
		out = append(out, "conflicts with its base")
	}
	if pr.Behind {
		out = append(out, "behind its base")
	}
	if n := len(pr.FailingChecks); n == 1 {
		out = append(out, "1 of its own CI checks failing")
	} else if n > 1 {
		out = append(out, fmt.Sprintf("%d of its own CI checks failing", n))
	}
	var rest []string
	for _, r := range pr.MergeReasons {
		if wakePRReasons[r] {
			rest = append(rest, r)
		}
	}
	if len(rest) > 0 {
		out = append(out, "also blocked by: "+strings.Join(rest, ", "))
	}
	return out
}
