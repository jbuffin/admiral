package github

import (
	"strings"
	"unicode"

	"github.com/jbuffin/admiral/internal/moor"
)

// ExcerptRunes is the longest an order's excerpt gets, before its ellipsis.
const ExcerptRunes = 280

// Excerpt reads text as one short line: whitespace runs become one space, other control characters go, the ends are
// trimmed, and a line past ExcerptRunes is cut on a rune boundary with an ellipsis. Empty when nothing is left.
func Excerpt(text string) string {
	var b strings.Builder
	space := false
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			space = true
		case unicode.IsControl(r):
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	out := b.String()
	if r := []rune(out); len(r) > ExcerptRunes {
		return string(r[:ExcerptRunes]) + "…"
	}
	return out
}

// failingChecks counts the checks the moor poll calls failing, from the rollup's per-state counts.
func (n *prNode) failingChecks() int {
	if n.StatusCheckRollup == nil || n.StatusCheckRollup.Contexts == nil {
		return 0
	}
	total := 0
	for _, c := range n.StatusCheckRollup.Contexts.CheckRuns {
		if moor.FailingCheckState("CheckRun", c.State) {
			total += c.Count
		}
	}
	for _, c := range n.StatusCheckRollup.Contexts.StatusContexts {
		if moor.FailingCheckState("StatusContext", c.State) {
			total += c.Count
		}
	}
	return total
}

// reviewedSincePush is true when the viewer's latest review was submitted on the head commit.
func (n *prNode) reviewedSincePush() bool {
	r := n.ViewerLatestReview
	if r == nil || r.Commit == nil || n.HeadRefOid == "" || r.Commit.Oid != n.HeadRefOid {
		return false
	}
	switch r.State {
	case "APPROVED", "CHANGES_REQUESTED", "COMMENTED":
		return true
	}
	return false
}
