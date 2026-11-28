package ships

import (
	"context"
	"errors"
)

// StopAnswered is who answered a stop: Via is "admiralbot" for the AdmiralBot actor token and empty for the engineer.
type StopAnswered struct {
	StopID string `json:"stop_id"`
	Via    string `json:"via,omitempty"`
}

// StopAnswer answers one stop of a ship: a permission prompt by Choice ("allow" or "deny"), a finished turn by Text.
// Reason is the reason the card was posted for; the answer holds only while the pane's live state, Reason and StopID still match.
type StopAnswer struct {
	StopID string
	Reason string
	Choice string
	Text   string
}

// ErrStopMoved is AnswerStop's refusal when the pane no longer waits on that stop; nothing was sent.
var ErrStopMoved = errors.New("the ship is no longer at that stop")

// StopRefused is AnswerStop's refusal of an answer the stop cannot take: a menu with no recorded keys, an unknown choice, text that cannot be typed.
type StopRefused struct{ Msg string }

func (e *StopRefused) Error() string { return e.Msg }

// AnswerStop takes the pane's answer lock, re-reads the live state, reason and stop id, and only then sends the keys or types the text.
func (s *Service) AnswerStop(ctx context.Context, session string, a StopAnswer) error {
	return errors.New("not implemented")
}
