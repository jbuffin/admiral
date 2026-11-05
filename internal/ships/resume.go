package ships

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// enterPause is how long Codex needs between a paste and the Enter that submits
// it, as `admiral crew send` waits.
const enterPause = 300 * time.Millisecond

// TypeResume types the resume text of a live ship's report at its prompt (ADR
// 0016's resume of a live ship): into the ship's own pane, never a crew boat's,
// and only while it needs you because its turn ended (reason `stop`). afloat is
// whether the ship is underway or needs you; typed whether the text went in. A
// ship that is busy, or asks a question or a permission, is afloat and not
// typed into: the caller tries again when it stops. One line goes in literally,
// as the chat types /clear, so a slash command runs; more lines go as one
// bracketed paste. Then Enter.
func (s *Service) TypeResume(session, text string) (afloat, typed bool, err error) {
	sess, ok := s.find(session)
	if !ok {
		return false, false, nil
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
	tmux := func(input string, args ...string) error {
		out, err := s.Launcher.RunTmuxInput(ctx, input, args...)
		if err != nil {
			return fmt.Errorf("tmux %s: %v: %s", args[0], err, strings.TrimSpace(out))
		}
		return nil
	}
	pane := sess.PaneID
	if !strings.Contains(text, "\n") {
		err = tmux("", "send-keys", "-t", pane, "-l", text)
	} else {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return true, false, err
		}
		buf := "admiral-resume-" + hex.EncodeToString(b[:])
		if err = tmux(text, "load-buffer", "-b", buf, "-"); err == nil {
			if err = tmux("", "paste-buffer", "-p", "-r", "-d", "-b", buf, "-t", pane); err != nil {
				_ = tmux("", "delete-buffer", "-b", buf)
			}
		}
	}
	if err != nil {
		return true, false, err
	}
	if sh.Harness == "codex" {
		time.Sleep(enterPause)
	}
	if err := tmux("", "send-keys", "-t", pane, "Enter"); err != nil {
		// The text is in the prompt: typing it again would double it.
		return true, true, err
	}
	return true, true, nil
}
