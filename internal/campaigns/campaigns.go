// Package campaigns works out, from an issue tree GitHub holds, what a campaign's page shows: each
// child's status (done, ready, blocked, claimed with no ship, underway), the ready set in sub-issue
// order, progress, and the mode and starting skill a child launches with unless the engineer
// overrode them. It reads nothing and writes nothing; the tree, the live claims and the overrides
// come in as input.
package campaigns

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

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
	Number     int
	Title      string
	URL        string
	Repo       string
	Closed     bool
	Parent     bool
	Unreadable int // sub-issues GitHub counts that couldn't be read, so the children shown may be incomplete
	Status     Status
	Blockers   []github.Blocker // the open blockers, for a blocked child
	Ship       string           // the ship holding the claim, for an underway child
	Mode       Mode
	Skill      Skill
	Labels     []string
	// Overridden is set for a child whose campaign override applies; DefaultMode and DefaultSkill are what
	// ADR 0027's table gives it without one.
	Overridden   bool
	DefaultMode  Mode
	DefaultSkill Skill
	Children     []*Node
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
func Build(in Input) Campaign {
	c := Campaign{Counts: map[Status]int{}}
	if in.Tree == nil {
		return c
	}
	b := &builder{in: in, c: &c}
	c.Root = b.fold(in.Tree, nil, in.Tree, true)
	return c
}

type builder struct {
	in Input
	c  *Campaign
}

// fold builds the node for n. above holds the open blockers of its open ancestors, parent is its nearest parent.
func (b *builder) fold(n *github.TreeNode, above []github.Blocker, parent *github.TreeNode, root bool) *Node {
	out := &Node{Number: n.Number, Title: n.Title, URL: n.URL, Repo: n.Repo, Closed: n.Closed, Labels: append([]string{}, n.Labels...)}
	if root || n.Parent || len(n.Children) > 0 {
		out.Parent, out.Unreadable = true, n.Unreadable
		below := above
		// The root's own blockers are about the campaign, not its children; a nested parent's still block everything under it.
		if !n.Closed && !root {
			below = mergeBlockers(openBlockers(n.BlockedBy), above)
		}
		for _, ch := range n.Children {
			out.Children = append(out.Children, b.fold(ch, below, n, false))
		}
		return out
	}
	b.c.Progress.Total++
	switch ship, held := b.in.Claims[n.URL]; {
	case n.Closed:
		out.Status = Done
		b.c.Progress.Closed++
	case held:
		out.Status, out.Ship = Underway, ship
	default:
		if open := mergeBlockers(openBlockers(n.BlockedBy), above); len(open) > 0 {
			out.Status, out.Blockers = Blocked, open
		} else if len(n.Assignees) > 0 {
			out.Status = ClaimedNoShip
		} else {
			out.Status = Ready
		}
	}
	b.c.Counts[out.Status]++
	out.Mode, out.Skill = defaults(n, parent)
	out.DefaultMode, out.DefaultSkill = out.Mode, out.Skill
	if o, ok := b.in.Overrides[n.URL]; ok {
		if o.Mode != "" {
			out.Mode = o.Mode
		}
		if o.Skill != nil {
			out.Skill = *o.Skill
		}
		out.Overridden = o.Mode != "" || o.Skill != nil
	}
	if out.Status == Ready {
		b.c.Ready = append(b.c.Ready, out)
	}
	return out
}

func openBlockers(bs []github.Blocker) []github.Blocker {
	var out []github.Blocker
	for _, b := range bs {
		if !b.Closed {
			out = append(out, b)
		}
	}
	return out
}

func mergeBlockers(own, inherited []github.Blocker) []github.Blocker {
	out := append([]github.Blocker(nil), own...)
	for _, b := range inherited {
		dup := false
		for _, o := range out {
			dup = dup || o.URL == b.URL
		}
		if !dup {
			out = append(out, b)
		}
	}
	return out
}

// defaults is ADR 0027's table. A wayfinder label wins over a triage label; a child matching no row
// is With you with no skill, the choice that starts nothing unattended.
func defaults(n, parent *github.TreeNode) (Mode, Skill) {
	has := func(l string) bool {
		for _, x := range n.Labels {
			if x == l {
				return true
			}
		}
		return false
	}
	wayfinder := Skill{Kind: "skill", Name: "wayfinder"}
	withMap := wayfinder
	withMap.Args = parent.URL + " " + n.URL
	switch {
	case has("wayfinder:grilling"), has("wayfinder:prototype"):
		return WithYou, withMap
	case has("wayfinder:research"):
		return WithoutYou, withMap
	case has("wayfinder:task"):
		if has("needs-human-action") {
			return WithYou, wayfinder
		}
		return WithoutYou, wayfinder
	case has("ready-for-human"), has("needs-triage"):
		return WithYou, Skill{Kind: "none"}
	case has("ready-for-agent"):
		return WithoutYou, Skill{Kind: "commodore"}
	}
	return WithYou, Skill{Kind: "none"}
}

// IssueRef names one issue on one host.
type IssueRef struct {
	Host   string // empty for owner/repo#n, which names no host
	Owner  string
	Repo   string
	Number int
}

// ParseIssueRef reads an issue URL or owner/repo#n; its error is a message to show.
func ParseIssueRef(s string) (IssueRef, error) {
	s = strings.TrimSpace(s)
	if m := issueURL.FindStringSubmatch(s); m != nil {
		return ref(m[1], m[2], m[3], m[4])
	}
	if m := shortRef.FindStringSubmatch(s); m != nil {
		return ref("", m[1], m[2], m[3])
	}
	if pullURL.MatchString(s) {
		return IssueRef{}, errors.New("That is a pull request. Paste the campaign's issue instead")
	}
	return IssueRef{}, errors.New("Paste an issue URL such as https://github.com/owner/repo/issues/12, or owner/repo#12")
}

const name = `[A-Za-z0-9_.-]+`

var (
	issueURL = regexp.MustCompile(`^https?://([A-Za-z0-9.-]+(?::\d+)?)/(` + name + `)/(` + name + `)/issues/(\d{1,9})$`)
	pullURL  = regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(?::\d+)?/` + name + `/` + name + `/pull/\d+`)
	shortRef = regexp.MustCompile(`^(` + name + `)/(` + name + `)#(\d{1,9})$`)
)

func ref(host, owner, repo, num string) (IssueRef, error) {
	n, _ := strconv.Atoi(num)
	if n == 0 {
		return IssueRef{}, errors.New("That is not an issue number. Paste an issue URL such as https://github.com/owner/repo/issues/12")
	}
	return IssueRef{Host: host, Owner: owner, Repo: repo, Number: n}, nil
}

var anyURL = regexp.MustCompile(`^https://([A-Za-z0-9.-]+(?::\d+)?)/(` + name + `)/(` + name + `)/(pull|issues)/(\d{1,9})$`)

// ParseGitHubURL reads an https pull request URL (pull) or issue URL on one of hosts; its error is a message to show.
func ParseGitHubURL(s string, hosts []string, pull bool) (IssueRef, error) {
	m := anyURL.FindStringSubmatch(strings.TrimSpace(s))
	want, noun := "issues", "issue"
	if pull {
		want, noun = "pull", "pull request"
	}
	if m == nil || m[4] != want || !slices.Contains(hosts, strings.ToLower(m[1])) {
		return IssueRef{}, fmt.Errorf("the subject must be an https %s URL on a GitHub host the fleet uses, such as https://github.com/owner/repo/%s/12", noun, want)
	}
	return ref(m[1], m[2], m[3], m[5])
}
