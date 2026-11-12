package chat

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// perFleetMarker says chat/ already holds per-fleet directories, so chat/questions is a fleet's.
const perFleetMarker = ".per-fleet"

// RemoveOldLayout clears what the single chat left in chat/: its questions, withdrawn so a
// waiting `admiral chat ask` exits 4, and its skill directory, and its session. It runs once, since afterwards
// chat/questions is the directory of a fleet keyed `questions`.
func (s *Service) RemoveOldLayout() error {
	root, err := os.OpenRoot(s.Home)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Mkdir("chat", 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	chat, err := subDir(root, "chat")
	if err != nil {
		return err
	}
	defer chat.Close()
	if _, err := chat.Lstat(perFleetMarker); err == nil {
		return nil
	}
	for _, name := range []string{"questions", ".claude"} {
		if _, err := chat.Lstat(name); err != nil {
			continue
		}
		if err := chat.RemoveAll(name); err != nil {
			return err
		}
		log.Printf("admiral: removed chat/%s, left by the single chat", name)
	}
	// The single chat's session would outlive the layout it was started in, still holding its
	// working directory and a skill that is gone.
	if s.Tmux != "" {
		srv := sessionhost.NewServer(s.Tmux, s.Socket).WithoutUTF8().StderrErrors()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if srv.HasSession(ctx, sessionhost.ChatName) {
			if err := srv.KillSession(ctx, sessionhost.ChatName); err != nil {
				return err
			}
			log.Printf("admiral: ended the %s session, left by the single chat", sessionhost.ChatName)
		}
	}
	f, err := chat.OpenFile(perFleetMarker, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}
