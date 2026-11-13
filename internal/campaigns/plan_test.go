package campaigns

import (
	"slices"
	"testing"

	"github.com/jbuffin/admiral/internal/github"
)

const planCampaign = "camp-1"

func wo(n int, opts ...opt) *github.TreeNode {
	return child(n, append([]opt{labelled("ready-for-agent")}, opts...)...)
}

func wy(n int, opts ...opt) *github.TreeNode { return child(n, opts...) }

func closedRoot(children ...*github.TreeNode) *github.TreeNode {
	r := tree(children...)
	r.Closed = true
	return r
}

func unreadableParent(n int) *github.TreeNode {
	p := child(n, closed())
	p.Parent, p.Unreadable = true, 2
	return p
}

func commodore(n int) Child {
	return Child{Campaign: planCampaign, URL: u(n), Title: child(n).Title, Skill: Skill{Kind: "commodore"}}
}

func plain(n int) Child {
	return Child{Campaign: planCampaign, URL: u(n), Title: child(n).Title, Skill: Skill{Kind: "none"}}
}

func urlsOf(n ...int) []string {
	var out []string
	for _, i := range n {
		out = append(out, u(i))
	}
	return out
}

func TestPlan(t *testing.T) {
	mapSkill := Skill{Kind: "skill", Name: "wayfinder", Args: u(1) + " " + u(2)}
	cases := []struct {
		name string
		in   PlanInput
		want Plan
	}{
		{
			name: "a ready Without-you child launches",
			in:   PlanInput{Campaign: Build(Input{Tree: tree(wo(2))}), Followed: true},
			want: Plan{Launch: []Child{commodore(2)}},
		},
		{
			name: "a launch carries the child's skill and URL",
			in: PlanInput{Followed: true, Campaign: Build(Input{Tree: tree(
				child(2, labelled("wayfinder:research")),
			)})},
			want: Plan{Launch: []Child{{Campaign: planCampaign, URL: u(2), Title: "Child 2", Skill: mapSkill}}},
		},
		{
			name: "a cross-repo child launches under its own URL",
			in:   PlanInput{Followed: true, Campaign: Build(Input{Tree: tree(crossRepo(40, "acme/other", labelled("ready-for-agent")))})},
			want: Plan{Launch: []Child{{Campaign: planCampaign, URL: other40, Title: "Child 40", Skill: Skill{Kind: "commodore"}}}},
		},
		{
			name: "a child already launched or fired does not launch again",
			in: PlanInput{
				Followed: true, Launched: urlsOf(2),
				Campaign: Build(Input{Tree: tree(wo(2), wo(3))}),
			},
			want: Plan{Launch: []Child{commodore(3)}},
		},
		{
			name: "a child whose ship anchored with the issue open is not relaunched",
			in: PlanInput{
				Followed: true, Launched: urlsOf(2),
				Campaign: Build(Input{Tree: tree(wo(2))}),
			},
			want: Plan{},
		},
		{
			name: "a blocked child does not launch",
			in:   PlanInput{Followed: true, Campaign: Build(Input{Tree: tree(wo(2, blockedBy(openBlocker(90))))})},
			want: Plan{},
		},
		{
			name: "a blocked child launches on the read where its blocker is closed",
			in: PlanInput{
				Followed: true,
				Campaign: Build(Input{Tree: tree(wo(2, blockedBy(github.Blocker{URL: u(90), Title: "Blocker", Closed: true})))}),
			},
			want: Plan{Launch: []Child{commodore(2)}},
		},
		{
			name: "a child another live ship claims never launches",
			in: PlanInput{
				Followed: true,
				Campaign: Build(Input{Tree: tree(wo(2), wo(3)), Claims: map[string]string{u(2): "Eagle"}}),
			},
			want: Plan{Launch: []Child{commodore(3)}},
		},
		{
			name: "an assigned child never launches",
			in:   PlanInput{Followed: true, Campaign: Build(Input{Tree: tree(wo(2, assigned("someone")))})},
			want: Plan{},
		},
		{
			name: "two ready With-you children propose only the first",
			in:   PlanInput{Followed: true, Campaign: Build(Input{Tree: tree(wy(2), wy(3))})},
			want: Plan{Propose: ptr(plain(2))},
		},
		{
			name: "an open proposal blocks a second proposal",
			in: PlanInput{
				Followed: true, Proposed: u(2),
				Campaign: Build(Input{Tree: tree(wy(2), wy(3))}),
			},
			want: Plan{},
		},
		{
			name: "the fleet's hold blocks a proposal",
			in:   PlanInput{Followed: true, Held: true, Campaign: Build(Input{Tree: tree(wy(2))})},
			want: Plan{},
		},
		{
			name: "the hold does not stop Without-you launches",
			in:   PlanInput{Followed: true, Held: true, Campaign: Build(Input{Tree: tree(wo(2), wy(3))})},
			want: Plan{Launch: []Child{commodore(2)}},
		},
		{
			name: "a With-you child and a Without-you child act in the same plan",
			in:   PlanInput{Followed: true, Campaign: Build(Input{Tree: tree(wy(2), wo(3))})},
			want: Plan{Launch: []Child{commodore(3)}, Propose: ptr(plain(2))},
		},
		{
			name: "a declined child is skipped and the next is proposed",
			in: PlanInput{
				Followed: true, Declined: urlsOf(2),
				Campaign: Build(Input{Tree: tree(wy(2), wy(3))}),
			},
			want: Plan{Propose: ptr(plain(3))},
		},
		{
			name: "every ready With-you child declined proposes nothing",
			in: PlanInput{
				Followed: true, Declined: urlsOf(2, 3),
				Campaign: Build(Input{Tree: tree(wy(2), wy(3))}),
			},
			want: Plan{},
		},
		{
			name: "a With-you child launched on an earlier Yes is not proposed again",
			in: PlanInput{
				Followed: true, Launched: urlsOf(2),
				Campaign: Build(Input{Tree: tree(wy(2), wy(3))}),
			},
			want: Plan{Propose: ptr(plain(3))},
		},
		{
			name: "the proposed child closed: the proposal is withdrawn",
			in: PlanInput{
				Followed: true, Proposed: u(2),
				Campaign: Build(Input{Tree: tree(wy(2, closed()))}),
			},
			want: Plan{Withdraw: u(2)},
		},
		{
			name: "the proposed child was claimed: the proposal is withdrawn",
			in: PlanInput{
				Followed: true, Proposed: u(2),
				Campaign: Build(Input{Tree: tree(wy(2)), Claims: map[string]string{u(2): "Eagle"}}),
			},
			want: Plan{Withdraw: u(2)},
		},
		{
			name: "the proposed child was assigned: the proposal is withdrawn",
			in: PlanInput{
				Followed: true, Proposed: u(2),
				Campaign: Build(Input{Tree: tree(wy(2, assigned("someone")))}),
			},
			want: Plan{Withdraw: u(2)},
		},
		{
			name: "the proposed child got blocked: the proposal is withdrawn",
			in: PlanInput{
				Followed: true, Proposed: u(2),
				Campaign: Build(Input{Tree: tree(wy(2, blockedBy(openBlocker(90))))}),
			},
			want: Plan{Withdraw: u(2)},
		},
		{
			name: "the proposed child is gone from the tree: the proposal is withdrawn",
			in: PlanInput{
				Followed: true, Proposed: u(2),
				Campaign: Build(Input{Tree: tree(wy(3))}),
			},
			want: Plan{Withdraw: u(2)},
		},
		{
			name: "an override made the proposed child Without you: withdrawn, and it launches",
			in: PlanInput{
				Followed: true, Proposed: u(2),
				Campaign: Build(Input{
					Tree:      tree(wy(2)),
					Overrides: map[string]Override{u(2): {Mode: WithoutYou}},
				}),
			},
			want: Plan{Withdraw: u(2), Launch: []Child{plain(2)}},
		},
		{
			name: "a withdrawn child is proposed again once it is ready and nobody declined it",
			in:   PlanInput{Followed: true, Campaign: Build(Input{Tree: tree(wy(2))})},
			want: Plan{Propose: ptr(plain(2))},
		},
		{
			name: "an override makes a With-you child launch",
			in: PlanInput{
				Followed: true,
				Campaign: Build(Input{Tree: tree(wy(2)), Overrides: map[string]Override{u(2): {Mode: WithoutYou}}}),
			},
			want: Plan{Launch: []Child{plain(2)}},
		},
		{
			name: "an override makes a Without-you child a proposal",
			in: PlanInput{
				Followed: true,
				Campaign: Build(Input{Tree: tree(wo(2)), Overrides: map[string]Override{u(2): {Mode: WithYou}}}),
			},
			want: Plan{Propose: ptr(commodore(2))},
		},
		{
			name: "a nested parent's ready children join in tree order",
			in: PlanInput{
				Followed: true,
				Campaign: Build(Input{Tree: tree(wo(2), child(3, kids(wo(4), wo(5))), wo(6))}),
			},
			want: Plan{Launch: []Child{commodore(2), commodore(4), commodore(5), commodore(6)}},
		},
		{
			name: "the first With-you child in tree order, nested ones included, is proposed",
			in: PlanInput{
				Followed: true,
				Campaign: Build(Input{Tree: tree(child(3, kids(wy(4), wy(5))), wy(6))}),
			},
			want: Plan{Propose: ptr(plain(4))},
		},
		{
			name: "all closed ends the commitment",
			in:   PlanInput{Followed: true, Campaign: Build(Input{Tree: closedRoot(child(2, closed()), child(3, closed()))})},
			want: Plan{End: Ended},
		},
		{
			name: "all closed ends the commitment and withdraws an open proposal",
			in: PlanInput{
				Followed: true, Proposed: u(2),
				Campaign: Build(Input{Tree: closedRoot(child(2, closed()))}),
			},
			want: Plan{End: Ended, Withdraw: u(2)},
		},
		{
			name: "all closed and unfollowed is reported as ended",
			in:   PlanInput{Followed: false, Campaign: Build(Input{Tree: closedRoot(child(2, closed()))})},
			want: Plan{End: Ended},
		},
		{
			name: "an open parent with every child closed keeps going",
			in:   PlanInput{Followed: true, Campaign: Build(Input{Tree: tree(child(2, closed()))})},
			want: Plan{},
		},
		{
			name: "an unreadable sub-tree under a closed parent does not end it",
			in:   PlanInput{Followed: true, Campaign: Build(Input{Tree: closedRoot(child(2, closed()), unreadableParent(3))})},
			want: Plan{},
		},
		{
			name: "an unreadable sub-tree deep in a closed tree does not end it",
			in: PlanInput{
				Followed: true,
				Campaign: Build(Input{Tree: closedRoot(child(2, closed(), kids(unreadableParent(3))))}),
			},
			want: Plan{},
		},
		{
			name: "a closed parent with an open child does not end it, and the child launches",
			in: PlanInput{
				Followed: true,
				Campaign: Build(Input{Tree: closedRoot(child(2, closed(), kids(wo(3), child(4, closed()))))}),
			},
			want: Plan{Launch: []Child{commodore(3)}},
		},
		{
			name: "unfollowed ends the commitment",
			in:   PlanInput{Followed: false, Campaign: Build(Input{Tree: tree(wo(2), wy(3))})},
			want: Plan{End: Unfollowed},
		},
		{
			name: "unfollowed ends the commitment and withdraws an open proposal",
			in: PlanInput{
				Followed: false, Proposed: u(3),
				Campaign: Build(Input{Tree: tree(wo(2), wy(3))}),
			},
			want: Plan{End: Unfollowed, Withdraw: u(3)},
		},
		{
			name: "a nil tree does nothing and does not end it",
			in:   PlanInput{Followed: true, Campaign: Build(Input{})},
			want: Plan{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.CampaignID = planCampaign
			got := Decide(tc.in)
			if !slices.Equal(got.Launch, tc.want.Launch) {
				t.Errorf("Launch = %+v, want %+v", got.Launch, tc.want.Launch)
			}
			if (got.Propose == nil) != (tc.want.Propose == nil) || got.Propose != nil && *got.Propose != *tc.want.Propose {
				t.Errorf("Propose = %+v, want %+v", got.Propose, tc.want.Propose)
			}
			if got.Withdraw != tc.want.Withdraw {
				t.Errorf("Withdraw = %q, want %q", got.Withdraw, tc.want.Withdraw)
			}
			if got.End != tc.want.End {
				t.Errorf("End = %q, want %q", got.End, tc.want.End)
			}
		})
	}
}

func ptr(c Child) *Child { return &c }
