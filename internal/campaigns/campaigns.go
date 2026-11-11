package campaigns

import (
	"errors"

	"github.com/jbuffin/admiral/internal/github"
)

// Status is where a launchable child stands.
type Status string

const (
	Done          Status = "done"
	Ready         Status = "ready"
	Blocked       Status = "blocked"
	ClaimedNoShip Status = "claimed-no-ship"
	Underway      Status = "underway"
)

// Mode is whether a child's ship needs the engineer at hand.
type Mode string

const (
	WithYou    Mode = "with-you"
	WithoutYou Mode = "without-you"
)

// Skill is what a child's ship starts on; Kind is the launch API's skill kind.
type Skill struct {
	Kind string // "commodore", "skill" or "none"
	Name string // "wayfinder" for Kind "skill"
	Args string // the text after the skill name
}

// Override replaces a child's default mode, skill or both; a zero field keeps the default.
type Override struct {
	Mode  Mode
	Skill *Skill
}

// Input is a read tree with what Admiral knows about it.
type Input struct {
	Tree      *github.TreeNode
	Claims    map[string]string   // issue URL to the name of the live ship holding the claim
	Overrides map[string]Override // issue URL to the campaign's override
}

// Node is one issue of the folded tree. A nested parent has Parent set and no
// Status, Mode or Skill.
type Node struct {
	Number   int
	Title    string
	URL      string
	Repo     string
	Closed   bool
	Parent   bool
	Status   Status
	Blockers []github.Blocker // the open blockers, for a blocked child
	Ship     string           // the ship holding the claim, for an underway child
	Mode     Mode
	Skill    Skill
	Children []*Node
}

// Progress counts closed children of all launchable children.
type Progress struct{ Closed, Total int }

// Campaign is the tree folded for the page.
type Campaign struct {
	Root     *Node
	Ready    []*Node // launchable ready children in tree order
	Progress Progress
	Counts   map[Status]int
}

// Build folds the tree and works out every child's status, mode and skill.
func Build(in Input) Campaign { return Campaign{} }

// IssueRef names one issue on one host.
type IssueRef struct {
	Host   string // empty for owner/repo#n, which names no host
	Owner  string
	Repo   string
	Number int
}

// ParseIssueRef reads an issue URL or owner/repo#n; its error is a message to show.
func ParseIssueRef(s string) (IssueRef, error) { return IssueRef{}, errors.New("not implemented") }
