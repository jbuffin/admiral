package chat

// EnqueueTurn queues a turn that can't act for the fleet's chat under a stable id; it only records, TurnTick delivers.
func (s *Service) EnqueueTurn(fleet, id, text string) error { return nil }

// QueuedTurns are the ids of the fleet's undelivered turns, head first.
func (s *Service) QueuedTurns(fleet string) []string { return nil }

// TurnCantAct is whether a queued turn is running in the fleet's chat, so AdmiralBot's writes are refused.
func (s *Service) TurnCantAct(fleet string) bool { return false }

// TurnTick is one pass of the queue's loop for every fleet: it tracks the running turn, types the head item when the
// chat is idle, and carries out a New chat clear that was deferred.
func (s *Service) TurnTick() {}
