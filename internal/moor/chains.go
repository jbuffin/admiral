package moor

// ChainPR is one pull request as chain detection reads it.
type ChainPR struct {
	URL           string
	State         string
	Head          string
	Base          string
	DefaultBranch string
	Stacked       bool // in a GitHub stack
}

// Chains is a stub: the implementer replaces it with the real split.
func Chains(prs []ChainPR) [][]ChainPR { return nil }
