package chat

// ScoutReport is one scout's part of a group report turn.
type ScoutReport struct {
	Card         ShipCard
	LaunchText   string
	FindingsKept bool
}

// QueueScoutReports sets the call that queues a scout group's one report turn; trigger is the scout whose end completed the group.
func (d *ShipCards) QueueScoutReports(report func(fleet string, trigger ShipCard, group []ShipCard) error) {
}

// TurnOver sets the check that a group's engineer turn is over; unset, every turn counts as over.
func (d *ShipCards) TurnOver(over func(fleet, promptKey string) bool) {}

// ScoutGroupTurnText is the text of a scout group's report turn.
func ScoutGroupTurnText(trigger ShipCard, group []ScoutReport) string { return "" }

// EnqueueScoutReport queues a scout group's report turn under the trigger card's id.
func (s *Service) EnqueueScoutReport(fleet string, trigger ShipCard, group []ShipCard) error {
	return nil
}
