package chat

// QueueReports makes the detector call report once for each reporting event of a ship AdmiralBot launched, before it settles the card.
func (d *ShipCards) QueueReports(report func(fleet string, c ShipCard) error) {}

// ReportTurnText is the text of a ship's report turn; reportResult is the Admiral report's result, "" when it could not be read.
func ReportTurnText(c ShipCard, launchText, reportResult string) string { return "" }

// LaunchTextOf is the launch text the chat log's launch line holds for the voyage, "" when it has none.
func (s *Service) LaunchTextOf(fleet, ship, firstLaunched string) string { return "" }

// EnqueueShipReport queues the report turn of the card's event under the card's id.
func (s *Service) EnqueueShipReport(fleet string, c ShipCard, reportResult string) error { return nil }
