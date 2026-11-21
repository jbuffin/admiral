package ships

import "github.com/jbuffin/admiral/internal/moor"

// wakeOwnPRLine is the sentence that names the one PR the engineer's message means; it is there only with exactly one open PR the ship opened.
const wakeOwnPRLine = "Unless the engineer's message names another pull request, it means "

// wakeWaitLine ends the prompt of a wake that has no resume text and no message from the engineer.
const wakeWaitLine = "Wait for the engineer's message and do nothing until it arrives."

// wakeContextInput is everything the ship-context document is built from. Repo, Branch and Result arrive raw
// and the document keeps only what Admiral's own vocabulary allows.
type wakeContextInput struct {
	Rec       moor.Record
	Round     int // the round the ship is woken for; rounds count from 0
	Repo      string
	Branch    string // "" when git could not tell
	PRs       []moor.PR
	Rounds    []moor.Round // the earlier rounds, oldest first
	Result    string       // the last report's result as the report gave it
	OrderFile string       // where the order's text is written, "" when it is not
}

// wakeContextText is the ship-context document of one wake.
func wakeContextText(in wakeContextInput) string { return "" }
