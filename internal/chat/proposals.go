package chat

import "github.com/jbuffin/admiral/internal/commitments"

// Propose, Reply, Withdraw and ChatOn make *Service the runner's question seam for With-you children.
var _ commitments.Proposals = (*Service)(nil)

func (s *Service) Propose(fleet string, p commitments.Proposal) error { return nil }

func (s *Service) Reply(fleet, id string) string { return commitments.ProposalGone }

func (s *Service) Withdraw(fleet, id string) {}

func (s *Service) ChatOn(fleet string) bool { return false }
