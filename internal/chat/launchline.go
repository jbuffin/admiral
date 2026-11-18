package chat

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/ships"
)

// PostLaunchLine appends a chat launch's line to the fleet's chat log. It starts no turn, and AdmiralBot does not read it.
func (s *Service) PostLaunchLine(fleet string, l ships.LaunchLine) error {
	body := map[string]any{"ship": l.Ship, "name": l.Name, "first_launched": l.FirstLaunched, "fleet": fleet, "kind": l.Kind, "reason": l.Reason, "text": l.Text}
	if l.Title != "" {
		body["title"] = l.Title
	}
	card, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = s.history().Append(fleet, chatlog.Entry{ID: "launch-line-" + l.Ship + "-" + strconv.FormatInt(time.Now().UnixNano(), 36), Kind: chatlog.KindLaunchLine, Text: l.Ship, Card: card})
	return err
}
