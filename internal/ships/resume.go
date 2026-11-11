package ships

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// enterPause is how long Codex needs between a paste and the Enter that submits it.
const enterPause = 300 * time.Millisecond

// TypeResume types only while the ship needs you because its turn ended (reason
// `stop`), never into a crew boat. One line goes in literally, so a slash command
// runs; more lines go as one bracketed paste.
func (s *Service) TypeResume(session, text string) (afloat, typed bool, err error) {
	sess, ok := s.find(session)
	if !ok {
		// Before the host's first snapshot every ship is missing: wait, so a
		// restart does not finish a text for a ship that is still afloat.
		return !s.Host.Known(), false, nil
	}
	sh := s.ship(sess)
	if sh.Status != StatusUnderway && sh.Status != StatusNeedsYou {
		return false, false, nil
	}
	if sh.Status != StatusNeedsYou || sh.Reason != "stop" || sess.PaneID == "" {
		return true, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv := sessionhost.NewServer(s.Launcher.Tmux, s.Launcher.Socket)
	pane := sess.PaneID
	if !strings.Contains(text, "\n") {
		err = srv.SendLiteral(ctx, pane, text)
	} else {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return true, false, err
		}
		buf := "admiral-resume-" + hex.EncodeToString(b[:])
		if err = srv.LoadBuffer(ctx, buf, text); err == nil {
			if err = srv.PasteBuffer(ctx, pane, buf, true); err != nil {
				_ = srv.DeleteBuffer(ctx, buf)
			}
		}
	}
	if err != nil {
		return true, false, err
	}
	if sh.Harness == "codex" {
		time.Sleep(enterPause)
	}
	if err := srv.SendEnter(ctx, pane); err != nil {
		// The text is in the prompt: typing it again would double it.
		return true, true, err
	}
	return true, true, nil
}
