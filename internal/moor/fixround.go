package moor

// PRProblems is what keeps one open PR from merging that its ship can fix.
type PRProblems struct {
	PR            string   `json:"pr"`
	Conflicts     bool     `json:"conflicts,omitempty"`
	Behind        bool     `json:"behind,omitempty"`
	FailingChecks []string `json:"failing_checks,omitempty"`
}

// ProblemsOf is the red-phase stub: it reports no problem for every status.
func ProblemsOf(url string, st Status) (PRProblems, bool) { return PRProblems{}, false }

// FixRound is the red-phase stub of the whole-ship feedback round Fix PR and Fix stack start.
type FixRound struct {
	PR       string
	Problems []PRProblems
	Feedback string
	Next     WakeState
}

// DecideFixRound is the red-phase stub: it never decides a round.
func DecideFixRound(in WakeInput) (FixRound, bool) { return FixRound{}, false }
